package v1alpha1

import "fmt"

// LaunchSpec uses one resolved template snapshot in hashing, image validation,
// scheduling and creation. Admission still forbids disk overrides in template mode.
func (n *ECSNodeClass) LaunchSpec() (ECSNodeClassSpec, error) {
	spec := *n.Spec.DeepCopy()
	if spec.LaunchTemplateID == nil {
		return spec, nil
	}
	resolved := n.Status.LaunchTemplate
	if resolved == nil || resolved.ID != *spec.LaunchTemplateID || resolved.Version <= 0 {
		return spec, fmt.Errorf("launch template concrete version and disk layout are unresolved")
	}
	if spec.LaunchTemplateVersion != nil && *spec.LaunchTemplateVersion != resolved.Version {
		return spec, fmt.Errorf("resolved launch template version differs from requested version")
	}
	version := resolved.Version
	spec.LaunchTemplateVersion = &version
	d := resolved.Disks.DeepCopy()
	spec.SystemDisk = &SystemDiskSpec{Category: d.SystemDisk.Category, Size: &d.SystemDisk.Size, Encrypted: d.SystemDisk.Encrypted}
	if d.SystemDisk.PerformanceLevel != "" {
		spec.SystemDisk.PerformanceLevel = &d.SystemDisk.PerformanceLevel
	}
	if d.SystemDisk.KMSKeyID != "" {
		spec.SystemDisk.KMSKeyID = &d.SystemDisk.KMSKeyID
	}
	spec.DataDisks = nil
	for _, disk := range d.DataDisks {
		item := DataDiskSpec{Category: disk.Category, Size: disk.Size, Encrypted: disk.Encrypted, DeleteWithInstance: disk.DeleteWithInstance}
		if disk.Device != "" {
			item.Device = &disk.Device
		}
		if disk.PerformanceLevel != "" {
			item.PerformanceLevel = &disk.PerformanceLevel
		}
		if disk.KMSKeyID != "" {
			item.KMSKeyID = &disk.KMSKeyID
		}
		if disk.SnapshotID != "" {
			item.SnapshotID = &disk.SnapshotID
		}
		spec.DataDisks = append(spec.DataDisks, item)
	}
	spec.InstanceStorePolicy = d.InstanceStorePolicy
	if _, err := NormalizeDisks(spec); err != nil {
		return spec, fmt.Errorf("invalid template disk layout: %w", err)
	}
	return spec, nil
}
