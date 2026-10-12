package imagefamily

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"testing"
)

type prerequisitesCS struct {
	*compatibilityCS
	addons   []*cs.ListClusterAddonInstancesResponseBodyAddons
	addonErr error
	calls    int
}

func (c *prerequisitesCS) ListClusterAddonInstances(context.Context, string) (*cs.ListClusterAddonInstancesResponse, error) {
	c.calls++
	return &cs.ListClusterAddonInstancesResponse{Body: &cs.ListClusterAddonInstancesResponseBody{Addons: c.addons}}, c.addonErr
}

func TestLinux4NetworkPrerequisitesAndFreshRevalidation(t *testing.T) {
	c, _, p, nc := compatibilityFixture()
	c.metadata[0].Images[0].ImageType = tea.String("AliyunLinux4ContainerOptimized")
	extended := &prerequisitesCS{compatibilityCS: c, addons: []*cs.ListClusterAddonInstancesResponseBodyAddons{{Name: tea.String("terway-eniip"), State: tea.String("active"), Version: tea.String("v1.16.4-aliyun.1")}}}
	p.ack.client = extended
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.Len(t, images, 1)
	extended.addons[0].Version = tea.String("v1.16.3")
	_, err = p.RevalidateForLaunch(context.Background(), nc, images)
	require.ErrorContains(t, err, "Terway")
	require.Equal(t, 2, extended.calls, "metadata cache must not hide addon downgrades")
	extended.addons[0].Version = tea.String("v1.9.18")
	extended.addons[0].State = tea.String("upgrading")
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "not active")
	extended.addonErr = fmt.Errorf("permission denied")
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "permission denied")
	extended.addonErr = nil
	extended.addons = nil
	_, err = p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "incomplete")
}
func TestTerwayVersionBoundaries(t *testing.T) {
	for raw, want := range map[string]bool{"1.9.17": false, "v1.9.18": true, "1.9.99-aliyun.1": true, "1.10.0": false, "1.15.99": false, "1.16.3": false, "1.16.4": true, "2.0.0": true, "1.16.4-beta.1": false, "unknown": false, "1.16": false} {
		require.Equal(t, want, supportedTerway(raw), raw)
	}
}
func TestUbuntuAndCustomRequireExplicitVerification(t *testing.T) {
	c, _, p, nc := compatibilityFixture()
	c.metadata[0].Images[0].ImageType = tea.String("Ubuntu24.04")
	_, err := p.ResolveForNodeClass(context.Background(), nc)
	require.ErrorContains(t, err, "imagePrerequisitesVerified")
	nc.Spec.ImagePrerequisitesVerified = true
	images, err := p.ResolveForNodeClass(context.Background(), nc)
	require.NoError(t, err)
	require.ErrorContains(t, ValidateNodeLabels(images[0], map[string]string{"alibabacloud.com/image-accelerate-enabled": "true"}), "cannot enable")
	require.NoError(t, ValidateNodeLabels(images[0], nil))
	require.Error(t, v1alpha1.ValidateImageCompatibilityPolicy(v1alpha1.ECSNodeClassSpec{ImageCompatibilityPolicy: "Custom"}))
}
func TestHardwareRestrictionsCannotBeWaived(t *testing.T) {
	image := v1alpha1.Image{ImageType: "ContainerOS", Architecture: "x86_64"}
	spec := v1alpha1.ECSNodeClassSpec{ImagePrerequisitesVerified: true}
	it := &instancetype.InstanceType{Name: "ecs-test"}
	require.ErrorContains(t, ValidateHardware(spec, image, it), "unknown")
	it.LocalDiskKnown = true
	require.NoError(t, ValidateHardware(spec, image, it))
	it.LocalDiskCount = 1
	require.ErrorContains(t, ValidateHardware(spec, image, it), "instance-local")
	it.LocalDiskCount = 0
	image.ImageType = "AliyunLinux3GPU"
	require.ErrorContains(t, ValidateHardware(spec, image, it), "GPU hardware")
	count := resource.MustParse("1")
	it.GPU = &instancetype.GPU{Count: &count, Model: "NVIDIA T4"}
	require.NoError(t, ValidateHardware(spec, image, it))
	spec.ImagePrerequisitesVerified = false
	require.ErrorContains(t, ValidateHardware(spec, image, it), "verification")
	spec.ImagePrerequisitesVerified = true
	image.ImageType = "AliyunLinux3"
	require.ErrorContains(t, ValidateHardware(spec, image, it), "matching GPU image")
	spec.ImageCompatibilityPolicy = "Custom"
	require.NoError(t, ValidateHardware(spec, image, it))
	it.GPU.Model = "AMD MI300X"
	require.ErrorContains(t, ValidateHardware(spec, image, it), "non-NVIDIA")
}
