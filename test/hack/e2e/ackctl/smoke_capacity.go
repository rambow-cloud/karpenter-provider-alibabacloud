package main

import (
	"fmt"
	"sort"
	"strconv"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

type smokeCapacityAPI interface {
	DescribeClusterNodePoolDetail(*string, *string) (*cs.DescribeClusterNodePoolDetailResponse, error)
	DescribeClusterNodes(*string, *cs.DescribeClusterNodesRequest) (*cs.DescribeClusterNodesResponse, error)
}

// Read native membership even when it has no Karpenter ownership tags. These
// records are evidence only and never authorize deleting replacement instances.
func smokeNativeCapacity(api smokeCapacityAPI, cluster, pool string) (map[string]interface{}, error) {
	detail, err := api.DescribeClusterNodePoolDetail(tea.String(cluster), tea.String(pool))
	if err != nil {
		return nil, cloudError("native capacity detail", err)
	}
	if detail == nil || detail.Body == nil || detail.Body.NodepoolInfo == nil || tea.StringValue(detail.Body.NodepoolInfo.NodepoolId) != pool || detail.Body.ScalingGroup == nil || detail.Body.ScalingGroup.DesiredSize == nil || detail.Body.Status == nil || detail.Body.Status.TotalNodes == nil {
		return nil, fmt.Errorf("incomplete native capacity baseline")
	}
	body := detail.Body
	ids, seen := []string{}, map[string]bool{}
	for page := 1; ; page++ {
		r, err := api.DescribeClusterNodes(tea.String(cluster), &cs.DescribeClusterNodesRequest{NodepoolId: tea.String(pool), State: tea.String("all"), PageSize: tea.String("100"), PageNumber: tea.String(strconv.Itoa(page))})
		if err != nil {
			return nil, cloudError("native capacity membership", err)
		}
		if r == nil || r.Body == nil || r.Body.Page == nil || r.Body.Page.TotalCount == nil || *r.Body.Page.TotalCount < 0 || (len(r.Body.Nodes) == 0 && len(ids) < int(*r.Body.Page.TotalCount)) {
			return nil, fmt.Errorf("incomplete native capacity pagination")
		}
		for _, n := range r.Body.Nodes {
			if n == nil || tea.StringValue(n.InstanceId) == "" || tea.StringValue(n.NodepoolId) != pool || seen[tea.StringValue(n.InstanceId)] {
				return nil, fmt.Errorf("foreign/duplicate native capacity identity")
			}
			seen[*n.InstanceId] = true
			ids = append(ids, *n.InstanceId)
		}
		if len(ids) >= int(*r.Body.Page.TotalCount) {
			if len(ids) != int(*r.Body.Page.TotalCount) {
				return nil, fmt.Errorf("native capacity count changed during pagination")
			}
			break
		}
	}
	sort.Strings(ids)
	return map[string]interface{}{"nodepool_id": pool, "desired_size": *body.ScalingGroup.DesiredSize, "total_nodes": *body.Status.TotalNodes, "member_ids": ids, "scaling_group_id": tea.StringValue(body.ScalingGroup.ScalingGroupId)}, nil
}
