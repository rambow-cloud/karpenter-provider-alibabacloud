package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

type NetworkConfig struct {
	ClusterNetwork      string
	NodeCidrMask        int
	MaxPods             int64
	TrunkEniEnabled     bool
	ExclusiveEniEnabled bool
	DualStack           bool
}

type ClusterEniConfig struct {
	ENITrunking bool `json:"ENITrunking"`
}

// CalculateMaxPods uses the returned IPv4 node CIDR, bounded by ACK's Flannel quota.
// Invalid/unknown masks never authorize density. Discovery reports their cause.
func (nc *NetworkConfig) CalculateMaxPods() int64 {
	if nc.NodeCidrMask <= 0 || nc.NodeCidrMask >= 32 {
		return 0
	}
	return min(int64(1)<<(32-nc.NodeCidrMask), 256)
}

type addonLister interface {
	ListClusterAddonInstances(context.Context, string) (*cs.ListClusterAddonInstancesResponse, error)
}

func getClusterNetworkAddon(ctx context.Context, csClient clients.CSClient, clusterID string) (string, *string, error) {
	lister, ok := csClient.(addonLister)
	if !ok {
		return "", nil, fmt.Errorf("CS client cannot list installed network addons")
	}
	response, err := lister.ListClusterAddonInstances(ctx, clusterID)
	if err != nil {
		return "", nil, fmt.Errorf("list installed network addons: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.Addons == nil {
		return "", nil, fmt.Errorf("installed network addon metadata is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	selected := ""
	for _, addon := range response.Body.Addons {
		if addon == nil || tea.StringValue(addon.Name) == "" {
			return "", nil, fmt.Errorf("installed addon identity is incomplete")
		}
		name := *addon.Name
		switch name {
		case "terway", "terway-eniip", "kube-flannel-ds", "kube-flannel-ds-vxlan":
		default:
			continue
		}
		if tea.StringValue(addon.State) != "active" {
			return "", nil, fmt.Errorf("network addon %s is not active", name)
		}
		if selected != "" {
			return "", nil, fmt.Errorf("conflicting network addons %s and %s", selected, name)
		}
		selected = name
	}
	if selected == "" {
		return "", nil, fmt.Errorf("no known active network addon in cluster %s", clusterID)
	}
	config, err := csClient.GetClusterAddonInstance(ctx, clusterID, selected)
	if err != nil {
		return "", nil, fmt.Errorf("read network addon configuration: %w", err)
	}
	if config == nil || config.Body == nil || tea.StringValue(config.Body.Name) != selected {
		return "", nil, fmt.Errorf("network addon configuration identity is incomplete or mismatched")
	}
	if config.Body.State != nil && *config.Body.State != "active" {
		return "", nil, fmt.Errorf("network addon %s changed activation state during discovery", selected)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return selected, config.Body.Config, nil
}

// InitializeClusterNetworkConfig requires authoritative ACK metadata; it never
// substitutes a CIDR or ignores query/configuration errors after startup failure.
func InitializeClusterNetworkConfig(ctx context.Context, csClient clients.CSClient, clusterID string) (*NetworkConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if clusterID == "" {
		return nil, fmt.Errorf("clusterID is empty")
	}
	name, config, err := getClusterNetworkAddon(ctx, csClient, clusterID)
	if err != nil {
		return nil, err
	}
	detail, err := csClient.DescribeClusterDetail(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("read cluster network detail: %w", err)
	}
	if detail == nil || detail.Body == nil {
		return nil, fmt.Errorf("cluster network detail is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nc := &NetworkConfig{ClusterNetwork: name}
	switch strings.ToLower(strings.TrimSpace(tea.StringValue(detail.Body.IpStack))) {
	case "ipv4":
	case "dual", "ipv6":
		nc.DualStack = true
	default:
		return nil, fmt.Errorf("cluster IP stack is missing or unsupported")
	}
	switch name {
	case "terway-eniip", "terway":
		if err := parseTerwayEniipConfig(config, nc); err != nil {
			return nil, err
		}
	case "kube-flannel-ds", "kube-flannel-ds-vxlan":
		mask, err := strconv.Atoi(tea.StringValue(detail.Body.NodeCidrMask))
		if err != nil || mask <= 0 || mask >= 32 {
			return nil, fmt.Errorf("Flannel requires a valid IPv4 node CIDR mask")
		}
		nc.NodeCidrMask = mask
		nc.MaxPods = nc.CalculateMaxPods()
	}
	return nc, nil
}

func parseTerwayEniipConfig(config *string, nc *NetworkConfig) error {
	if config == nil || *config == "" {
		return fmt.Errorf("Terway configuration is missing")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*config), &fields); err != nil || fields == nil {
		return fmt.Errorf("Terway configuration must be a JSON object")
	}
	// An omitted override means the addon's default false. Explicit null,
	// strings or malformed values do not prove that standard mode is in use.
	if raw, exists := fields["ENITrunking"]; exists {
		var enabled *bool
		if err := json.Unmarshal(raw, &enabled); err != nil || enabled == nil {
			return fmt.Errorf("Terway ENITrunking must be a boolean")
		}
		nc.TrunkEniEnabled = *enabled
	}
	return nil
}
