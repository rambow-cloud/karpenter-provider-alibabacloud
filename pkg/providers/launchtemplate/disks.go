package launchtemplate

import (
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"strconv"
)

func templateEncrypted(value *string) (*bool, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(*value)
	if err != nil {
		return nil, fmt.Errorf("invalid template disk encrypted flag")
	}
	return &parsed, nil
}
func resolveTemplateDisks(data *ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateData) (v1alpha1.NormalizedDisks, error) {
	spec := v1alpha1.ECSNodeClassSpec{}
	if disk := data.SystemDisk; disk != nil {
		encrypted, err := templateEncrypted(disk.Encrypted)
		if err != nil {
			return v1alpha1.NormalizedDisks{}, err
		}
		spec.SystemDisk = &v1alpha1.SystemDiskSpec{Category: tea.StringValue(disk.Category), Size: disk.Size, Encrypted: encrypted, PerformanceLevel: disk.PerformanceLevel}
	}
	if data.DataDisks != nil {
		for _, disk := range data.DataDisks.DataDisk {
			if disk == nil || disk.Category == nil || disk.Size == nil {
				return v1alpha1.NormalizedDisks{}, fmt.Errorf("incomplete template data disk")
			}
			encrypted, err := templateEncrypted(disk.Encrypted)
			if err != nil {
				return v1alpha1.NormalizedDisks{}, err
			}
			spec.DataDisks = append(spec.DataDisks, v1alpha1.DataDiskSpec{Category: *disk.Category, Size: *disk.Size, Encrypted: encrypted, PerformanceLevel: disk.PerformanceLevel, Device: disk.Device, SnapshotID: disk.SnapshotId, DeleteWithInstance: disk.DeleteWithInstance})
		}
	}
	disks, err := v1alpha1.NormalizeDisks(spec)
	if err != nil {
		return disks, fmt.Errorf("invalid template disks: %w", err)
	}
	return disks, nil
}
