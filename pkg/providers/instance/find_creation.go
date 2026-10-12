package instance

import (
	"context"
	"fmt"
	"sort"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

// FindCreation validates raw ownership before detail reads; incomplete data and
// query failures must not be mistaken for absence during an ambiguous launch.
func (p *Provider) FindCreation(ctx context.Context, ownership map[string]string) ([]string, error) {
	for _, key := range []string{v1alpha1.TagManagedBy, v1alpha1.TagClusterID, v1alpha1.TagNodeClaim, v1alpha1.TagNodeClaimUID} {
		if ownership[key] == "" {
			return nil, fmt.Errorf("missing recovery ownership %s", key)
		}
	}
	request := &ecs.DescribeInstancesRequest{RegionId: tea.String(p.region), PageSize: tea.Int32(100)}
	keys := make([]string, 0, len(ownership))
	for key := range ownership {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		request.Tag = append(request.Tag, &ecs.DescribeInstancesRequestTag{Key: tea.String(key), Value: tea.String(ownership[key])})
	}
	ids := map[string]bool{}
	for page := int32(1); ; page++ {
		request.PageNumber = tea.Int32(page)
		response, err := p.ecsClient.DescribeInstances(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("find launch by ownership: %w", err)
		}
		if response == nil || response.Body == nil || response.Body.Instances == nil || response.Body.TotalCount == nil {
			return nil, fmt.Errorf("invalid launch recovery response")
		}
		if *response.Body.TotalCount < 0 || (len(response.Body.Instances.Instance) == 0 && *response.Body.TotalCount > 0) {
			return nil, fmt.Errorf("incomplete launch recovery pagination")
		}
		for _, inst := range response.Body.Instances.Instance {
			if inst == nil || tea.StringValue(inst.InstanceId) == "" {
				return nil, fmt.Errorf("incomplete launch recovery instance")
			}
			tags := convertTags(inst.Tags)
			for key, value := range ownership {
				if tags[key] != value {
					return nil, fmt.Errorf("launch recovery ownership mismatch")
				}
			}
			ids[tea.StringValue(inst.InstanceId)] = true
		}
		if page*100 >= *response.Body.TotalCount {
			break
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
