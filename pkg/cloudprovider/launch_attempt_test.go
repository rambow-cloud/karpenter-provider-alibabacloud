package cloudprovider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// Simulate an idempotent ECS service, including creation with a lost response.
type recoverECS struct {
	imageLaunchECS
	runErrors   []error
	lookupError error
	visible     bool
	duplicate   bool
	tokens      map[string]string
	raw         *ecs.DescribeInstancesResponseBodyInstancesInstance
}

func (e *recoverECS) RunInstances(_ context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	e.requests = append(e.requests, req)
	var err error
	if len(e.runErrors) > 0 {
		err, e.runErrors = e.runErrors[0], e.runErrors[1:]
	}
	// A definite capacity rejection never creates the instance.
	if _, ok := err.(*tea.SDKError); ok {
		return nil, err
	}
	token := tea.StringValue(req.ClientToken)
	if e.tokens == nil {
		e.tokens = map[string]string{}
	}
	id := e.tokens[token]
	if id == "" {
		id = fmt.Sprintf("i-test-%d", len(e.tokens)+1)
		e.tokens[token] = id
		tags := &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{}
		for _, tag := range req.Tag {
			tags.Tag = append(tags.Tag, &ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{TagKey: tag.Key, TagValue: tag.Value})
		}
		e.raw = &ecs.DescribeInstancesResponseBodyInstancesInstance{
			InstanceId: tea.String(id), RegionId: tea.String("cn-hangzhou"), ZoneId: tea.String("cn-hangzhou-i"),
			InstanceType: req.InstanceType, ImageId: req.ImageId, Status: tea.String("Running"),
			CreationTime: tea.String("2026-10-12T00:00:00Z"), Cpu: tea.Int32(2), Memory: tea.Int32(8192),
			InstanceChargeType: req.InstanceChargeType, Tags: tags,
		}
	}
	if err != nil {
		return nil, err
	}
	return &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: []*string{tea.String(id)}}}}, nil
}

func (e *recoverECS) DescribeInstances(_ context.Context, req *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	if e.lookupError != nil {
		return nil, e.lookupError
	}
	rows := []*ecs.DescribeInstancesResponseBodyInstancesInstance{}
	if e.visible && e.raw != nil {
		rows = append(rows, e.raw)
		if e.duplicate && req.InstanceIds == nil {
			other := *e.raw
			other.InstanceId = tea.String("i-duplicate")
			rows = append(rows, &other)
		}
	}
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{
		TotalCount: tea.Int32(int32(len(rows))), PageNumber: tea.Int32(1), PageSize: tea.Int32(100),
		Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: rows},
	}}, nil
}
func (e *recoverECS) TagResources(context.Context, *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	return &ecs.TagResourcesResponse{}, nil
}

func launchFixture(t *testing.T, e *recoverECS) (context.Context, *CloudProvider, *coreapis.NodeClaim, instance.CreateOptions, string) {
	t.Helper()
	ctx, cancel := context.WithCancel((&options.Options{ClusterID: "cluster-test"}).ToContext(context.Background()))
	t.Cleanup(cancel)
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	scheme.AddKnownTypes(schema.GroupVersion{Group: "karpenter.sh", Version: "v1"}, &coreapis.NodeClaim{}, &coreapis.NodeClaimList{})
	class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Status: v1alpha1.ECSNodeClassStatus{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	claim := &coreapis.NodeClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", UID: types.UID("uid-original")}, Spec: coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Name: "class"}}}
	tags, err := clusterScopedInstanceTags(ctx, claim, class)
	require.NoError(t, err)
	hash, err := class.Hash()
	require.NoError(t, err)
	provider := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(class, claim).Build(), instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", e), instanceTypeProvider: instancetype.NewProvider("cn-hangzhou", e)}
	t.Cleanup(provider.instanceProvider.Batcher.Close)
	return ctx, provider, claim, instance.CreateOptions{InstanceType: "ecs.g7.large", ImageID: "m-x86", VSwitchID: "vsw-test", SecurityGroupIDs: []string{"sg-test"}, Tags: tags, UserData: "bootstrap-secret", SystemDisk: instance.SystemDisk{Category: "cloud_essd", Size: 40}}, hash
}

func TestLaunchRecoversAfterRestart(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-response=%t", lost), func(t *testing.T) {
			e := &recoverECS{visible: true}
			if lost {
				e.runErrors = []error{fmt.Errorf("response timeout")}
			}
			ctx, p, claim, opts, hash := launchFixture(t, e)
			id, err := p.launchInstance(ctx, claim, opts, hash)
			if lost {
				require.ErrorContains(t, err, "timeout")
			} else {
				require.NoError(t, err)
				e.lookupError = fmt.Errorf("temporary DescribeInstances failure")
				_, err = p.instanceProvider.Get(ctx, id, true)
				require.Error(t, err)
				e.lookupError = nil
			}
			// Lose all process-local state. Only Kubernetes and ECS survive.
			restarted := &CloudProvider{kubeClient: p.kubeClient, instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", e), instanceTypeProvider: instancetype.NewProvider("cn-hangzhou", e)}
			t.Cleanup(restarted.instanceProvider.Batcher.Close)
			result, err := restarted.Create(ctx, claim)
			require.NoError(t, err)
			require.Equal(t, "cn-hangzhou.i-test-1", result.Status.ProviderID)
			require.Equal(t, hash, result.Annotations[v1alpha1.AnnotationECSNodeClassHash])
			require.Len(t, e.requests, 1)
			current, attempt, err := p.readLaunch(ctx, claim)
			require.NoError(t, err)
			require.Equal(t, "i-test-1", attempt.InstanceID)
			require.NotContains(t, current.Annotations[v1alpha1.AnnotationLaunchAttempt], "bootstrap-secret")
		})
	}
}

