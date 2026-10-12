package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ECSNodeClassHashVersion must change when the static drift contract changes.
// Current deployment scope is a fresh installation; legacy hashes are not migrated.
const ECSNodeClassHashVersion = "v4"

// Hash is shared by the NodeClass controller and CloudProvider. Resource selectors
// are dynamic: drift checks membership of the resolved images, vSwitches and
// security groups instead of replacing nodes when selector syntax changes.
func (n *ECSNodeClass) Hash() (string, error) {
	spec := *n.Spec.DeepCopy()
	if n.Spec.LaunchTemplateID != nil && n.Status.LaunchTemplate != nil {
		var err error
		spec, err = n.LaunchSpec()
		if err != nil {
			return "", err
		}
	}
	spec.VSwitchSelectorTerms = nil
	spec.SecurityGroupSelectorTerms = nil
	spec.ImageSelectorTerms = nil
	if spec.ImageCompatibilityPolicy == "ACK" {
		spec.ImageCompatibilityPolicy = ""
	}
	// Use the same disk defaults as launch. Keep disk and clusterDNS order:
	// disk indices and DNS resolver precedence are meaningful, unlike OR terms.
	disks, err := NormalizeDisks(spec)
	if err != nil {
		return "", fmt.Errorf("normalize drift disks: %w", err)
	}
	spec.SystemDisk, spec.DataDisks, spec.InstanceStorePolicy = nil, nil, nil
	// Capacity reservation terms are ORed, but do not yet have a runtime
	// membership drift check. Preserve their semantics as a sorted unique set.
	terms := map[string]json.RawMessage{}
	for _, term := range spec.CapacityReservationSelectorTerms {
		data, err := json.Marshal(term)
		if err != nil {
			return "", err
		}
		terms[string(data)] = data
	}
	keys := make([]string, 0, len(terms))
	for key := range terms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var reservations []json.RawMessage
	for _, key := range keys {
		reservations = append(reservations, terms[key])
	}
	spec.CapacityReservationSelectorTerms = nil
	data, err := json.Marshal(struct {
		Spec         ECSNodeClassSpec     `json:"spec"`
		Disks        NormalizedDisks      `json:"disks"`
		Reservations []json.RawMessage    `json:"reservations,omitempty"`
		ACKNodePool  *ResolvedACKNodePool `json:"ackNodePool,omitempty"`
	}{spec, disks, reservations, n.Status.ACKNodePool})
	if err != nil {
		return "", fmt.Errorf("marshal drift configuration: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
