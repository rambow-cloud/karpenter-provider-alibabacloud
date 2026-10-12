package nodepool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

type api interface {
	DescribeClusterNodePools(context.Context, string) (*cs.DescribeClusterNodePoolsResponse, error)
	DescribeClusterNodePoolDetail(context.Context, string, string) (*cs.DescribeClusterNodePoolDetailResponse, error)
}
type Provider struct {
	clusterID string
	client    api
}

func New(clusterID string, client interface{}) *Provider {
	p := &Provider{clusterID: clusterID}
	p.client, _ = client.(api)
	return p
}

// Resolve selects a pool explicitly, including when ACK would otherwise choose
// a mutable default. It reads only resource settings; native scripts are excluded.
func (p *Provider) Resolve(ctx context.Context, nc *v1alpha1.ECSNodeClass) (*v1alpha1.ResolvedACKNodePool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.client == nil || p.clusterID == "" {
		return nil, fmt.Errorf("native ACK node-pool metadata client is unavailable")
	}
	if nc.Spec.ClusterID != "" && nc.Spec.ClusterID != p.clusterID {
		return nil, fmt.Errorf("nodeclass cluster does not match controller cluster")
	}
	if err := v1alpha1.ValidateNodeResourceConfiguration(nc.Spec); err != nil {
		return nil, err
	}
	id := ""
	if nc.Spec.ACKNodePoolID != nil {
		id = *nc.Spec.ACKNodePoolID
	} else {
		list, err := p.client.DescribeClusterNodePools(ctx, p.clusterID)
		if err != nil {
			return nil, fmt.Errorf("list native ACK node pools: %w", err)
		}
		if list == nil || list.Body == nil || list.Body.Nodepools == nil {
			return nil, fmt.Errorf("native ACK node-pool list is incomplete")
		}
		for _, pool := range list.Body.Nodepools {
			if pool == nil || pool.NodepoolInfo == nil || pool.NodepoolInfo.NodepoolId == nil || pool.NodepoolInfo.IsDefault == nil {
				return nil, fmt.Errorf("native ACK node-pool identity/default metadata is incomplete")
			}
			if *pool.NodepoolInfo.IsDefault {
				if id != "" {
					return nil, fmt.Errorf("multiple default ACK node pools")
				}
				id = *pool.NodepoolInfo.NodepoolId
			}
		}
		if id == "" {
			return nil, fmt.Errorf("no default ACK node pool")
		}
	}
	if id == "" {
		return nil, fmt.Errorf("native ACK node pool ID is missing")
	}
	detail, err := p.client.DescribeClusterNodePoolDetail(ctx, p.clusterID, id)
	if err != nil {
		return nil, fmt.Errorf("read native ACK node pool: %w", err)
	}
	if detail == nil || detail.Body == nil || detail.Body.NodepoolInfo == nil || tea.StringValue(detail.Body.NodepoolInfo.NodepoolId) != id || tea.StringValue(detail.Body.NodepoolInfo.Type) != "ess" || detail.Body.Status == nil || tea.StringValue(detail.Body.Status.State) != "active" {
		return nil, fmt.Errorf("native ACK node pool is missing, mismatched or not an active regular pool")
	}
	body := detail.Body
	if body.AutoMode != nil && tea.BoolValue(body.AutoMode.Enable) {
		return nil, fmt.Errorf("ACK auto-mode pools do not have a verified bootstrap resource model")
	}
	// Managed pool scripts can change filesystems/reservations without a model.
	if k := body.KubernetesConfig; k != nil {
		if tea.StringValue(k.PreUserData) != "" || tea.StringValue(k.UserData) != "" {
			return nil, fmt.Errorf("native ACK pool userdata prevents a verified resource model")
		}
		if k.Runtime != nil && *k.Runtime != "containerd" {
			return nil, fmt.Errorf("native ACK pool runtime must be containerd")
		}
		if k.CpuPolicy != nil && *k.CpuPolicy != "none" {
			return nil, fmt.Errorf("native ACK pool CPU policy is unsupported")
		}
	}
	var kubelet *v1alpha1.KubeletConfiguration
	if body.NodeConfig != nil && body.NodeConfig.KubeletConfiguration != nil {
		k := body.NodeConfig.KubeletConfiguration
		if (k.CpuManagerPolicy != nil && *k.CpuManagerPolicy != "none") || (k.MemoryManagerPolicy != nil && *k.MemoryManagerPolicy != "None" && *k.MemoryManagerPolicy != "none") || len(k.ReservedMemory) > 0 {
			return nil, fmt.Errorf("native ACK pool CPU/memory manager overrides are unsupported")
		}
		if len(k.EvictionSoft) > 0 || len(k.EvictionSoftGracePeriod) > 0 {
			return nil, fmt.Errorf("native ACK pool soft eviction overrides are unsupported")
		}
		if k.MaxPods != nil && (*k.MaxPods <= 0 || *k.MaxPods > int64(^uint32(0)>>1)) {
			return nil, fmt.Errorf("native ACK pool maxPods is invalid")
		}
		data, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		kubelet = &v1alpha1.KubeletConfiguration{}
		// Validate only the resource-affecting maps; no numeric/string coercion.
		if err := json.Unmarshal(data, kubelet); err != nil {
			return nil, fmt.Errorf("invalid native ACK kubelet resource configuration: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &v1alpha1.ResolvedACKNodePool{ID: id, ClusterID: p.clusterID, Kubelet: kubelet}, nil
}