func TestPendingLaunchReusesTokenAndRejectsChangedParameters(t *testing.T) {
	e := &recoverECS{runErrors: []error{fmt.Errorf("response lost"), fmt.Errorf("response lost")}}
	ctx, p, claim, opts, hash := launchFixture(t, e)
	_, err := p.launchInstance(ctx, claim, opts, hash)
	require.Error(t, err)
	for n := 0; n < 30; n++ {
		opts.Tags[fmt.Sprintf("aaa-%02d", n)] = "value"
	}
	changed := opts
	changed.VSwitchID = "vsw-other"
	_, err = p.launchInstance(ctx, claim, changed, hash)
	require.ErrorContains(t, err, "parameters changed")
	// Restore the exact intent, including tags. Indexed Tag.N ordering must agree.
	for n := 0; n < 30; n++ {
		delete(opts.Tags, fmt.Sprintf("aaa-%02d", n))
	}
	_, err = p.launchInstance(ctx, claim, opts, hash)
	require.Error(t, err)
	require.Len(t, e.requests, 2)
	require.Equal(t, e.requests[0], e.requests[1])
	require.Len(t, e.tokens, 1)
	require.Len(t, tea.StringValue(e.requests[0].ClientToken), 64)
	require.EqualValues(t, 1, tea.Int32Value(e.requests[0].Amount))
	require.Equal(t, "PostPaid", tea.StringValue(e.requests[0].InstanceChargeType))
}

func TestDefiniteRejectionAllowsNewAttemptAndUIDSeparatesRecreatedClaim(t *testing.T) {
	for _, code := range []string{"OperationDenied.NoStock", "InvalidVSwitchId.IpNotEnough"} {
		t.Run(code, func(t *testing.T) {
			e := &recoverECS{runErrors: []error{&tea.SDKError{Code: tea.String(code)}}}
			ctx, p, claim, opts, hash := launchFixture(t, e)
			_, err := p.launchInstance(ctx, claim, opts, hash)
			require.Error(t, err)
			opts.VSwitchID = "vsw-fallback"
			_, err = p.launchInstance(ctx, claim, opts, hash)
			require.NoError(t, err)
			require.NotEqual(t, tea.StringValue(e.requests[0].ClientToken), tea.StringValue(e.requests[1].ClientToken))
			require.NoError(t, p.kubeClient.Delete(ctx, claim))
			next := claim.DeepCopy()
			next.ResourceVersion = ""
			next.UID = types.UID("uid-recreated")
			require.NoError(t, p.kubeClient.Create(ctx, next))
			opts.Tags[v1alpha1.TagNodeClaimUID] = string(next.UID)
			_, err = p.launchInstance(ctx, next, opts, hash)
			require.NoError(t, err)
			require.NotEqual(t, tea.StringValue(e.requests[1].ClientToken), tea.StringValue(e.requests[2].ClientToken))
		})
	}
}

type failingLaunchPatch struct{ client.Client }

func (f failingLaunchPatch) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return fmt.Errorf("API write unavailable")
}

