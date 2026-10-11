// Copyright 2026 The Alibaba Cloud Karpenter Authors.
// SPDX-License-Identifier: Apache-2.0

package batcher

import (
	"context"
	"errors"
	"math"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type migrationECSClient struct {
	run func(context.Context, *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error)
}

func (c migrationECSClient) RunInstances(ctx context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	return c.run(ctx, req)
}

func TestV2BatchPreservesRequestAndCallerOwnership(t *testing.T) {
	req := &ecs.RunInstancesRequest{
		Amount: tea.Int32(1), ClientToken: tea.String("original-token"),
		InstanceChargeType: tea.String("PostPaid"), LaunchTemplateId: tea.String("lt-1"),
		HttpTokens: tea.String("required"), DeletionProtection: tea.Bool(true),
		SystemDisk: &ecs.RunInstancesRequestSystemDisk{Category: tea.String("cloud_essd"), Size: tea.String("40")},
		Tag:        []*ecs.RunInstancesRequestTag{{Key: tea.String("owner"), Value: tea.String("test")}},
		DataDisk:   []*ecs.RunInstancesRequestDataDisk{{Size: tea.Int32(120), DeleteWithInstance: tea.Bool(false)}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := NewRunInstancesExecutor(migrationECSClient{run: func(gotCtx context.Context, got *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
		require.Same(t, ctx, gotCtx)
		require.Equal(t, int32(2), tea.Int32Value(got.Amount))
		require.Equal(t, "cn-hangzhou", tea.StringValue(got.RegionId))
		require.Equal(t, "original-token", tea.StringValue(got.ClientToken))
		require.Equal(t, "PostPaid", tea.StringValue(got.InstanceChargeType))
		require.Equal(t, "lt-1", tea.StringValue(got.LaunchTemplateId))
		require.Equal(t, "required", tea.StringValue(got.HttpTokens))
		require.True(t, tea.BoolValue(got.DeletionProtection))
		require.False(t, tea.BoolValue(got.DataDisk[0].DeleteWithInstance))
		*got.SystemDisk.Size = "80"
		*got.Tag[0].Value = "mutated"
		*got.DataDisk[0].Size = 240
		return &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice([]string{"i-1", "i-2"})}}}, nil
	}}, "cn-hangzhou")
	items := []*BatchItem{
		{Request: &RunInstancesRequest{Request: req}, ResultCh: make(chan *BatchResult, 1)},
		{Request: &RunInstancesRequest{Request: req}, ResultCh: make(chan *BatchResult, 1)},
	}
	require.NoError(t, executor.Execute(ctx, items))
	require.Equal(t, "i-1", (<-items[0].ResultCh).Value.(*RunInstancesResult).InstanceID)
	require.Equal(t, "i-2", (<-items[1].ResultCh).Value.(*RunInstancesResult).InstanceID)
	require.Equal(t, int32(1), tea.Int32Value(req.Amount))
	require.Nil(t, req.RegionId)
	require.Equal(t, "40", tea.StringValue(req.SystemDisk.Size))
	require.Equal(t, "test", tea.StringValue(req.Tag[0].Value))
	require.Equal(t, int32(120), tea.Int32Value(req.DataDisk[0].Size))
}

func TestV2BatchEmptyAndPartialResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		response    *ecs.RunInstancesResponse
		wantSuccess int
	}{
		{"nil response", nil, 0},
		{"nil body", &ecs.RunInstancesResponse{}, 0},
		{"missing IDs", &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{}}, 0},
		{"partial IDs", &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: []*string{nil, tea.String(""), tea.String("i-one")}}}}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := NewRunInstancesExecutor(migrationECSClient{run: func(context.Context, *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
				return test.response, nil
			}}, "cn-hangzhou")
			items := []*BatchItem{
				{Request: &RunInstancesRequest{Request: &ecs.RunInstancesRequest{}}, ResultCh: make(chan *BatchResult, 1)},
				{Request: &RunInstancesRequest{Request: &ecs.RunInstancesRequest{}}, ResultCh: make(chan *BatchResult, 1)},
			}
			require.ErrorContains(t, executor.Execute(context.Background(), items), "insufficient instances")
			for index, item := range items {
				result := <-item.ResultCh
				if index < test.wantSuccess {
					require.NoError(t, result.Error)
				} else {
					require.Error(t, result.Error)
				}
			}
		})
	}
}

func TestV2BatchKeyIncludesAllRequestParameters(t *testing.T) {
	base := &ecs.RunInstancesRequest{RegionId: tea.String("cn-hangzhou"), ImageId: tea.String("m-1")}
	for _, change := range []func(*ecs.RunInstancesRequest){
		func(r *ecs.RunInstancesRequest) { r.InstanceChargeType = tea.String("PrePaid") },
		func(r *ecs.RunInstancesRequest) { r.LaunchTemplateId = tea.String("lt-1") },
		func(r *ecs.RunInstancesRequest) { r.HttpTokens = tea.String("required") },
		func(r *ecs.RunInstancesRequest) { r.ClientToken = tea.String("different-token") },
		func(r *ecs.RunInstancesRequest) {
			r.Tag = []*ecs.RunInstancesRequestTag{{Key: tea.String("owner"), Value: tea.String("different")}}
		},
	} {
		changed := *base
		change(&changed)
		require.NotEqual(t, ComputeRunInstancesBatchKey(base), ComputeRunInstancesBatchKey(&changed))
	}
	changed := *base
	changed.Amount = tea.Int32(9)
	require.Equal(t, ComputeRunInstancesBatchKey(base), ComputeRunInstancesBatchKey(&changed))
	base.Tag = []*ecs.RunInstancesRequestTag{{Key: tea.String("b"), Value: tea.String("2")}, {Key: tea.String("a"), Value: tea.String("1")}}
	changed = *base
	changed.Tag = []*ecs.RunInstancesRequestTag{base.Tag[1], base.Tag[0]}
	require.Equal(t, ComputeRunInstancesBatchKey(base), ComputeRunInstancesBatchKey(&changed))
	require.Equal(t, "b", tea.StringValue(base.Tag[0].Key))
}

func TestV2BatchRejectsInvalidRequestsWithoutCloudCalls(t *testing.T) {
	batcher := NewInstanceBatcher(migrationECSClient{run: func(context.Context, *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
		t.Fatal("invalid request reached cloud client")
		return nil, errors.New("unexpected call")
	}}, "cn-hangzhou")
	defer batcher.Close()
	_, err := batcher.CreateInstance(context.Background(), nil)
	require.Error(t, err)
	_, err = batcher.CreateInstance(context.Background(), &ecs.RunInstancesRequest{SpotPriceLimit: tea.Float32(float32(math.NaN()))})
	require.ErrorContains(t, err, "cannot be encoded")
}
