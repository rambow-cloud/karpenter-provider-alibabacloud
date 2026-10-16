package cloudprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/nodepool"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type terminationCS struct {
	clients.CSClient
	member               bool
	taskState            string
	removeCalls, desired int
	err                  error
}

func (*terminationCS) DescribeClusterNodePools(context.Context, string) (*cs.DescribeClusterNodePoolsResponse, error) {
	panic("unexpected pool list")
}
func (*terminationCS) DescribeClusterNodePoolDetail(context.Context, string, string) (*cs.DescribeClusterNodePoolDetailResponse, error) {
	panic("unexpected pool detail")
}
func (f *terminationCS) DescribeClusterNodes(_ context.Context, cluster string, req *cs.DescribeClusterNodesRequest) (*cs.DescribeClusterNodesResponse, error) {
	if cluster != "cluster-a" || tea.StringValue(req.InstanceIds) != "i-owned" || tea.StringValue(req.State) != "all" {
		panic("unscoped membership read")
	}
	body := &cs.DescribeClusterNodesResponseBody{Page: &cs.DescribeClusterNodesResponseBodyPage{TotalCount: tea.Int32(0)}}
	if f.member {
		body.Page.TotalCount = tea.Int32(1)
		body.Nodes = []*cs.DescribeClusterNodesResponseBodyNodes{{InstanceId: tea.String("i-owned"), NodepoolId: tea.String("np-system"), InstanceRole: tea.String("Worker"), State: tea.String("running")}}
	}
	return &cs.DescribeClusterNodesResponse{Body: body}, f.err
}
func (f *terminationCS) RemoveNodePoolNodes(_ context.Context, cluster, pool string, req *cs.RemoveNodePoolNodesRequest) (*cs.RemoveNodePoolNodesResponse, error) {
	if cluster != "cluster-a" || pool != "np-system" || len(req.InstanceIds) != 1 || tea.StringValue(req.InstanceIds[0]) != "i-owned" || req.DrainNode == nil || *req.DrainNode || req.ReleaseNode == nil || *req.ReleaseNode {
		panic("unsafe native removal")
	}
	f.removeCalls++
	f.desired--
	return &cs.RemoveNodePoolNodesResponse{Body: &cs.RemoveNodePoolNodesResponseBody{TaskId: tea.String("T-removal")}}, nil
}
func (f *terminationCS) DescribeTaskInfo(_ context.Context, task string) (*cs.DescribeTaskInfoResponse, error) {
	return &cs.DescribeTaskInfoResponse{Body: &cs.DescribeTaskInfoResponseBody{TaskId: tea.String(task), ClusterId: tea.String("cluster-a"), State: tea.String(f.taskState)}}, nil
}

type terminationECS struct {
	clients.ECSClient
	row     *ecs.DescribeInstancesResponseBodyInstancesInstance
	deletes int
}

func (f *terminationECS) DescribeInstances(context.Context, *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	body := &ecs.DescribeInstancesResponseBody{TotalCount: tea.Int32(0), PageNumber: tea.Int32(1), PageSize: tea.Int32(100), Instances: &ecs.DescribeInstancesResponseBodyInstances{}}
	if f.deletes == 0 {
		body.TotalCount = tea.Int32(1)
		body.Instances.Instance = []*ecs.DescribeInstancesResponseBodyInstancesInstance{f.row}
	}
	return &ecs.DescribeInstancesResponse{Body: body}, nil
}
func (f *terminationECS) DeleteInstances(context.Context, *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	f.deletes++
	return &ecs.DeleteInstancesResponse{Body: &ecs.DeleteInstancesResponseBody{}}, nil
}
func terminationFixture(t *testing.T) (*CloudProvider, *coreapis.NodeClaim, *terminationCS, *terminationECS) {
	t.Helper()
	claim := &coreapis.NodeClaim{ObjectMeta: metav1.ObjectMeta{Name: "owned", UID: "uid-owned"}, Status: coreapis.NodeClaimStatus{ProviderID: "cn-hangzhou.i-owned"}}
	e := &terminationECS{row: &ecs.DescribeInstancesResponseBodyInstancesInstance{InstanceId: tea.String("i-owned"), RegionId: tea.String("cn-hangzhou"), ZoneId: tea.String("zone-a"), InstanceType: tea.String("ecs.c7.large"), ImageId: tea.String("m-image"), Status: tea.String("Running"), CreationTime: tea.String("2026-10-16T00:00:00Z"), Cpu: tea.Int32(2), Memory: tea.Int32(4096), Tags: &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{}}}
	for k, v := range map[string]string{v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue, v1alpha1.TagClusterID: "cluster-a", v1alpha1.TagNodeClaim: "owned", v1alpha1.TagNodeClaimUID: "uid-owned"} {
		e.row.Tags.Tag = append(e.row.Tags.Tag, &ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{TagKey: tea.String(k), TagValue: tea.String(v)})
	}
	f := &terminationCS{member: true, taskState: "running", desired: 2}
	scheme := clientscheme.Scheme
	k := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim).Build()
	p := &CloudProvider{kubeClient: k, instanceProvider: instance.NewProvider(context.Background(), "cn-hangzhou", e), ackNodePoolProvider: nodepool.New("cluster-a", f)}
	t.Cleanup(p.instanceProvider.Batcher.Close)
	return p, claim, f, e
}

