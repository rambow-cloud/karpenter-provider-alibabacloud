package cloudprovider

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/noderesources"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"testing"
)

func TestResourceSnapshotSurvivesCreateGetListAndRecovery(t *testing.T) {
	e := &recoverECS{visible: true}
	ctx, p, claim, opts, hash := launchFixture(t, e)
	cap := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("7Gi"), corev1.ResourcePods: resource.MustParse("20"), corev1.ResourceEphemeralStorage: resource.MustParse("37Gi")}
	alloc := cap.DeepCopy()
	alloc[corev1.ResourceMemory] = resource.MustParse("6Gi")
	tag, err := (&noderesources.Model{Capacity: cap, Allocatable: alloc}).Encode()
	require.NoError(t, err)
	opts.Tags[v1alpha1.TagResourceModel] = tag
	for i := range 30 {
		opts.Tags[fmt.Sprintf("user-%02d", i)] = "value"
	}
	id, err := p.launchInstance(ctx, claim, opts, hash)
	require.NoError(t, err)
	require.LessOrEqual(t, len(e.requests[0].Tag), 20)
	found := false
	for _, item := range e.requests[0].Tag {
		if *item.Key == v1alpha1.TagResourceModel {
			found = true
			require.Equal(t, tag, *item.Value)
		}
	}
	require.True(t, found, "snapshot must accompany the first cloud mutation")
	recovered, err := p.Create(ctx, claim)
	require.NoError(t, err)
	get, err := p.Get(ctx, "cn-hangzhou."+id)
	require.NoError(t, err)
	list, err := p.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	for _, result := range []*coreapis.NodeClaim{recovered, get, list[0]} {
		require.Equal(t, cap.Memory().Value(), result.Status.Capacity.Memory().Value())
		require.Equal(t, alloc.Memory().Value(), result.Status.Allocatable.Memory().Value())
		q := result.Status.Allocatable[corev1.ResourceEphemeralStorage]
		require.EqualValues(t, 37<<30, q.Value())
	}
	require.Len(t, e.requests, 1, "recovery must preserve the launch instead of issuing a new request")
}

func TestInstanceResourcesUseLaunchSnapshotThenRegisteredKubelet(t *testing.T) {
	ctx := context.Background()
	cap := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"), corev1.ResourcePods: resource.MustParse("20")}
	alloc := cap.DeepCopy()
	alloc[corev1.ResourceCPU] = resource.MustParse("3920m")
	tag, err := (&noderesources.Model{Capacity: cap, Allocatable: alloc}).Encode()
	require.NoError(t, err)
	inst := &instance.Instance{Region: "cn-hangzhou", InstanceID: "i-test", Tags: map[string]string{v1alpha1.TagResourceModel: tag, v1alpha1.TagNodeClaim: "claim", v1alpha1.TagNodeClaimUID: "uid-test"}}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	scheme.AddKnownTypes(schema.GroupVersion{Group: "karpenter.sh", Version: "v1"}, &coreapis.NodeClaim{}, &coreapis.NodeClaimList{})
	claim := &coreapis.NodeClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", UID: "uid-test"}}
	p := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(claim).WithObjects(claim).Build()}
	a, b, err := p.instanceResources(ctx, inst)
	require.NoError(t, err)
	require.Equal(t, cap.Cpu().MilliValue(), a.Cpu().MilliValue())
	require.Equal(t, alloc.Memory().Value(), b.Memory().Value())
	actual := cap.DeepCopy()
	actual[corev1.ResourceEphemeralStorage] = resource.MustParse("37Gi")
	actualAlloc := actual.DeepCopy()
	actualAlloc[corev1.ResourceEphemeralStorage] = resource.MustParse("32Gi")
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}, Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-test"}, Status: corev1.NodeStatus{Capacity: actual, Allocatable: actualAlloc}}
	claim.Status.NodeName = node.Name
	p.kubeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim, node).Build()
	a, b, err = p.instanceResources(ctx, inst)
	require.NoError(t, err)
	require.Equal(t, actual, a)
	require.Equal(t, actualAlloc, b)
	result, err := p.convertInstanceToNodeClaim(ctx, inst, &coreapis.NodeClaim{}, nil, "c-test")
	require.NoError(t, err)
	require.Equal(t, b, result.Status.Allocatable)
	inst.Tags[v1alpha1.TagNodeClaimUID] = "different-uid"
	_, _, err = p.instanceResources(ctx, inst)
	require.ErrorContains(t, err, "UID mismatch")
	inst.Tags[v1alpha1.TagNodeClaimUID] = "uid-test"
	inst.InstanceID = "i-other"
	_, _, err = p.instanceResources(ctx, inst)
	require.ErrorContains(t, err, "ProviderID mismatch")
}
