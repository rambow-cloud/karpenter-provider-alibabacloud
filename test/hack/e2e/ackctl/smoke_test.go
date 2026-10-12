package main

import (
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type smokeFake struct {
	describe func(*ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error)
	deleted  bool
}

func (f *smokeFake) DescribeInstances(r *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	return f.describe(r)
}
func (f *smokeFake) DeleteInstances(r *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	f.deleted = true
	return &ecs.DeleteInstancesResponse{}, nil
}

func smokeResponse(id, next, run string) *ecs.DescribeInstancesResponse {
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{NextToken: tea.String(next), Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{{InstanceId: tea.String(id), Tags: &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{Tag: []*ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{
		{TagKey: tea.String(smokeRunTag), TagValue: tea.String(run)},
		{TagKey: tea.String(v1alpha1.TagClusterID), TagValue: tea.String("c-test")},
		{TagKey: tea.String(v1alpha1.TagManagedBy), TagValue: tea.String("karpenter")},
	}}}}}}}
}

func TestSmokeCloudPaginationOwnershipAndRedactedOutput(t *testing.T) {
	run := "ack-123456789"
	pages := 0
	f := &smokeFake{describe: func(r *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
		if tea.BoolValue(r.DryRun) {
			return nil, &tea.SDKError{Code: tea.String("DryRunOperation")}
		}
		pages++
		require.Len(t, r.Tag, 3)
		if pages == 1 {
			return smokeResponse("i-one", "next", run), nil
		}
		require.Equal(t, "next", tea.StringValue(r.NextToken))
		return smokeResponse("i-two", "", run), nil
	}}
	rows, err := smokeInstances(f, "cn-hangzhou", "c-test", run, nil)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotContains(t, rows[0], "UserData")
	f.describe = func(r *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
		if tea.BoolValue(r.DryRun) {
			return nil, &tea.SDKError{Code: tea.String("DryRunOperation")}
		}
		return smokeResponse("i-foreign", "", "ack-foreign"), nil
	}
	_, err = smokeInstances(f, "cn-hangzhou", "c-test", run, nil)
	require.Error(t, err)
	require.Error(t, releaseSmokeInstance(f, "cn-hangzhou", "c-test", run, "i-foreign"))
	require.False(t, f.deleted)
}

func TestSmokeCloudSparseDeniedAndRepeatedPaginationFail(t *testing.T) {
	for _, mode := range []string{"denied", "sparse", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			f := &smokeFake{describe: func(r *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
				if mode == "denied" {
					return nil, &tea.SDKError{Code: tea.String("Forbidden")}
				}
				if tea.BoolValue(r.DryRun) {
					return nil, &tea.SDKError{Code: tea.String("DryRunOperation")}
				}
				if mode == "sparse" {
					return &ecs.DescribeInstancesResponse{}, nil
				}
				return smokeResponse("i-repeated", "same", "ack-123456789"), nil
			}}
			_, err := smokeInstances(f, "cn-hangzhou", "c-test", "ack-123456789", nil)
			require.Error(t, err)
		})
	}
}