func TestLaunchFailsClosedBeforeRunWhenIntentCannotPersist(t *testing.T) {
	e := &recoverECS{}
	ctx, p, claim, opts, hash := launchFixture(t, e)
	realClient := p.kubeClient
	p.kubeClient = failingLaunchPatch{realClient}
	_, err := p.launchInstance(ctx, claim, opts, hash)
	require.ErrorContains(t, err, "persist launch intent")
	p.kubeClient = realClient
	stale := claim.DeepCopy()
	stale.UID = types.UID("stale")
	_, err = p.launchInstance(ctx, stale, opts, hash)
	require.ErrorContains(t, err, "identity changed")
	require.Empty(t, e.requests)
}

func TestRecoveryRejectsAmbiguousOrForeignInstances(t *testing.T) {
	for _, tc := range []string{"duplicate", "ownership", "query failure"} {
		t.Run(tc, func(t *testing.T) {
			e := &recoverECS{visible: true, runErrors: []error{fmt.Errorf("response timeout")}}
			ctx, p, claim, opts, hash := launchFixture(t, e)
			_, err := p.launchInstance(ctx, claim, opts, hash)
			require.Error(t, err)
			switch tc {
			case "duplicate":
				e.duplicate = true
			case "ownership":
				e.raw.Tags.Tag[0].TagValue = tea.String("foreign")
			case "query failure":
				e.lookupError = fmt.Errorf("permission denied")
			}
			_, err = p.Create(ctx, claim)
			require.Error(t, err)
			require.Len(t, e.requests, 1)
		})
	}
}

func TestOwnershipTagsAlwaysAccompanyRunInstances(t *testing.T) {
	e := &recoverECS{}
	ctx, p, claim, opts, hash := launchFixture(t, e)
	for n := 0; n < 40; n++ {
		opts.Tags[fmt.Sprintf("aaa-%02d", n)] = "value"
	}
	_, err := p.launchInstance(ctx, claim, opts, hash)
	require.NoError(t, err)
	require.Len(t, e.requests[0].Tag, 20)
	for _, key := range []string{v1alpha1.TagManagedBy, v1alpha1.TagClusterID, v1alpha1.TagNodeClaim, v1alpha1.TagNodeClaimUID} {
		found := false
		for _, tag := range e.requests[0].Tag {
			if tea.StringValue(tag.Key) == key {
				found = true
			}
		}
		require.True(t, found, key)
	}
	// Only opaque hashes may be stored in Kubernetes.
	current := &coreapis.NodeClaim{}
	require.NoError(t, p.kubeClient.Get(ctx, client.ObjectKeyFromObject(claim), current))
	require.False(t, strings.Contains(current.Annotations[v1alpha1.AnnotationLaunchAttempt], "bootstrap-secret"))
}

func TestPendingLaunchSurvivesUnreadyNodeClass(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(fmt.Sprintf("visible=%t", visible), func(t *testing.T) {
			e := &recoverECS{visible: visible, runErrors: []error{fmt.Errorf("response timeout")}}
			ctx, p, claim, opts, hash := launchFixture(t, e)
			_, err := p.launchInstance(ctx, claim, opts, hash)
			require.Error(t, err)
			class := &v1alpha1.ECSNodeClass{}
			require.NoError(t, p.kubeClient.Get(ctx, client.ObjectKey{Name: "class"}, class))
			class.Generation = 2
			require.NoError(t, p.kubeClient.Update(ctx, class))
			result, err := p.Create(ctx, claim)
			if visible {
				require.NoError(t, err)
				require.Equal(t, "cn-hangzhou.i-test-1", result.Status.ProviderID)
			} else {
				require.ErrorContains(t, err, "unresolved launch waiting")
				require.False(t, coreprovider.IsNodeClassNotReadyError(err), "core must keep the claim carrying its unresolved intent")
			}
			require.Len(t, e.requests, 1)
		})
	}
}

type staleLaunchClient struct{ client.Client }

func (s staleLaunchClient) Get(ctx context.Context, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
	if err := s.Client.Get(ctx, key, object, opts...); err != nil {
		return err
	}
	if claim, ok := object.(*coreapis.NodeClaim); ok {
		delete(claim.Annotations, v1alpha1.AnnotationLaunchAttempt)
	}
	return nil
}
func TestLaunchUsesUncachedReader(t *testing.T) {
	e := &recoverECS{}
	ctx, p, claim, opts, hash := launchFixture(t, e)
	p.launchReader = p.kubeClient
	p.kubeClient = staleLaunchClient{p.kubeClient}
	_, err := p.launchInstance(ctx, claim, opts, hash)
	require.NoError(t, err)
	_, attempt, err := p.readLaunch(ctx, claim)
	require.NoError(t, err)
	require.Equal(t, "i-test-1", attempt.InstanceID)
}