func TestACKTerminationDecrementsNativeCapacityBeforeECSDelete(t *testing.T) {
	p, claim, ack, ecs := terminationFixture(t)
	require.NoError(t, p.Delete(context.Background(), claim))
	require.Equal(t, 1, ack.removeCalls)
	require.Equal(t, 1, ack.desired)
	require.Zero(t, ecs.deletes)
	// A new CloudProvider represents a restarted controller. Its durable receipt
	// prevents a second native decrement while the ACK task is in flight.
	restarted := *p
	require.NoError(t, restarted.Delete(context.Background(), claim))
	require.Equal(t, 1, ack.removeCalls)
	require.Zero(t, ecs.deletes)
	ack.taskState = "success"
	ack.member = false
	require.True(t, coreprovider.IsNodeClaimNotFoundError(restarted.Delete(context.Background(), claim)))
	require.Equal(t, 1, ack.desired)
	require.Equal(t, 1, ack.removeCalls)
	require.Equal(t, 1, ecs.deletes)
}

func TestACKTerminationFailsClosedOnOwnershipOrNativeFailure(t *testing.T) {
	for _, name := range []string{"foreign ownership", "membership denied", "failed task", "membership remains after task"} {
		t.Run(name, func(t *testing.T) {
			p, claim, ack, ecs := terminationFixture(t)
			switch name {
			case "foreign ownership":
				ecs.row.Tags.Tag[0].TagValue = tea.String("foreign")
			case "membership denied":
				ack.err = errors.New("Forbidden.RAM")
			default:
				require.NoError(t, p.Delete(context.Background(), claim))
				ack.taskState = "failed"
				if name == "membership remains after task" {
					ack.taskState = "success"
				}
			}
			require.Error(t, p.Delete(context.Background(), claim))
			require.Zero(t, ecs.deletes)
		})
	}
}

type failingReceiptClient struct {
	client.Client
	patches, failAt int
}

func (f *failingReceiptClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	f.patches++
	if f.patches == f.failAt {
		return errors.New("interrupted receipt write")
	}
	return f.Client.Patch(ctx, obj, patch, opts...)
}

func TestACKTerminationDoesNotRepeatAmbiguousNativeDecrement(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		p, claim, ack, ecs := terminationFixture(t)
		p.kubeClient = &failingReceiptClient{Client: p.kubeClient, failAt: failAt}
		require.ErrorContains(t, p.Delete(context.Background(), claim), "interrupted receipt write")
		require.Equal(t, failAt-1, ack.removeCalls)
		require.Zero(t, ecs.deletes)
		if failAt == 2 {
			// Native removal succeeded, but its task ID was not persisted.
			// Restarting must retain the intent and never submit it a second time.
			restarted := *p
			require.ErrorContains(t, restarted.Delete(context.Background(), claim), "outcome is unknown")
			require.Equal(t, 1, ack.removeCalls)
			require.Equal(t, 1, ack.desired)
			require.Zero(t, ecs.deletes)
		}
	}
}
