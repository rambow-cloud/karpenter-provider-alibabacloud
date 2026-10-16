package instance

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type getResponseECS struct {
	clients.ECSClient
	response *ecs.DescribeInstancesResponse
	calls    int
}

func (f *getResponseECS) DescribeInstances(context.Context, *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	f.calls++
	return f.response, nil
}

func completeGetResponse() *ecs.DescribeInstancesResponse {
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{TotalCount: tea.Int32(1), Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{{
		InstanceId: tea.String("i-worker"), RegionId: tea.String("cn-hangzhou"), ZoneId: tea.String("zone-a"), InstanceType: tea.String("ecs.g7.large"), ImageId: tea.String("m-worker"), Status: tea.String("Running"), CreationTime: tea.String("2026-10-16T00:00:00Z"), Cpu: tea.Int32(2), Memory: tea.Int32(8192),
		VpcAttributes: &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributes{VSwitchId: tea.String("vsw-worker")}, SecurityGroupIds: &ecs.DescribeInstancesResponseBodyInstancesInstanceSecurityGroupIds{SecurityGroupId: tea.StringSlice([]string{"sg-a"})},
	}}}}}
}

func TestGetRejectsPartialAndForeignResponsesWithoutCachingThem(t *testing.T) {
	for name, change := range map[string]func(*ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse{
		"nil response": func(*ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse { return nil },
		"missing body": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse { r.Body = nil; return r },
		"missing count": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.TotalCount = nil
			return r
		},
		"missing rows": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances = nil
			return r
		},
		"nil row": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance[0] = nil
			return r
		},
		"foreign ID": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance[0].InstanceId = tea.String("i-foreign")
			return r
		},
		"foreign region": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance[0].RegionId = tea.String("cn-shanghai")
			return r
		},
		"count mismatch": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.TotalCount = tea.Int32(2)
			return r
		},
		"duplicate rows": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance = append(r.Body.Instances.Instance, r.Body.Instances.Instance[0])
			return r
		},
		"missing CPU": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance[0].Cpu = nil
			return r
		},
		"missing image": func(r *ecs.DescribeInstancesResponse) *ecs.DescribeInstancesResponse {
			r.Body.Instances.Instance[0].ImageId = nil
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			api := &getResponseECS{response: change(completeGetResponse())}
			p := NewProvider(context.Background(), "cn-hangzhou", api)
			t.Cleanup(p.Batcher.Close)
			_, err := p.Get(context.Background(), "i-worker", false)
			require.Error(t, err)
			require.False(t, coreprovider.IsNodeClaimNotFoundError(err), "malformed is not confirmed absent")
			api.response = completeGetResponse()
			got, err := p.Get(context.Background(), "i-worker", false)
			require.NoError(t, err)
			require.Equal(t, "i-worker", got.InstanceID)
			require.Equal(t, 2, api.calls)
		})
	}
}

func TestGetDoesNotCacheUnobservedNetworkMetadata(t *testing.T) {
	api := &getResponseECS{response: completeGetResponse()}
	api.response.Body.Instances.Instance[0].SecurityGroupIds = nil
	p := NewProvider(context.Background(), "cn-hangzhou", api)
	t.Cleanup(p.Batcher.Close)
	got, err := p.Get(context.Background(), "i-worker", false)
	require.NoError(t, err)
	require.Nil(t, got.SecurityGroupIDs)
	api.response = completeGetResponse()
	got, err = p.Get(context.Background(), "i-worker", false)
	require.NoError(t, err)
	require.Equal(t, []string{"sg-a"}, got.SecurityGroupIDs)
	require.Equal(t, 2, api.calls)
	api.response = &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{TotalCount: tea.Int32(0)}}
	_, err = p.Get(context.Background(), "i-worker", true)
	require.True(t, coreprovider.IsNodeClaimNotFoundError(err), "explicit zero-count absence remains not found")
}
