package instance

import (
	"context"
	"fmt"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type recoveryPages struct {
	clients.ECSClient
	requests []*ecs.DescribeInstancesRequest
	empty    bool
}

func (e *recoveryPages) DescribeInstances(_ context.Context, req *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	snapshot := *req
	e.requests = append(e.requests, &snapshot)
	tags := &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{}
	for _, tag := range req.Tag {
		tags.Tag = append(tags.Tag, &ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{TagKey: tag.Key, TagValue: tag.Value})
	}
	rows := []*ecs.DescribeInstancesResponseBodyInstancesInstance{}
	if !e.empty {
		for n := int32(0); n < 100 && (tea.Int32Value(req.PageNumber)-1)*100+n < 101; n++ {
			rows = append(rows, &ecs.DescribeInstancesResponseBodyInstancesInstance{InstanceId: tea.String(fmt.Sprintf("i-%03d", (tea.Int32Value(req.PageNumber)-1)*100+n)), Tags: tags})
		}
	}
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{TotalCount: tea.Int32(101), Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: rows}}}, nil
}

func TestRecoveryQueryRequiresCompleteOwnedPages(t *testing.T) {
	owned := map[string]string{v1alpha1.TagManagedBy: "karpenter", v1alpha1.TagClusterID: "cluster", v1alpha1.TagNodeClaim: "claim", v1alpha1.TagNodeClaimUID: "uid"}
	e := &recoveryPages{}
	p := &Provider{region: "cn-hangzhou", ecsClient: e}
	ids, err := p.FindCreation(context.Background(), owned)
	require.NoError(t, err)
	require.Len(t, ids, 101)
	require.Len(t, e.requests, 2)
	require.EqualValues(t, 2, tea.Int32Value(e.requests[1].PageNumber))
	require.Len(t, e.requests[0].Tag, 4)
	e.empty = true
	_, err = p.FindCreation(context.Background(), owned)
	require.ErrorContains(t, err, "incomplete")
	count := len(e.requests)
	delete(owned, v1alpha1.TagNodeClaimUID)
	_, err = p.FindCreation(context.Background(), owned)
	require.ErrorContains(t, err, "missing recovery ownership")
	require.Len(t, e.requests, count)
}
