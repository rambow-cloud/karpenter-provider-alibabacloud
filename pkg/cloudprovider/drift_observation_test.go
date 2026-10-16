package cloudprovider

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type observationECS struct {
	clients.ECSClient
	observe func(*ecs.DescribeInstancesResponse)
	calls   int
}

func (f *observationECS) DescribeInstances(ctx context.Context, req *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	if tea.StringValue(req.InstanceIds) != `["i-worker"]` || tea.StringValue(req.RegionId) != "cn-hangzhou" {
		panic("unscoped drift observation")
	}
	f.calls++
	r, _ := (&driftECS{}).DescribeInstances(ctx, req)
	if f.observe != nil {
		f.observe(r)
	}
	return r, nil
}

func observationFixture(t *testing.T, api *observationECS) (*CloudProvider, *coreapis.NodeClaim) {
	t.Helper()
	n := &v1alpha1.ECSNodeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1},
		Status: v1alpha1.ECSNodeClassStatus{
			Images: []v1alpha1.Image{{ID: "m-worker"}}, VSwitches: []v1alpha1.VSwitch{{ID: "vsw-worker"}},
			SecurityGroups: []v1alpha1.SecurityGroup{{ID: "sg-a"}, {ID: "sg-b"}},
			Conditions:     []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}},
		},
	}
	hash, err := n.Hash()
	require.NoError(t, err)
	claim := &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Name: n.Name}}, Status: coreapis.NodeClaimStatus{ProviderID: "cn-hangzhou.i-worker"}}
	setNodeClassHashAnnotation(claim, hash)
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	p := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(n).Build(), instanceProvider: instance.NewProvider(context.Background(), "cn-hangzhou", api)}
	t.Cleanup(p.instanceProvider.Batcher.Close)
	return p, claim
}

func TestDriftUsesFreshECSInsteadOfWarmCache(t *testing.T) {
	ctx := context.Background()
	api := &observationECS{observe: func(r *ecs.DescribeInstancesResponse) {
		r.Body.Instances.Instance[0].SecurityGroupIds.SecurityGroupId = tea.StringSlice([]string{"sg-stale"})
	}}
	p, claim := observationFixture(t, api)
	_, err := p.instanceProvider.Get(ctx, "i-worker", false)
	require.NoError(t, err)
	api.observe = nil
	cached, err := p.instanceProvider.Get(ctx, "i-worker", false)
	require.NoError(t, err)
	require.Equal(t, []string{"sg-stale"}, cached.SecurityGroupIDs)
	require.Equal(t, 1, api.calls)
	reason, err := p.IsDrifted(ctx, claim)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, 2, api.calls, "drift must read through the stale general cache")

	for _, groups := range [][]string{{"sg-a"}, {"sg-a", "sg-b", "sg-extra"}, {}} {
		api.observe = func(r *ecs.DescribeInstancesResponse) {
			// Preserve an explicitly observed empty slice, distinct from unknown.
			r.Body.Instances.Instance[0].SecurityGroupIds.SecurityGroupId = make([]*string, 0, len(groups))
			for _, group := range groups {
				r.Body.Instances.Instance[0].SecurityGroupIds.SecurityGroupId = append(r.Body.Instances.Instance[0].SecurityGroupIds.SecurityGroupId, tea.String(group))
			}
		}
		reason, err = p.IsDrifted(ctx, claim)
		require.NoError(t, err)
		require.Equal(t, SecurityGroupDrift, reason, "fresh actual differences remain drift even with a warm cache")
	}
	api.observe = nil
	reason, err = p.IsDrifted(ctx, claim)
	require.NoError(t, err)
	require.Empty(t, reason, "a later matching observation must allow upstream to clear drift")
}

func TestDriftDefersIncompleteNetworkObservationsAndRecovers(t *testing.T) {
	for name, change := range map[string]func(*ecs.DescribeInstancesResponseBodyInstancesInstance){
		"missing group object": func(i *ecs.DescribeInstancesResponseBodyInstancesInstance) { i.SecurityGroupIds = nil },
		"missing group list":   func(i *ecs.DescribeInstancesResponseBodyInstancesInstance) { i.SecurityGroupIds.SecurityGroupId = nil },
		"nil group entry": func(i *ecs.DescribeInstancesResponseBodyInstancesInstance) {
			i.SecurityGroupIds.SecurityGroupId[0] = nil
		},
		"blank group entry": func(i *ecs.DescribeInstancesResponseBodyInstancesInstance) {
			i.SecurityGroupIds.SecurityGroupId[0] = tea.String("")
		},
		"missing subnet": func(i *ecs.DescribeInstancesResponseBodyInstancesInstance) { i.VpcAttributes = nil },
	} {
		t.Run(name, func(t *testing.T) {
			api := &observationECS{observe: func(r *ecs.DescribeInstancesResponse) { change(r.Body.Instances.Instance[0]) }}
			p, claim := observationFixture(t, api)
			reason, err := p.IsDrifted(context.Background(), claim)
			require.Error(t, err)
			require.Empty(t, reason, "unknown must not be reported as actual drift")
			api.observe = nil
			reason, err = p.IsDrifted(context.Background(), claim)
			require.NoError(t, err)
			require.Empty(t, reason)
		})
	}
}

func TestStaticNodeClassDriftDoesNotDependOnPartialCloudObservation(t *testing.T) {
	api := &observationECS{observe: func(r *ecs.DescribeInstancesResponse) { r.Body = nil }}
	p, claim := observationFixture(t, api)
	claim.Annotations[v1alpha1.AnnotationECSNodeClassHash] = "previous-configuration"
	reason, err := p.IsDrifted(context.Background(), claim)
	require.NoError(t, err)
	require.Equal(t, NodeClassDrift, reason)
	require.Zero(t, api.calls, "known static drift should not spend a cloud read")
}
