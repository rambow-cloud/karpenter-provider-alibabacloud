package clients

import (
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVersionMetadataRequestUsesActualClusterContext(t *testing.T) {
	detail := &cs.DescribeClusterDetailResponseBody{CurrentVersion: tea.String("1.34.10-aliyun.1"), RegionId: tea.String("cn-hangzhou"), ClusterType: tea.String("ManagedKubernetes"), Profile: tea.String("Edge")}
	req, err := VersionMetadataRequest("cn-hangzhou", detail, "containerd")
	require.NoError(t, err)
	require.Equal(t, "Edge", *req.Profile)
	require.Equal(t, "1.34.10-aliyun.1", *req.KubernetesVersion)
	_, err = VersionMetadataRequest("cn-beijing", detail, "containerd")
	require.Error(t, err)
	_, err = VersionMetadataRequest("cn-hangzhou", detail, "docker")
	require.Error(t, err)
	detail.ClusterType = nil
	_, err = VersionMetadataRequest("cn-hangzhou", detail, "containerd")
	require.Error(t, err)
	_, err = VersionMetadataRequest("cn-hangzhou", nil, "containerd")
	require.Error(t, err)
}
