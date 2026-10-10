/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package garbagecollection

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	corev1 "k8s.io/api/core/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"time"
)

// GarbageCollectionController delegates confirmed cloud absence to upstream lifecycle cleanup.
type GarbageCollectionController struct {
	client                     client.Client
	cloudProvider              cloudprovider.CloudProvider
	creationProtectionDuration time.Duration
}

func NewGarbageCollectionController(kubeClient client.Client, provider cloudprovider.CloudProvider, protection *time.Duration) *GarbageCollectionController {
	duration := time.Minute
	if protection != nil {
		duration = *protection
	}
	return &GarbageCollectionController{client: kubeClient, cloudProvider: provider, creationProtectionDuration: duration}
}
func (c *GarbageCollectionController) Name() string { return "instance.garbagecollection" }
func (c *GarbageCollectionController) Reconcile(ctx context.Context) (reconciler.Result, error) {
	claims := &coreapis.NodeClaimList{}
	if err := c.client.List(ctx, claims); err != nil {
		return reconciler.Result{}, err
	}
	candidates := make([]*coreapis.NodeClaim, 0, len(claims.Items))
	for i := range claims.Items {
		claim := &claims.Items[i]
		ref := claim.Spec.NodeClassRef
		if ref == nil || ref.Group != v1alpha1.GroupVersion.Group || ref.Kind != "ECSNodeClass" ||
			claim.Status.ProviderID == "" || !claim.DeletionTimestamp.IsZero() ||
			!claim.StatusConditions().Get(coreapis.ConditionTypeRegistered).IsTrue() ||
			time.Since(claim.CreationTimestamp.Time) < c.creationProtectionDuration {
			continue
		}
		candidates = append(candidates, claim)
	}
	if len(candidates) == 0 {
		return reconciler.Result{RequeueAfter: time.Minute}, nil
	}
	instances, err := c.cloudProvider.List(ctx)
	if err != nil {
		return reconciler.Result{}, fmt.Errorf("listing cloud instances: %w", err)
	}
	present := make(map[string]bool, len(instances))
	for _, claim := range instances {
		present[claim.Status.ProviderID] = true
	}
	nodes := &corev1.NodeList{}
	if err := c.client.List(ctx, nodes); err != nil {
		return reconciler.Result{}, err
	}
	for _, claim := range candidates {
		if present[claim.Status.ProviderID] {
			continue
		}
		// A live kubelet overrides an eventually consistent inventory omission.
		ready := false
		for _, node := range nodes.Items {
			if node.Spec.ProviderID != claim.Status.ProviderID {
				continue
			}
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
					ready = true
				}
			}
		}
		if ready {
			continue
		}
		_, err := c.cloudProvider.Get(ctx, claim.Status.ProviderID)
		if err == nil {
			continue
		}
		if !cloudprovider.IsNodeClaimNotFoundError(err) {
			return reconciler.Result{}, fmt.Errorf("confirming absence of %s: %w", claim.Name, err)
		}
		// Upstream finalizers own cloud termination and Node deletion.
		if err := client.IgnoreNotFound(c.client.Delete(ctx, claim)); err != nil {
			return reconciler.Result{}, fmt.Errorf("deleting absent nodeclaim %s: %w", claim.Name, err)
		}
		log.FromContext(ctx).Info("Requested lifecycle cleanup for absent instance", "nodeClaim", claim.Name, "providerID", claim.Status.ProviderID)
	}
	return reconciler.Result{RequeueAfter: time.Minute}, nil
}
func (c *GarbageCollectionController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).Named(c.Name()).WatchesRawSource(singleton.Source()).Complete(singleton.AsReconciler(c))
}
