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
	return &DefaultCSClient{
		client: client,
	}
}

// DescribeClusterAttachScripts implements CSClient interface
func (c *DefaultCSClient) DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	response, err := c.client.DescribeClusterAttachScripts(&clusterID, request)
	if err != nil {
		return "", err
	}

	if response.Body == nil {
		return "", nil
	}

	return *response.Body, nil
}

func (c *DefaultCSClient) GetClusterAddonInstance(ctx context.Context, clusterID, addonName string) (*cs.GetClusterAddonInstanceResponse, error) {
	return c.client.GetClusterAddonInstance(&clusterID, &addonName)
}

func (c *DefaultCSClient) DescribeClusterDetail(ctx context.Context, clusterID string) (*cs.DescribeClusterDetailResponse, error) {
	return c.client.DescribeClusterDetail(&clusterID)
}

func (c *DefaultCSClient) DescribeKubernetesVersionMetadata(ctx context.Context, request *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || tea.StringValue(request.Region) == "" || tea.StringValue(request.ClusterType) == "" || tea.StringValue(request.KubernetesVersion) == "" {
		return nil, fmt.Errorf("ACK version metadata requires Region, ClusterType and KubernetesVersion")
	}
	resp, err := c.client.DescribeKubernetesVersionMetadata(request)
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
