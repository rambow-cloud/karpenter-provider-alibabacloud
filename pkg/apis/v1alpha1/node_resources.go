package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	"regexp"
)

// ResolvedACKNodePool contains no native userdata or credentials.
// +kubebuilder:object:generate=true
type ResolvedACKNodePool struct {
	ID        string                `json:"id"`
	ClusterID string                `json:"clusterID"`
	Kubelet   *KubeletConfiguration `json:"kubelet,omitempty"`
}

// NodeFilesystem records measurements from a node using the same image/disks.
// Allocatable already includes filesystem/kubelet reservation and eviction losses.
// +kubebuilder:object:generate=true
type NodeFilesystem struct {
	// +kubebuilder:validation:Pattern="^[a-f0-9]{64}$"
	ConfigurationHash string `json:"configurationHash"`
	// +kubebuilder:validation:MinLength=1
	ImageID     string `json:"imageID"`
	Capacity    string `json:"capacity"`
	Allocatable string `json:"allocatable"`
}

var ackPoolIDPattern = regexp.MustCompile(`^np[-a-zA-Z0-9]+$`)

func ValidateNodeResourceConfiguration(spec ECSNodeClassSpec) error {
	if spec.ACKNodePoolID != nil && !ackPoolIDPattern.MatchString(*spec.ACKNodePoolID) {
		return fmt.Errorf("ackNodePoolID must identify an ACK node pool")
	}
	if spec.Kubelet != nil {
		return fmt.Errorf("spec.kubelet overrides cannot be applied by ACK attach scripts; configure kubelet on the selected native ACK node pool")
	}
	if fs := spec.NodeFilesystem; fs != nil {
		if _, err := hex.DecodeString(fs.ConfigurationHash); len(fs.ConfigurationHash) != 64 || err != nil {
			return fmt.Errorf("nodeFilesystem requires the measured resource configuration hash")
		}
		if fs.ImageID == "" {
			return fmt.Errorf("nodeFilesystem requires the measured image ID")
		}
		capacity, err := resource.ParseQuantity(fs.Capacity)
		if err != nil || capacity.Sign() <= 0 {
			return fmt.Errorf("nodeFilesystem.capacity must be a positive quantity")
		}
		allocatable, err := resource.ParseQuantity(fs.Allocatable)
		if err != nil || allocatable.Sign() < 0 || allocatable.Cmp(capacity) > 0 {
			return fmt.Errorf("nodeFilesystem.allocatable must be nonnegative and no greater than capacity")
		}
	}
	return nil
}

// ResourceConfigurationHash binds nodefs measurements to effective launch disks,
// native kubelet configuration and custom bootstrap data, without exposing scripts.
func (nc *ECSNodeClass) ResourceConfigurationHash() (string, error) {
	spec, err := nc.LaunchSpec()
	if err != nil {
		return "", err
	}
	disks, err := NormalizeDisks(spec)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Disks               NormalizedDisks
		Pool                *ResolvedACKNodePool
		UserData            *string
		InstanceStorePolicy *string
	}{disks, nc.Status.ACKNodePool, spec.UserData, spec.InstanceStorePolicy})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
