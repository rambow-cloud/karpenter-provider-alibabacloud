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

package garbagecollection_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/garbagecollection"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"testing"
	"time"
)

type inventoryProvider struct {
	cloudprovider.CloudProvider
	listed               []*coreapis.NodeClaim
	listErr, getErr      error
	lists, gets, deletes int
}

func (p *inventoryProvider) List(context.Context) ([]*coreapis.NodeClaim, error) {
	p.lists++
	return p.listed, p.listErr
}
func (p *inventoryProvider) Get(context.Context, string) (*coreapis.NodeClaim, error) {
	p.gets++
	if p.getErr != nil {
		return nil, p.getErr
	}
	return &coreapis.NodeClaim{}, nil
}
func (p *inventoryProvider) Delete(context.Context, *coreapis.NodeClaim) error {
	p.deletes++
	return errors.New("GC must never terminate instances")
}

func TestGarbageCollectionSafety(t *testing.T) {
	absent := cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance is absent"))
	tests := []struct {
		name                  string
		mutate                func(*coreapis.NodeClaim, *corev1.Node)
		listed                bool
		listErr, getErr       error
		wantDelete, wantError bool
		wantGets, wantLists   int
	}{
		{name: "list omission but Get confirms live instance", wantGets: 1, wantLists: 1},
		{name: "confirmed absence delegates to lifecycle", getErr: absent, wantDelete: true, wantGets: 1, wantLists: 1},
		{name: "Get timeout preserves resources", getErr: errors.New("timeout"), wantError: true, wantGets: 1, wantLists: 1},
		{name: "List error preserves resources", listErr: errors.New("inventory unavailable"), wantError: true, wantLists: 1},
		{name: "listed instance needs no Get", listed: true, wantLists: 1},
		{name: "empty provider ID", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.Status.ProviderID = "" }},
		{name: "unregistered claim", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.Status.Conditions = nil }},
		{name: "foreign group", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.Spec.NodeClassRef.Group = "foreign.example" }},
		{name: "foreign kind", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.Spec.NodeClassRef.Kind = "OtherClass" }},
		{name: "missing NodeClassRef", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.Spec.NodeClassRef = nil }},
		{name: "creation protection", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) { c.CreationTimestamp = metav1.Now() }},
		{name: "Ready node overrides absent inventory", getErr: absent, wantLists: 1, mutate: func(_ *coreapis.NodeClaim, n *corev1.Node) {
			n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
		}},
		{name: "deleting claim stays with lifecycle", mutate: func(c *coreapis.NodeClaim, _ *corev1.Node) {
			now := metav1.Now()
			c.DeletionTimestamp = &now
			c.Finalizers = []string{coreapis.TerminationFinalizer}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			gv := schema.GroupVersion{Group: "karpenter.sh", Version: "v1"}
			scheme.AddKnownTypes(gv, &coreapis.NodeClaim{}, &coreapis.NodeClaimList{})
			metav1.AddToGroupVersion(scheme, gv)
			claim := &coreapis.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "claim", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
				Spec:       coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Group: v1alpha1.GroupVersion.Group, Kind: "ECSNodeClass", Name: "default"}},
				Status:     coreapis.NodeClaimStatus{ProviderID: "cn-hangzhou.i-test"},
			}
			claim.StatusConditions().SetTrue(coreapis.ConditionTypeRegistered)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}, Spec: corev1.NodeSpec{ProviderID: claim.Status.ProviderID}}
			if tt.mutate != nil {
				tt.mutate(claim, node)
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim, node).Build()
			provider := &inventoryProvider{listErr: tt.listErr, getErr: tt.getErr}
			if tt.listed {
				provider.listed = []*coreapis.NodeClaim{claim}
			}
			controller := garbagecollection.NewGarbageCollectionController(kubeClient, provider, nil)
			_, err := controller.Reconcile(context.Background())
			if (err != nil) != tt.wantError {
				t.Fatalf("Reconcile error = %v, wantError %v", err, tt.wantError)
			}
			err = kubeClient.Get(context.Background(), client.ObjectKey{Name: claim.Name}, &coreapis.NodeClaim{})
			if apierrors.IsNotFound(err) != tt.wantDelete {
				t.Fatalf("claim lookup = %v, wantDelete %v", err, tt.wantDelete)
			}
			if err := kubeClient.Get(context.Background(), client.ObjectKey{Name: node.Name}, &corev1.Node{}); err != nil {
				t.Fatalf("GC removed node: %v", err)
			}
			if provider.deletes != 0 || provider.gets != tt.wantGets || provider.lists != tt.wantLists {
				t.Fatalf("cloud calls: deletes=%d gets=%d lists=%d", provider.deletes, provider.gets, provider.lists)
			}
		})
	}
}
