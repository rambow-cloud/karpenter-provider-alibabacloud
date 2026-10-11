package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type workerImageECS struct {
	clients.ECSClient
	t                              *testing.T
	id, arch                       string
	images                         []ecs.DescribeImagesResponseBodyImagesImage
	imageErr, instanceErr, typeErr error
	requests                       []*ecs.DescribeInstancesRequest
	imageCalls                     int
	secondPage                     bool
}

func (c *workerImageECS) DescribeInstances(_ context.Context, req *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	c.requests = append(c.requests, req)
	require.Equal(c.t, "cn-hangzhou", tea.StringValue(req.RegionId))
	require.Len(c.t, req.Tag, 1)
	require.Equal(c.t, "ack.aliyun.com", tea.StringValue(req.Tag[0].Key))
	require.Equal(c.t, "cluster-a", tea.StringValue(req.Tag[0].Value))
	if c.instanceErr != nil {
		return nil, c.instanceErr
	}
	workers := []*ecs.DescribeInstancesResponseBodyInstancesInstance{{InstanceId: tea.String("i-worker"), InstanceType: tea.String("ecs.worker.large"), ImageId: tea.String(c.id)}}
	total := int32(1)
	if c.secondPage {
		total = 101
		if tea.Int32Value(req.PageNumber) == 1 {
			workers = []*ecs.DescribeInstancesResponseBodyInstancesInstance{nil, {InstanceId: tea.String("i-without-image")}}
		}
	}
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{TotalCount: &total, Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: workers}}}, nil
}

func (c *workerImageECS) DescribeInstanceTypes(_ context.Context, ids []string) (*ecs.DescribeInstanceTypesResponse, error) {
	require.Equal(c.t, []string{"ecs.worker.large"}, ids)
	if c.typeErr != nil {
		return nil, c.typeErr
	}
	return &ecs.DescribeInstanceTypesResponse{Body: &ecs.DescribeInstanceTypesResponseBody{InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{InstanceType: []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{{InstanceTypeId: tea.String(ids[0]), CpuArchitecture: tea.String(c.arch)}}}}}, nil
}

func (c *workerImageECS) DescribeImages(_ context.Context, ids []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	c.imageCalls++
	require.Equal(c.t, []string{c.id}, ids)
	require.Empty(c.t, filters, "must not query ImageName or default family")
	return c.images, c.imageErr
}

func TestWorkerImageDiscoveryUsesExactPublicCustomAndSharedIDs(t *testing.T) {
	for _, tc := range []struct {
		id, owner, arch string
		pages           bool
	}{
		{"aliyun_3_x64_20G_alibase_20260828.vhd", "system", "x86_64", false},
		{"lifsea_3_x64_5G_alibase_20260519.qcow2", "system", "amd64", false},
		{"m-custom", "self", "arm64", false},
		{"m-shared", "others", "aarch64", true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			client := &workerImageECS{t: t, id: tc.id, arch: tc.arch, secondPage: tc.pages, images: []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String(tc.id), ImageOwnerAlias: tea.String(tc.owner), Status: tea.String("Available"), OSType: tea.String("linux"), Architecture: tea.String(tc.arch)}}}
			id, err := discoverWorkerImageID(context.Background(), "cn-hangzhou", "cluster-a", client)
			require.NoError(t, err)
			require.Equal(t, tc.id, id)
			require.Equal(t, 1, client.imageCalls)
			if tc.pages {
				require.Len(t, client.requests, 2)
			}
		})
	}
}

func TestWorkerImageDiscoveryFailsWithoutSubstitution(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*workerImageECS)
	}{
		{"missing image", "unavailable", func(c *workerImageECS) { c.images = nil }},
		{"different image", "unavailable", func(c *workerImageECS) { c.images[0].ImageId = tea.String("m-other") }},
		{"unavailable status", "not available", func(c *workerImageECS) { c.images[0].Status = tea.String("Deprecated") }},
		{"missing status", "not available", func(c *workerImageECS) { c.images[0].Status = nil }},
		{"wrong architecture", "does not match", func(c *workerImageECS) { c.images[0].Architecture = tea.String("arm64") }},
		{"unknown image architecture", "does not match", func(c *workerImageECS) { c.images[0].Architecture = nil }},
		{"unknown worker architecture", "unknown architecture", func(c *workerImageECS) { c.arch = "unknown" }},
		{"windows", "not Linux", func(c *workerImageECS) { c.images[0].OSType = tea.String("windows") }},
		{"unsubscribed marketplace", "not available", func(c *workerImageECS) {
			c.images[0].ImageOwnerAlias = tea.String("marketplace")
			c.images[0].IsSubscribed = tea.Bool(false)
		}},
		{"image query failure", "query denied", func(c *workerImageECS) { c.imageErr = fmt.Errorf("query denied") }},
		{"worker query failure", "worker query denied", func(c *workerImageECS) { c.instanceErr = fmt.Errorf("worker query denied") }},
		{"type query failure", "type query denied", func(c *workerImageECS) { c.typeErr = fmt.Errorf("type query denied") }},
		{"no worker image", "no discoverable worker image", func(c *workerImageECS) { c.id = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &workerImageECS{t: t, id: "m-worker", arch: "x86_64", images: []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String("m-worker"), Status: tea.String("Available"), OSType: tea.String("linux"), Architecture: tea.String("x86_64")}}}
			tc.change(client)
			id, err := discoverWorkerImageID(context.Background(), "cn-hangzhou", "cluster-a", client)
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, id)
			require.LessOrEqual(t, client.imageCalls, 1, "discovery must not retry with a different image")
		})
	}
}
