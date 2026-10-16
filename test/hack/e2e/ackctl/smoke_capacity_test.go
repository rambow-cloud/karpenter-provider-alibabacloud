package main

import (
	"testing"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestVPCInventoryIncludesUntaggedACKReplacements(t *testing.T) {
	f := &smokeFake{describe: func(req *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
		require.Equal(t, "vpc-dedicated", tea.StringValue(req.VpcId))
		require.Empty(t, req.Tag)
		if tea.BoolValue(req.DryRun) {
			return nil, &tea.SDKError{Code: tea.String("DryRunOperation")}
		}
		return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{{InstanceId: tea.String("i-native"), VpcAttributes: &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributes{VpcId: tea.String("vpc-dedicated")}}}}}}, nil
	}}
	rows, err := smokeInstances(f, "cn-hangzhou", "c-test", "", nil, "vpc-dedicated")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "i-native", rows[0]["InstanceId"])
	require.False(t, f.deleted)
}

type capacityFake struct{ foreign bool }

func (*capacityFake) DescribeClusterNodePoolDetail(*string, *string) (*cs.DescribeClusterNodePoolDetailResponse, error) {
	return &cs.DescribeClusterNodePoolDetailResponse{Body: &cs.DescribeClusterNodePoolDetailResponseBody{NodepoolInfo: &cs.DescribeClusterNodePoolDetailResponseBodyNodepoolInfo{NodepoolId: tea.String("np-test")}, ScalingGroup: &cs.DescribeClusterNodePoolDetailResponseBodyScalingGroup{DesiredSize: tea.Int64(5), ScalingGroupId: tea.String("asg-test")}, Status: &cs.DescribeClusterNodePoolDetailResponseBodyStatus{TotalNodes: tea.Int64(1)}}}, nil
}
func (f *capacityFake) DescribeClusterNodes(_ *string, req *cs.DescribeClusterNodesRequest) (*cs.DescribeClusterNodesResponse, error) {
	pool := "np-test"
	if f.foreign {
		pool = "np-foreign"
	}
	return &cs.DescribeClusterNodesResponse{Body: &cs.DescribeClusterNodesResponseBody{Page: &cs.DescribeClusterNodesResponseBodyPage{TotalCount: tea.Int32(1)}, Nodes: []*cs.DescribeClusterNodesResponseBodyNodes{{InstanceId: tea.String("i-baseline"), NodepoolId: tea.String(pool)}}}}, nil
}
func TestNativeCapacityDoesNotConfuseDesiredWithCurrentCount(t *testing.T) {
	state, err := smokeNativeCapacity(&capacityFake{}, "c-test", "np-test")
	require.NoError(t, err)
	require.EqualValues(t, 5, state["desired_size"])
	require.EqualValues(t, 1, state["total_nodes"])
	require.Equal(t, []string{"i-baseline"}, state["member_ids"])
	_, err = smokeNativeCapacity(&capacityFake{foreign: true}, "c-test", "np-test")
	require.Error(t, err)
}
