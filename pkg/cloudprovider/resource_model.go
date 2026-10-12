package cloudprovider

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/noderesources"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func (c *CloudProvider) resourceModel(ctx context.Context, it *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, imageID string) (*noderesources.Model, error) {
	return noderesources.Compute(ctx, it, nc, c.clusterNetworkConfig, imageID)
}

// Existing instances retain their launch snapshot even if the NodeClass changes
// or disappears. Registered kubelet data is authoritative when identity matches.
func (c *CloudProvider) instanceResources(ctx context.Context, inst *instance.Instance) (corev1.ResourceList, corev1.ResourceList, error) {
	var capacity, allocatable corev1.ResourceList
	if value := inst.Tags[v1alpha1.TagResourceModel]; value != "" {
		var err error
		capacity, allocatable, err = noderesources.Decode(value)
		if err != nil {
			return nil, nil, err
		}
	}
	if c.kubeClient == nil || inst.Tags[v1alpha1.TagNodeClaim] == "" || inst.Tags[v1alpha1.TagNodeClaimUID] == "" {
		return capacity, allocatable, nil
	}
	claim := &coreapis.NodeClaim{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: inst.Tags[v1alpha1.TagNodeClaim]}, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return capacity, allocatable, nil
		}
		return nil, nil, err
	}
	if string(claim.UID) != inst.Tags[v1alpha1.TagNodeClaimUID] {
		return nil, nil, fmt.Errorf("resource lookup NodeClaim UID mismatch")
	}
	if claim.Status.NodeName == "" {
		return capacity, allocatable, nil
	}
	node := &corev1.Node{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: claim.Status.NodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return capacity, allocatable, nil
		}
		return nil, nil, err
	}
	if node.Spec.ProviderID != fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID) {
		return nil, nil, fmt.Errorf("resource lookup node ProviderID mismatch")
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods} {
		q, ok := node.Status.Capacity[key]
		if !ok || q.Sign() <= 0 {
			return capacity, allocatable, nil
		}
		if _, ok := node.Status.Allocatable[key]; !ok {
			return capacity, allocatable, nil
		}
	}
	for key, q := range node.Status.Allocatable {
		cap, ok := node.Status.Capacity[key]
		if !ok || q.Sign() < 0 || q.Cmp(cap) > 0 {
			return nil, nil, fmt.Errorf("invalid registered node allocatable for %s", key)
		}
	}
	return node.Status.Capacity.DeepCopy(), node.Status.Allocatable.DeepCopy(), nil
}
