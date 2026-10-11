package cloudprovider

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
)

func filterInstanceTypesByTemplate(types []*instancetype.InstanceType, nc *v1alpha1.ECSNodeClass) []*instancetype.InstanceType {
	if nc.Spec.LaunchTemplateID == nil || nc.Status.LaunchTemplate == nil || nc.Status.LaunchTemplate.InstanceType == "" {
		return types
	}
	result := make([]*instancetype.InstanceType, 0, 1)
	for _, it := range types {
		if it.Name == nc.Status.LaunchTemplate.InstanceType {
			result = append(result, it)
		}
	}
	return result
}
