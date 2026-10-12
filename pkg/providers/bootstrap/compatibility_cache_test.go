package bootstrap

import (
	"context"
	"fmt"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type bootstrapCompatibilityCS struct {
	clients.CSClient
	version, profile                        string
	detailError, metadataError              error
	attachCalls, detailCalls, metadataCalls int
	metadata                                []*cs.DescribeKubernetesVersionMetadataResponseBody
	formatRequests                          []bool
}

func (c *bootstrapCompatibilityCS) DescribeClusterDetail(context.Context, string) (*cs.DescribeClusterDetailResponse, error) {
	c.detailCalls++
	return &cs.DescribeClusterDetailResponse{Body: &cs.DescribeClusterDetailResponseBody{CurrentVersion: tea.String(c.version), Profile: tea.String(c.profile), ClusterType: tea.String("ManagedKubernetes")}}, c.detailError
}
func (c *bootstrapCompatibilityCS) DescribeClusterAttachScripts(_ context.Context, _ string, req *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	c.attachCalls++
	c.formatRequests = append(c.formatRequests, tea.BoolValue(req.FormatDisk))
	return fmt.Sprintf("bash attach-%s-%s.sh --runtime containerd --runtime-version old", c.version, c.profile), nil
}

func TestACKDiskFormattingIsValidatedAndCachedSeparately(t *testing.T) {
	c := &bootstrapCompatibilityCS{version: "1.34.10-aliyun.1", profile: "Default"}
	p := NewProvider("cn-hangzhou", c)
	opts := BootstrapOptions{ClusterID: "cluster", ClusterType: ACKClusterType}
	_, err := p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	opts.FormatDisk = true
	_, err = p.GenerateUserData(context.Background(), opts)
	require.ErrorContains(t, err, "persistent")
	require.Equal(t, 1, c.attachCalls)
	disks, err := v1alpha1.NormalizeDisks(v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 40}}})
	require.NoError(t, err)
	opts.Disks = &disks
	_, err = p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	_, err = p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, []bool{false, true}, c.formatRequests)
	disks.DataDisks[0].SnapshotID = "s-existing"
	_, err = p.GenerateUserData(context.Background(), opts)
	require.Error(t, err)
	require.Equal(t, 2, c.attachCalls)
}
func (c *bootstrapCompatibilityCS) DescribeKubernetesVersionMetadata(context.Context, *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	c.metadataCalls++
	if c.metadata != nil {
		return c.metadata, c.metadataError
	}
	return []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String(c.version), Runtimes: []*cs.Runtime{{Name: tea.String("containerd"), Version: tea.String("1.7.27")}}}}, c.metadataError
}

func TestACKBootstrapCacheTracksVersionAndProfile(t *testing.T) {
	c := &bootstrapCompatibilityCS{version: "1.34.10-aliyun.1", profile: "Default"}
	p := NewProvider("cn-hangzhou", c)
	opts := BootstrapOptions{ClusterID: "cluster", ClusterType: ACKClusterType}
	first, err := p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	require.Contains(t, first, "--runtime-version 1.7.27")
	second, err := p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, c.attachCalls)
	require.Equal(t, 2, c.detailCalls)
	c.version = "1.35.7-aliyun.1"
	upgraded, err := p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	require.Contains(t, upgraded, "attach-1.35.7-aliyun.1")
	require.NotContains(t, upgraded, "attach-1.34")
	c.profile = "Edge"
	profileChanged, err := p.GenerateUserData(context.Background(), opts)
	require.NoError(t, err)
	require.Contains(t, profileChanged, "-Edge.sh")
	require.Equal(t, 3, c.attachCalls)
	c.detailError = fmt.Errorf("context unavailable")
	_, err = p.GenerateUserData(context.Background(), opts)
	require.ErrorContains(t, err, "context unavailable", "cache must not mask failures to read the current cluster context")
}

func TestACKBootstrapMetadataFailuresAreNotCached(t *testing.T) {
	for _, scenario := range []string{"API failure", "wrong version", "wrong runtime", "nil entries"} {
		t.Run(scenario, func(t *testing.T) {
			c := &bootstrapCompatibilityCS{version: "1.34.10-aliyun.1", profile: "Default"}
			switch scenario {
			case "API failure":
				c.metadataError = fmt.Errorf("forbidden")
			case "wrong version":
				c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String("1.35.7-aliyun.1"), Runtimes: []*cs.Runtime{{Name: tea.String("containerd"), Version: tea.String("1.7.27")}}}}
			case "wrong runtime":
				c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String(c.version), Runtimes: []*cs.Runtime{{Name: tea.String("docker"), Version: tea.String("20.10")}}}}
			case "nil entries":
				c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{nil, {Version: tea.String(c.version), Runtimes: []*cs.Runtime{nil, {}}}}
			}
			p := NewProvider("cn-hangzhou", c)
			opts := BootstrapOptions{ClusterID: "cluster", ClusterType: ACKClusterType}
			_, err := p.GenerateUserData(context.Background(), opts)
			require.Error(t, err)
			require.Empty(t, p.cache)
			c.metadataError, c.metadata = nil, nil
			result, err := p.GenerateUserData(context.Background(), opts)
			require.NoError(t, err)
			require.Contains(t, result, "--runtime-version 1.7.27")
			require.Equal(t, 2, c.attachCalls)
		})
	}
}

func TestACKRuntimeSelectsExactVersionContainerdSafely(t *testing.T) {
	c := &bootstrapCompatibilityCS{version: "1.34.10-aliyun.1"}
	c.metadata = []*cs.DescribeKubernetesVersionMetadataResponseBody{
		nil,
		{Version: tea.String("wrong-version"), Runtimes: []*cs.Runtime{{Name: tea.String("containerd"), Version: tea.String("wrong-runtime-version")}}},
		{Version: tea.String(c.version), Runtimes: []*cs.Runtime{nil, {Name: tea.String("docker"), Version: tea.String("20.10")}, {Name: tea.String("containerd"), Version: tea.String("1.7.27")}}},
	}
	p := NewProvider("cn-hangzhou", c)
	result, err := p.GenerateUserData(context.Background(), BootstrapOptions{ClusterID: "cluster", ClusterType: ACKClusterType})
	require.NoError(t, err)
	require.Contains(t, result, "--runtime containerd --runtime-version 1.7.27")
	require.NotContains(t, result, "wrong-runtime-version")
}
