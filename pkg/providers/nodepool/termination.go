package nodepool

import (
	"context"
	"fmt"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

// ACK adoption changes native desired capacity even with autoscaling disabled.
// RemoveNodePoolNodes relinquishes that capacity before ECS termination.
type lifecycleAPI interface {
	DescribeClusterNodes(context.Context, string, *cs.DescribeClusterNodesRequest) (*cs.DescribeClusterNodesResponse, error)
	RemoveNodePoolNodes(context.Context, string, string, *cs.RemoveNodePoolNodesRequest) (*cs.RemoveNodePoolNodesResponse, error)
	DescribeTaskInfo(context.Context, string) (*cs.DescribeTaskInfoResponse, error)
}

type Member struct {
	PoolID string
	State  string
}

func (p *Provider) ClusterID() string { return p.clusterID }

func (p *Provider) LookupInstance(ctx context.Context, id string) (*Member, error) {
	api, ok := p.client.(lifecycleAPI)
	if !ok || p.clusterID == "" || id == "" {
		return nil, fmt.Errorf("ACK node lifecycle client or identity is unavailable")
	}
	response, err := api.DescribeClusterNodes(ctx, p.clusterID, &cs.DescribeClusterNodesRequest{
		InstanceIds: tea.String(id), State: tea.String("all"), PageNumber: tea.String("1"), PageSize: tea.String("100"),
	})
	if err != nil {
		return nil, fmt.Errorf("read ACK instance membership: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.Page == nil || response.Body.Page.TotalCount == nil ||
		int32(len(response.Body.Nodes)) != *response.Body.Page.TotalCount || len(response.Body.Nodes) > 1 {
		return nil, fmt.Errorf("incomplete ACK instance membership response")
	}
	if len(response.Body.Nodes) == 0 {
		return nil, nil
	}
	n := response.Body.Nodes[0]
	if n == nil || tea.StringValue(n.InstanceId) != id || tea.StringValue(n.InstanceRole) != "Worker" || tea.StringValue(n.NodepoolId) == "" || tea.StringValue(n.State) == "" {
		return nil, fmt.Errorf("ACK instance membership identity is incomplete or mismatched")
	}
	return &Member{PoolID: *n.NodepoolId, State: *n.State}, nil
}

func (p *Provider) RemoveInstance(ctx context.Context, poolID, id string) (string, error) {
	api, ok := p.client.(lifecycleAPI)
	if !ok || p.clusterID == "" || poolID == "" || id == "" {
		return "", fmt.Errorf("ACK removal identity or client is unavailable")
	}
	r, err := api.RemoveNodePoolNodes(ctx, p.clusterID, poolID, &cs.RemoveNodePoolNodesRequest{
		InstanceIds: tea.StringSlice([]string{id}), DrainNode: tea.Bool(false), ReleaseNode: tea.Bool(false), Concurrency: tea.Bool(false),
	})
	if err != nil {
		return "", fmt.Errorf("remove owned ACK instance: %w", err)
	}
	if r == nil || r.Body == nil || tea.StringValue(r.Body.TaskId) == "" {
		return "", fmt.Errorf("ACK removal returned no task identity; ECS termination deferred")
	}
	return *r.Body.TaskId, nil
}

func (p *Provider) RemovalComplete(ctx context.Context, taskID string) (bool, error) {
	api, ok := p.client.(lifecycleAPI)
	if !ok || taskID == "" {
		return false, fmt.Errorf("ACK removal task identity or client is unavailable")
	}
	r, err := api.DescribeTaskInfo(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("read ACK removal task: %w", err)
	}
	if r == nil || r.Body == nil || tea.StringValue(r.Body.TaskId) != taskID || tea.StringValue(r.Body.ClusterId) != p.clusterID {
		return false, fmt.Errorf("ACK removal task identity is incomplete or mismatched")
	}
	switch tea.StringValue(r.Body.State) {
	case "running":
		return false, nil
	case "success":
		return true, nil
	default:
		// Do not erase failed/unknown task evidence and silently delete ECS.
		return false, fmt.Errorf("ACK removal task %s did not succeed", taskID)
	}
}
