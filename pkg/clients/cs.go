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

package clients

import (
	"context"
	"fmt"
	"strings"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

// CSClient is an interface for Container Service client operations
type CSClient interface {
	DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error)
	GetClusterAddonInstance(ctx context.Context, clusterID, addonName string) (*cs.GetClusterAddonInstanceResponse, error)
	DescribeClusterDetail(ctx context.Context, clusterID string) (*cs.DescribeClusterDetailResponse, error)
	DescribeKubernetesVersionMetadata(ctx context.Context, request *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error)
}

// DefaultCSClient implements CSClient using Alibaba Cloud SDK
type DefaultCSClient struct {
	client *cs.Client
}

// NewDefaultCSClient creates a new default CS client adapter
func NewDefaultCSClient(client *cs.Client) CSClient {
	copy := *client
	prepareHTTPClient(&copy.Client)
	client = &copy
	return &DefaultCSClient{
		client: client,
	}
}

// ListClusterAddonInstances retains caller cancellation and the common SDK timeouts.
// Optional interface keeps existing CSClient implementations source-compatible.
func (c *DefaultCSClient) ListClusterAddonInstances(ctx context.Context, clusterID string) (*cs.ListClusterAddonInstancesResponse, error) {
	return callSDK(ctx, func() (*cs.ListClusterAddonInstancesResponse, error) {
		return c.withContext(ctx).ListClusterAddonInstances(tea.String(clusterID))
	})
}

// Native ACK pool discovery uses the same request context as bootstrap.
func (c *DefaultCSClient) DescribeClusterNodePools(ctx context.Context, clusterID string) (*cs.DescribeClusterNodePoolsResponse, error) {
	return callSDK(ctx, func() (*cs.DescribeClusterNodePoolsResponse, error) {
		return c.withContext(ctx).DescribeClusterNodePools(&clusterID, &cs.DescribeClusterNodePoolsRequest{})
	})
}
func (c *DefaultCSClient) DescribeClusterNodePoolDetail(ctx context.Context, clusterID, poolID string) (*cs.DescribeClusterNodePoolDetailResponse, error) {
	return callSDK(ctx, func() (*cs.DescribeClusterNodePoolDetailResponse, error) {
		return c.withContext(ctx).DescribeClusterNodePoolDetail(&clusterID, &poolID)
	})
}

// withContext copies SDK configuration so calls keep their own context.
func (c *DefaultCSClient) withContext(ctx context.Context) *cs.Client {
	copy := *c.client
	copy.HttpClient = contextHTTPClient{ctx: ctx, base: c.client.HttpClient}
	return &copy
}

// DescribeClusterAttachScripts implements CSClient interface
func (c *DefaultCSClient) DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	response, err := callSDK(ctx, func() (*cs.DescribeClusterAttachScriptsResponse, error) {
		return c.withContext(ctx).DescribeClusterAttachScripts(&clusterID, request)
	})
	if err != nil {
		return "", err
	}

	if response == nil || response.Body == nil {
		return "", nil
	}

	return *response.Body, nil
}

func (c *DefaultCSClient) GetClusterAddonInstance(ctx context.Context, clusterID, addonName string) (*cs.GetClusterAddonInstanceResponse, error) {
	return callSDK(ctx, func() (*cs.GetClusterAddonInstanceResponse, error) {
		return c.withContext(ctx).GetClusterAddonInstance(&clusterID, &addonName)
	})
}

func (c *DefaultCSClient) DescribeClusterDetail(ctx context.Context, clusterID string) (*cs.DescribeClusterDetailResponse, error) {
	return callSDK(ctx, func() (*cs.DescribeClusterDetailResponse, error) {
		return c.withContext(ctx).DescribeClusterDetail(&clusterID)
	})
}

func (c *DefaultCSClient) DescribeKubernetesVersionMetadata(ctx context.Context, request *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || tea.StringValue(request.Region) == "" || tea.StringValue(request.ClusterType) == "" || tea.StringValue(request.KubernetesVersion) == "" {
		return nil, fmt.Errorf("ACK version metadata requires Region, ClusterType and KubernetesVersion")
	}
	resp, err := callSDK(ctx, func() (*cs.DescribeKubernetesVersionMetadataResponse, error) {
		return c.withContext(ctx).DescribeKubernetesVersionMetadata(request)
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("ACK version metadata response is nil")
	}
	return resp.Body, nil
}

// VersionMetadataRequest scopes compatibility to the actual cluster, not the latest version.
func VersionMetadataRequest(region string, detail *cs.DescribeClusterDetailResponseBody, runtime string) (*cs.DescribeKubernetesVersionMetadataRequest, error) {
	if detail == nil || tea.StringValue(detail.CurrentVersion) == "" {
		return nil, fmt.Errorf("ACK cluster CurrentVersion is missing")
	}
	if actual := tea.StringValue(detail.RegionId); actual != "" && actual != region {
		return nil, fmt.Errorf("ACK cluster region does not match provider region")
	}
	clusterType := tea.StringValue(detail.ClusterType)
	if region == "" || (clusterType != "ManagedKubernetes" && clusterType != "Kubernetes") {
		return nil, fmt.Errorf("ACK metadata requires a region and a supported ACK ClusterType")
	}
	profile := tea.StringValue(detail.Profile)
	if profile == "" {
		profile = "Default"
	}
	if profile != "Default" && profile != "Edge" && profile != "Serverless" {
		return nil, fmt.Errorf("unsupported ACK profile %q", profile)
	}
	if runtime == "" {
		runtime = "containerd"
	}
	if runtime != "containerd" {
		return nil, fmt.Errorf("only containerd is supported for ACK image compatibility")
	}
	return &cs.DescribeKubernetesVersionMetadataRequest{Region: tea.String(region), ClusterType: tea.String(clusterType), KubernetesVersion: tea.String(strings.TrimSpace(*detail.CurrentVersion)), Profile: tea.String(profile), Runtime: tea.String(runtime)}, nil
}
