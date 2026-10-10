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

package cloudprovider

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"testing"
)

type scopedInventoryClient struct {
	clients.ECSClient
	t     *testing.T
	calls int
}

func (c *scopedInventoryClient) DescribeInstances(_ context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	c.calls++
	tags := map[string]string{}
	for _, tag := range request.Tag {
		tags[tea.StringValue(tag.Key)] = tea.StringValue(tag.Value)
	}
	if tags[v1alpha1.TagManagedBy] != v1alpha1.TagManagedByValue || tags[v1alpha1.TagClusterID] != "cluster-a" {
		c.t.Fatalf("unscoped discovery request: %v", tags)
	}
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{
		TotalCount: tea.Int32(0), Instances: &ecs.DescribeInstancesResponseBodyInstances{},
	}}, nil
}
func TestListRequiresClusterOwnership(t *testing.T) {
	provider := &CloudProvider{}
	if _, err := provider.List(context.Background()); err == nil {
		t.Fatal("missing cluster ID must fail before querying ECS")
	}
	ctx := (&options.Options{ClusterID: "cluster-a"}).ToContext(context.Background())
	ecsClient := &scopedInventoryClient{t: t}
	provider.instanceProvider = instance.NewProvider(ctx, "cn-hangzhou", ecsClient)
	defer provider.instanceProvider.Batcher.Close()
	claims, err := provider.List(ctx)
	if err != nil || len(claims) != 0 || ecsClient.calls != 1 {
		t.Fatalf("List() = %v, %v; calls=%d", claims, err, ecsClient.calls)
	}
}
func TestInvalidProviderIDCannotDelete(t *testing.T) {
	provider := &CloudProvider{} // No ECS client: an attempted delete would panic.
	for _, id := range []string{"", "i-test", ".i-test", "cn-hangzhou.", "cn-hangzhou.wrong", "cn-hangzhou.i-", "cn-hangzhou.extra.i-test", "cn-hangzhou.i-test/other", "cn-hangzhou.i-test "} {
		t.Run(id, func(t *testing.T) {
			claim := &coreapis.NodeClaim{Status: coreapis.NodeClaimStatus{ProviderID: id}}
			if err := provider.Delete(context.Background(), claim); err == nil {
				t.Fatal("invalid ID was accepted")
			}
			if _, err := provider.Get(context.Background(), id); err == nil {
				t.Fatal("invalid ID was accepted by Get")
			}
		})
	}
	if id := fromProviderID("cn-hangzhou.i-test123"); id != "i-test123" {
		t.Fatalf("valid ID parsed as %q", id)
	}
}

func TestCreateOwnershipMatchesDiscovery(t *testing.T) {
	ctx := (&options.Options{ClusterID: "cluster-a"}).ToContext(context.Background())
	for _, classID := range []string{"", "cluster-a", "cluster-b"} {
		t.Run(classID, func(t *testing.T) {
			class := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{ClusterID: classID,
				Tags: map[string]string{v1alpha1.TagManagedBy: "foreign", v1alpha1.TagClusterID: "foreign"}}}
			tags, err := clusterScopedInstanceTags(ctx, &coreapis.NodeClaim{}, class)
			if classID == "cluster-b" {
				if err == nil {
					t.Fatal("cross-cluster creation was accepted")
				}
				return
			}
			if err != nil || tags[v1alpha1.TagClusterID] != "cluster-a" || tags[v1alpha1.TagManagedBy] != v1alpha1.TagManagedByValue {
				t.Fatalf("ownership tags = %v, error = %v", tags, err)
			}
		})
	}
	if _, err := clusterScopedInstanceTags(context.Background(), &coreapis.NodeClaim{}, &v1alpha1.ECSNodeClass{}); err == nil {
		t.Fatal("creation without configured ownership must fail")
	}
}
