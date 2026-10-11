package v1alpha1

import (
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestHashEquivalentConfiguration(t *testing.T) {
	base := &ECSNodeClass{Spec: ECSNodeClassSpec{
		VSwitchSelectorTerms:             []VSwitchSelectorTerm{{ID: tea.String("vsw-1")}},
		SecurityGroupSelectorTerms:       []SecurityGroupSelectorTerm{{ID: tea.String("sg-1")}},
		ImageSelectorTerms:               []ImageSelectorTerm{{ID: tea.String("m-image")}},
		CapacityReservationSelectorTerms: []CapacityReservationSelectorTerm{{ID: tea.String("cr-1")}, {ID: tea.String("cr-2")}},
		Tags:                             map[string]string{"a": "1", "b": "2"},
	}}
	before := base.DeepCopy()
	hash, err := base.Hash()
	require.NoError(t, err)
	require.Equal(t, before, base, "hashing must not mutate the API object")
	equivalent := base.DeepCopy()
	equivalent.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{Tags: map[string]string{"discovery": "cluster"}}, {ID: tea.String("vsw-1")}}
	equivalent.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{{Name: tea.String("workers")}}
	equivalent.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ImageFamily: tea.String("acs:family")}}
	equivalent.Spec.ImageCompatibilityPolicy = "ACK"
	equivalent.Spec.SystemDisk = &SystemDiskSpec{Category: "cloud_essd", Size: tea.Int32(40), PerformanceLevel: tea.String("PL0"), Encrypted: tea.Bool(false)}
	equivalent.Spec.CapacityReservationSelectorTerms = []CapacityReservationSelectorTerm{{ID: tea.String("cr-2")}, {ID: tea.String("cr-1")}, {ID: tea.String("cr-1")}}
	other, err := equivalent.Hash()
	require.NoError(t, err)
	require.Equal(t, hash, other)
}

func TestHashLaunchFields(t *testing.T) {
	base := &ECSNodeClass{}
	hash, err := base.Hash()
	require.NoError(t, err)
	for name, change := range map[string]func(*ECSNodeClassSpec){
		"cluster identity":       func(s *ECSNodeClassSpec) { s.ClusterID = "cluster-a" },
		"cluster name":           func(s *ECSNodeClassSpec) { s.ClusterName = "workers" },
		"endpoint":               func(s *ECSNodeClassSpec) { s.ClusterEndpoint = "https://cluster.example" },
		"userdata":               func(s *ECSNodeClassSpec) { s.UserData = tea.String("echo bootstrap") },
		"role":                   func(s *ECSNodeClassSpec) { s.Role = tea.String("worker-role") },
		"metadata":               func(s *ECSNodeClassSpec) { s.MetadataOptions = &MetadataOptions{HttpTokens: "required"} },
		"kubelet":                func(s *ECSNodeClassSpec) { s.Kubelet = &KubeletConfiguration{MaxPods: tea.Int32(30)} },
		"disk":                   func(s *ECSNodeClassSpec) { s.SystemDisk = &SystemDiskSpec{Size: tea.Int32(80)} },
		"data disk":              func(s *ECSNodeClassSpec) { s.DataDisks = []DataDiskSpec{{Category: "cloud_essd", Size: 40}} },
		"local disk":             func(s *ECSNodeClassSpec) { s.InstanceStorePolicy = tea.String("RAID0") },
		"spot strategy":          func(s *ECSNodeClassSpec) { s.SpotStrategy = tea.String("SpotWithPriceLimit") },
		"spot price":             func(s *ECSNodeClassSpec) { value := 0.5; s.SpotPriceLimit = &value },
		"template":               func(s *ECSNodeClassSpec) { s.LaunchTemplateID = tea.String("lt-1") },
		"template version":       func(s *ECSNodeClassSpec) { s.LaunchTemplateVersion = tea.Int64(2) },
		"reservation preference": func(s *ECSNodeClassSpec) { s.CapacityReservationPreference = tea.String("target") },
		"reservation selector": func(s *ECSNodeClassSpec) {
			s.CapacityReservationSelectorTerms = []CapacityReservationSelectorTerm{{ID: tea.String("cr-1")}}
		},
		"tags":         func(s *ECSNodeClassSpec) { s.Tags = map[string]string{"team": "platform"} },
		"image policy": func(s *ECSNodeClassSpec) { s.ImageCompatibilityPolicy = "Custom" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base.DeepCopy()
			change(&changed.Spec)
			other, err := changed.Hash()
			require.NoError(t, err)
			require.NotEqual(t, hash, other)
		})
	}
}

func TestHashKeepsOrderedDisksAndDNS(t *testing.T) {
	base := &ECSNodeClass{Spec: ECSNodeClassSpec{DataDisks: []DataDiskSpec{{Category: "cloud_essd", Size: 40}, {Category: "cloud_essd", Size: 80}}, Kubelet: &KubeletConfiguration{ClusterDNS: []string{"10.0.0.10", "10.0.0.11"}}}}
	hash, err := base.Hash()
	require.NoError(t, err)
	changed := base.DeepCopy()
	changed.Spec.DataDisks[0], changed.Spec.DataDisks[1] = changed.Spec.DataDisks[1], changed.Spec.DataDisks[0]
	other, err := changed.Hash()
	require.NoError(t, err)
	require.NotEqual(t, hash, other)
	changed = base.DeepCopy()
	changed.Spec.Kubelet.ClusterDNS[0], changed.Spec.Kubelet.ClusterDNS[1] = changed.Spec.Kubelet.ClusterDNS[1], changed.Spec.Kubelet.ClusterDNS[0]
	other, err = changed.Hash()
	require.NoError(t, err)
	require.NotEqual(t, hash, other)
	base.Spec.SystemDisk = &SystemDiskSpec{Size: tea.Int32(-1)}
	_, err = base.Hash()
	require.Error(t, err, "invalid config must not receive a generation-based fallback hash")
}
