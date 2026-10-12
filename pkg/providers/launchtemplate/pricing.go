package launchtemplate

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

func resolveTemplatePricing(data *ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateData) v1alpha1.LaunchTemplatePricing {
	result := v1alpha1.LaunchTemplatePricing{
		InternetChargeType:      tea.StringValue(data.InternetChargeType),
		InternetMaxBandwidthOut: tea.Int32Value(data.InternetMaxBandwidthOut),
		IoOptimized:             tea.StringValue(data.IoOptimized),
		SpotDuration:            data.SpotDuration,
	}
	// DescribePrice cannot represent system-disk provisioned IOPS or bursting.
	// Data-disk performance inherited from a native template is also not part
	// of our normalized launch disk model. Never price it as a basic disk.
	if d := data.SystemDisk; d != nil && (tea.Int64Value(d.ProvisionedIops) != 0 || tea.BoolValue(d.BurstingEnabled)) {
		result.UnsupportedReason = "template system disk uses unmodeled provisioned IOPS or bursting"
	}
	if data.DataDisks != nil {
		for _, d := range data.DataDisks.DataDisk {
			if d != nil && (tea.Int64Value(d.ProvisionedIops) != 0 || tea.BoolValue(d.BurstingEnabled)) {
				result.UnsupportedReason = "template data disk uses unmodeled provisioned IOPS or bursting"
			}
		}
	}
	return result
}
