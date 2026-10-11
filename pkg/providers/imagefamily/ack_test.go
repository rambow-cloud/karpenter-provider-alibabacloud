package imagefamily

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type compatibilityCS struct {
	clients.CSClient
	version  string
	metadata []*cs.DescribeKubernetesVersionMetadataResponseBody
	err      error
	requests []*cs.DescribeKubernetesVersionMetadataRequest
}

func TestACKAutomaticAlternativesAreFilteredButPinsRemainStrict(t *testing.T) {
	c, e, p, nc := compatibilityFixture()
	c.metadata[0].Images = append(c.metadata[0].Images, &cs.DescribeKubernetesVersionMetadataResponseBodyImages{ImageId: tea.String("m-container"), ImageType: tea.String("ContainerOS4"), OsType: tea.String("Linux"), Architecture: tea.String("x86_64")})
	e.images["m-container"] = e.images["m-approved"]
	container := e.images["m-container"]
	container.ImageId = tea.String("m-container")
	e.images["m-container"] = container
	nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("system")}}
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Equal(t, []v1alpha1.Image{{ID: "m-approved", Name: "compatible-node", Architecture: "x86_64", OSType: "linux", ImageType: "AliyunLinux3ContainerOptimized"}}, images)

	images, err = p.RevalidateForLaunch(context.Background(), nc, []v1alpha1.Image{{ID: "m-approved"}, {ID: "m-stale"}})
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, "m-approved", images[0].ID)
	_, err = p.RevalidateForLaunch(context.Background(), nc, []v1alpha1.Image{{ID: "m-stale"}})
	require.ErrorContains(t, err, "no status images remain")
	nc.Spec.ImageSelectorTerms = append(nc.Spec.ImageSelectorTerms, v1alpha1.ImageSelectorTerm{ID: tea.String("m-container")})
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "exactly one", "invalid explicit pin remains mandatory even alongside a valid automatic selector")
	nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-stale")}}
	_, err = p.RevalidateForLaunch(context.Background(), nc, []v1alpha1.Image{{ID: "m-approved"}})
	require.ErrorContains(t, err, "pin is not replaced")
}

func TestACKRevalidationDoesNotExpandStatus(t *testing.T) {
	c, e, p, nc := compatibilityFixture()
	entry := *c.metadata[0].Images[0]
	entry.ImageId = tea.String("m-ecs-latest")
	c.metadata[0].Images = append(c.metadata[0].Images, &entry)
	images, err := p.RevalidateForLaunch(context.Background(), nc, []v1alpha1.Image{{ID: "m-approved"}})
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, "m-approved", images[0].ID)
	require.Contains(t, e.requested[0], "m-ecs-latest", "new candidate is discovered but must wait for status reconciliation")
}

func TestACKRetainsCreationTimeAndSortsOnlyApprovedCandidates(t *testing.T) {
	c, e, p, nc := compatibilityFixture()
	entry := *c.metadata[0].Images[0]
	entry.ImageId = tea.String("m-ecs-latest")
	c.metadata[0].Images = append(c.metadata[0].Images, &entry)
	old := e.images["m-approved"]
	old.CreationTime = tea.String("2025-01-01T00:00:00Z")
	e.images["m-approved"] = old
	newer := e.images["m-ecs-latest"]
	newer.CreationTime = tea.String("2026-10-01T00:00:00Z")
	e.images["m-ecs-latest"] = newer
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Equal(t, []string{"m-ecs-latest", "m-approved"}, imageIDs(images))
	require.Equal(t, "2026-10-01T00:00:00Z", images[0].CreationTime)
	// A single explicit older pin stays selected even though a newer approved image exists.
	nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-approved")}}
	images, err = p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Equal(t, []string{"m-approved"}, imageIDs(images))
}

func TestACKCacheWaitCanBeCanceled(t *testing.T) {
	_, _, p, nc := compatibilityFixture()
	p.ack.gate <- struct{}{}
	defer func() { <-p.ack.gate }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.ResolveForNodeClass(ctx, nc); done <- err }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("cache waiter ignored cancellation")
	}
}

func (c *compatibilityCS) DescribeClusterDetail(context.Context, string) (*cs.DescribeClusterDetailResponse, error) {
	return &cs.DescribeClusterDetailResponse{Body: &cs.DescribeClusterDetailResponseBody{CurrentVersion: tea.String(c.version), ClusterType: tea.String("ManagedKubernetes"), RegionId: tea.String("cn-hangzhou")}}, nil
}
func (c *compatibilityCS) DescribeKubernetesVersionMetadata(_ context.Context, req *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	c.requests = append(c.requests, req)
	return c.metadata, c.err
}

type compatibilityECS struct {
	clients.ECSClient
	images    map[string]ecs.DescribeImagesResponseBodyImagesImage
	requested [][]string
}

func (c *compatibilityECS) DescribeImages(_ context.Context, ids []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	if len(ids) == 0 || len(filters) != 0 {
		return nil, fmt.Errorf("expected precise IDs, not an ECS latest-family query")
	}
	c.requested = append(c.requested, append([]string(nil), ids...))
	var result []ecs.DescribeImagesResponseBodyImagesImage
	for _, id := range ids {
		if img, ok := c.images[id]; ok {
			result = append(result, img)
		}
	}
	return result, nil
}
func compatibilityFixture() (*compatibilityCS, *compatibilityECS, *Provider, *v1alpha1.ECSNodeClass) {
	c := &compatibilityCS{version: "1.34.10-aliyun.1"}
	c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String(c.version), Images: []*cs.DescribeKubernetesVersionMetadataResponseBodyImages{{ImageId: tea.String("m-approved"), ImageType: tea.String("AliyunLinux3ContainerOptimized"), OsType: tea.String("Linux"), Architecture: tea.String("x86_64")}}}}
	e := &compatibilityECS{images: map[string]ecs.DescribeImagesResponseBodyImagesImage{}}
	for _, id := range []string{"m-approved", "m-ecs-latest", "m-custom"} {
		e.images[id] = ecs.DescribeImagesResponseBodyImagesImage{ImageId: tea.String(id), ImageFamily: tea.String("acs:family"), ImageName: tea.String("compatible-node"), ImageOwnerAlias: tea.String("system"), OSType: tea.String("linux"), Architecture: tea.String("x86_64"), Status: tea.String("Available")}
	}
	p := NewProvider(e, WithACKCompatibility(c, "cn-hangzhou", "cluster-a"))
	nc := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{ClusterID: "cluster-a", ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{{ImageFamily: tea.String("acs:family")}}}}
	return c, e, p, nc
}
func TestACKFamilyUsesApprovedIDsAndRejectsUnapprovedPin(t *testing.T) {
	c, e, p, nc := compatibilityFixture()
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, "m-approved", images[0].ID)
	require.Equal(t, "linux", images[0].OSType)
	require.Equal(t, [][]string{{"m-approved"}}, e.requested)
	require.Equal(t, "cn-hangzhou", *c.requests[0].Region)
	require.Equal(t, "ManagedKubernetes", *c.requests[0].ClusterType)
	require.Equal(t, "Default", *c.requests[0].Profile)
	require.Equal(t, "containerd", *c.requests[0].Runtime)
	require.Equal(t, c.version, *c.requests[0].KubernetesVersion)
	nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-ecs-latest")}}
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "pin is not replaced")
}
func TestACKMetadataFailuresNeverFallbackAndRecover(t *testing.T) {
	for _, failure := range []string{"error", "empty", "wrong version", "missing image type", "unavailable", "unknown OS", "architecture mismatch"} {
		t.Run(failure, func(t *testing.T) {
			c, e, p, nc := compatibilityFixture()
			saved := c.metadata
			img := e.images["m-approved"]
			switch failure {
			case "error":
				c.err = fmt.Errorf("forbidden")
			case "empty":
				c.metadata = nil
			case "wrong version":
				c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String("1.35.1-aliyun.1"), Images: saved[0].Images}}
			case "missing image type":
				entry := *saved[0].Images[0]
				entry.ImageType = nil
				c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String(c.version), Images: []*cs.DescribeKubernetesVersionMetadataResponseBodyImages{&entry}}}
			case "unavailable":
				changed := img
				changed.Status = tea.String("Unavailable")
				e.images["m-approved"] = changed
			case "unknown OS":
				changed := img
				changed.OSType = nil
				e.images["m-approved"] = changed
			case "architecture mismatch":
				changed := img
				changed.Architecture = tea.String("arm64")
				e.images["m-approved"] = changed
			}
			_, err := p.ResolveForNodeClass(context.Background(), nc)
			require.Error(t, err)
			for _, ids := range e.requested {
				require.NotContains(t, ids, "m-ecs-latest")
			}
			c.err, c.metadata, e.images["m-approved"] = nil, saved, img
			images, err := p.ResolveForNodeClass(context.Background(), nc)
			require.NoError(t, err)
			require.Len(t, images, 1)
		})
	}
}
func TestACKUpgradeReplacesCachedCompatibilityAndRechecksLegacyStatus(t *testing.T) {
	c, _, p, nc := compatibilityFixture()
	_, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	c.version = "1.35.7-aliyun.1"
	c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String(c.version), Images: []*cs.DescribeKubernetesVersionMetadataResponseBodyImages{{ImageId: tea.String("m-ecs-latest"), OsType: tea.String("Linux"), ImageType: tea.String("AliyunLinux3ContainerOptimized"), Architecture: tea.String("x86_64")}}}}
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Equal(t, "m-ecs-latest", images[0].ID)
	require.Len(t, c.requests, 2)
	_, err = p.RevalidateForLaunch(context.Background(), nc, []v1alpha1.Image{{ID: "m-approved"}})
	require.ErrorContains(t, err, "no status images remain")
}
func TestCustomImagePolicyRequiresExactSelfSharedLinux(t *testing.T) {
	for _, test := range []struct {
		owner, os string
		allowed   bool
	}{{"self", "linux", true}, {"others", "linux", true}, {"system", "linux", false}, {"marketplace", "linux", false}, {"self", "windows", false}, {"others", "", false}} {
		t.Run(test.owner+"/"+test.os, func(t *testing.T) {
			c, e, p, nc := compatibilityFixture()
			nc.Spec.ImageCompatibilityPolicy = "Custom"
			nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-custom")}}
			img := e.images["m-custom"]
			img.ImageOwnerAlias, img.OSType = tea.String(test.owner), tea.String(test.os)
			e.images["m-custom"] = img
			_, err := p.ResolveForNodeClass(context.Background(), nc)
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Empty(t, c.requests)
			nc.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{Name: tea.String("*")}}
			_, err = p.ResolveForNodeClass(context.Background(), nc)
			require.ErrorContains(t, err, "exact image IDs")
		})
	}
}
func TestContainerOSDiskAndLinuxBoundary(t *testing.T) {
	image := v1alpha1.Image{ID: "lifsea_3_x64_5G_alibase_20260716.qcow2", Architecture: "x86_64", OSType: "linux", ImageType: "ContainerOS"}
	for _, test := range []struct {
		name  string
		spec  v1alpha1.ECSNodeClassSpec
		valid bool
	}{
		{"missing", v1alpha1.ECSNodeClassSpec{}, false},
		{"valid", v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 40}}}, true},
		{"invalid size", v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 1}}}, false},
		{"multiple", v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 40}, {Category: "cloud_essd", Size: 40}}}, false},
		{"template disk unknown", v1alpha1.ECSNodeClassSpec{LaunchTemplateID: tea.String("lt-test")}, false},
		{"local disk", v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "ephemeral_ssd", Size: 40}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBootImages(test.spec, []v1alpha1.Image{image})
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, imageType := range []string{"ContainerOS4", "ContainerOSGPU"} {
		image.ImageType = imageType
		require.Error(t, ValidateBootImages(v1alpha1.ECSNodeClassSpec{}, []v1alpha1.Image{image}))
	}
	image.ImageType = "AliyunLinux4ContainerOptimized"
	require.NoError(t, ValidateBootImages(v1alpha1.ECSNodeClassSpec{}, []v1alpha1.Image{image}))
	image.OSType = "windows"
	require.ErrorContains(t, ValidateBootImages(v1alpha1.ECSNodeClassSpec{}, []v1alpha1.Image{image}), "only Linux")
}

func TestACKContainerOSResolutionRejectsMissingDiskAndLaunchTemplate(t *testing.T) {
	c, _, p, nc := compatibilityFixture()
	c.metadata[0].Images[0].ImageType = tea.String("ContainerOS4")
	_, err := p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "exactly one")
	nc.Spec.DataDisks = []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 40}}
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Equal(t, "ContainerOS4", images[0].ImageType)
	nc.Spec.LaunchTemplateID = tea.String("lt-test")
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "launchTemplateID is unsupported")
}
