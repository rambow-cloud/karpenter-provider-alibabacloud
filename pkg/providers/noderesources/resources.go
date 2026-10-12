package noderesources

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type Model struct {
	Capacity    corev1.ResourceList
	Allocatable corev1.ResourceList
	Overhead    *cloudprovider.InstanceTypeOverhead
}

func Compute(ctx context.Context, it *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, network *cluster.NetworkConfig, imageID string) (*Model, error) {
	if it == nil || it.CPU == nil || it.Memory == nil || it.CPU.Sign() <= 0 || it.Memory.Sign() <= 0 {
		return nil, fmt.Errorf("instance CPU/memory metadata is incomplete")
	}
	if err := v1alpha1.ValidateNodeResourceConfiguration(nc.Spec); err != nil {
		return nil, err
	}
	pool := nc.Status.ACKNodePool
	if pool == nil || pool.ID == "" || pool.ClusterID == "" || (nc.Spec.ACKNodePoolID != nil && *nc.Spec.ACKNodePoolID != pool.ID) || (nc.Spec.ClusterID != "" && nc.Spec.ClusterID != pool.ClusterID) {
		return nil, fmt.Errorf("native ACK bootstrap pool is unresolved or stale")
	}
	if network == nil {
		return nil, fmt.Errorf("network resource limits are unresolved")
	}
	var pods int64
	switch network.ClusterNetwork {
	case "kube-flannel-ds", "kube-flannel-ds-vxlan":
		pods = network.MaxPods
	case "terway-eniip":
		if it.EniQuantity <= 1 || it.EniPrivateIpAddressQuantity <= 0 {
			return nil, fmt.Errorf("shared Terway ENI metadata is incomplete")
		}
		pods = (int64(it.EniQuantity)-1)*int64(it.EniPrivateIpAddressQuantity) + 3
	case "terway":
		if it.EniQuantity <= 1 {
			return nil, fmt.Errorf("Terway ENI metadata is incomplete")
		}
		pods = int64(it.EniQuantity) - 1 + 3
	default:
		return nil, fmt.Errorf("unsupported network resource limits")
	}
	if pods <= 0 || pods > math.MaxInt32 {
		return nil, fmt.Errorf("invalid network Pod limit")
	}
	reservationPods := pods
	kubelet := pool.Kubelet
	if kubelet != nil && kubelet.MaxPods != nil {
		if *kubelet.MaxPods <= 0 {
			return nil, fmt.Errorf("invalid native kubelet maxPods")
		}
		reservationPods = max(reservationPods, int64(*kubelet.MaxPods))
		pods = min(pods, int64(*kubelet.MaxPods))
	}
	if kubelet != nil && kubelet.PodsPerCore != nil {
		return nil, fmt.Errorf("native podsPerCore cannot be proven by the ACK API")
	}
	opt := options.FromContext(ctx)
	if opt.PodDensityMode != options.PodDensityModeKubelet || network.ExclusiveEniEnabled {
		return nil, fmt.Errorf("custom/exclusive Pod density is not verified for native ACK bootstrap")
	}
	if math.IsNaN(opt.VMMemoryOverheadPercent) || math.IsInf(opt.VMMemoryOverheadPercent, 0) || opt.VMMemoryOverheadPercent < 0 || opt.VMMemoryOverheadPercent >= 1 {
		return nil, fmt.Errorf("invalid VM memory overhead fraction")
	}
	vm := int64(math.Ceil(float64(it.Memory.Value()) * opt.VMMemoryOverheadPercent))
	if opt.VMMemoryOverhead != "" {
		fixed, err := resource.ParseQuantity(opt.VMMemoryOverhead)
		if err != nil || fixed.Sign() < 0 {
			return nil, fmt.Errorf("invalid fixed VM memory overhead")
		}
		vm = max(vm, fixed.Value())
	}
	memory := it.Memory.Value() - vm
	if memory <= 0 {
		return nil, fmt.Errorf("VM overhead exhausts memory")
	}
	capacity := corev1.ResourceList{corev1.ResourceCPU: it.CPU.DeepCopy(), corev1.ResourceMemory: *resource.NewQuantity(memory, resource.BinarySI), corev1.ResourcePods: *resource.NewQuantity(pods, resource.DecimalSI)}
	if it.GPU != nil {
		if it.GPU.Count != nil && it.GPU.Count.Sign() > 0 {
			capacity[v1alpha1.ResourceGPU] = it.GPU.Count.DeepCopy()
		}
		if it.GPU.Memory != nil && it.GPU.Memory.Sign() > 0 {
			capacity[v1alpha1.ResourceGPUMemory] = it.GPU.Memory.DeepCopy()
		}
	}
	// ACK >=1.28 CPU brackets and equal Kubernetes/system reservation split.
	cpu := it.CPU.MilliValue()
	reservedCPU := float64(min(cpu, int64(1000)))*.06 + float64(min(max(cpu-1000, 0), int64(1000)))*.01 + float64(min(max(cpu-2000, 0), int64(2000)))*.005 + float64(max(cpu-4000, 0))*.0025
	reservedMemory := min((11*reservationPods+255)*1024*1024, memory/4)
	kubeReserved := corev1.ResourceList{corev1.ResourceCPU: *resource.NewMilliQuantity(int64(math.Ceil(reservedCPU/2)), resource.DecimalSI), corev1.ResourceMemory: *resource.NewQuantity((reservedMemory+1)/2, resource.BinarySI)}
	systemReserved := kubeReserved.DeepCopy()
	if kubelet != nil {
		if err := applyReserved(kubeReserved, kubelet.KubeReserved); err != nil {
			return nil, err
		}
		if err := applyReserved(systemReserved, kubelet.SystemReserved); err != nil {
			return nil, err
		}
	}
	eviction := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("300Mi")}
	if kubelet != nil && kubelet.EvictionHard != nil {
		if _, ok := kubelet.EvictionHard["memory.available"]; !ok {
			return nil, fmt.Errorf("custom ACK evictionHard must explicitly specify memory.available")
		}
		// A custom map replaces defaults (mergeDefaultEvictionSettings is not
		// exposed by ACK). Do not silently add thresholds the node will not use.
		eviction = corev1.ResourceList{}
		for signal, value := range kubelet.EvictionHard {
			switch signal {
			case "memory.available":
				q, err := threshold(value, memory)
				if err != nil {
					return nil, err
				}
				eviction[corev1.ResourceMemory] = q
			case "nodefs.available", "nodefs.inodesFree", "imagefs.available", "imagefs.inodesFree":
				if _, err := threshold(value, 1<<30); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unsupported native eviction signal %s", signal)
			}
		}
	}
	if fs := nc.Spec.NodeFilesystem; fs != nil {
		hash, err := nc.ResourceConfigurationHash()
		if err != nil {
			return nil, err
		}
		if fs.ConfigurationHash != hash {
			return nil, fmt.Errorf("nodeFilesystem measurement uses a different resource configuration")
		}
		if fs.ImageID != imageID {
			return nil, fmt.Errorf("nodeFilesystem measurement does not match launch image")
		}
		disks, err := v1alpha1.NormalizeDisks(nc.Spec)
		if err != nil {
			return nil, err
		}
		bound := int64(disks.SystemDisk.Size)
		for _, disk := range disks.DataDisks {
			bound = max(bound, int64(disk.Size))
		}
		measured := resource.MustParse(fs.Capacity)
		if measured.Value() > bound*1024*1024*1024 {
			return nil, fmt.Errorf("nodeFilesystem exceeds any launch disk; disks are not summed")
		}
		available := resource.MustParse(fs.Allocatable)
		capacity[corev1.ResourceEphemeralStorage] = measured
		// Profile allocatable includes the actual nodefs reserves/eviction.
		// Move that single total to eviction so core subtracts it exactly once.
		loss := measured.DeepCopy()
		loss.Sub(available)
		knownLoss := kubeReserved[corev1.ResourceEphemeralStorage].DeepCopy()
		knownLoss.Add(systemReserved[corev1.ResourceEphemeralStorage])
		nodefsThreshold := "10%"
		if kubelet != nil && kubelet.EvictionHard != nil {
			nodefsThreshold = kubelet.EvictionHard["nodefs.available"]
		}
		if nodefsThreshold != "" {
			thresholdLoss, err := threshold(nodefsThreshold, measured.Value())
			if err != nil {
				return nil, err
			}
			knownLoss.Add(thresholdLoss)
		}
		if loss.Cmp(knownLoss) < 0 {
			return nil, fmt.Errorf("nodeFilesystem allocatable contradicts known reservations/eviction")
		}
		delete(kubeReserved, corev1.ResourceEphemeralStorage)
		delete(systemReserved, corev1.ResourceEphemeralStorage)
		eviction[corev1.ResourceEphemeralStorage] = loss
	} else {
		delete(kubeReserved, corev1.ResourceEphemeralStorage)
		delete(systemReserved, corev1.ResourceEphemeralStorage)
	}
	overhead := &cloudprovider.InstanceTypeOverhead{KubeReserved: kubeReserved, SystemReserved: systemReserved, EvictionThreshold: eviction}
	allocatable := capacity.DeepCopy()
	for name, q := range overhead.Total() {
		if value, exists := allocatable[name]; exists {
			value.Sub(q)
			if value.Sign() < 0 {
				return nil, fmt.Errorf("resource reservations exhaust %s", name)
			}
			allocatable[name] = value
		}
	}
	return &Model{capacity, allocatable, overhead}, nil
}

func applyReserved(target corev1.ResourceList, overrides map[corev1.ResourceName]string) error {
	for name, value := range overrides {
		if name != corev1.ResourceCPU && name != corev1.ResourceMemory && name != corev1.ResourceEphemeralStorage {
			return fmt.Errorf("unsupported reserved resource %s", name)
		}
		q, err := resource.ParseQuantity(value)
		if err != nil || q.Sign() < 0 {
			return fmt.Errorf("invalid reservation for %s", name)
		}
		target[name] = q
	}
	return nil
}
func threshold(value string, capacity int64) (resource.Quantity, error) {
	if strings.HasSuffix(value, "%") {
		percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
		if err != nil || math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
			return resource.Quantity{}, fmt.Errorf("invalid eviction percentage")
		}
		return *resource.NewQuantity(int64(math.Ceil(float64(capacity)*percent/100)), resource.BinarySI), nil
	}
	q, err := resource.ParseQuantity(value)
	if err != nil || q.Sign() < 0 {
		return resource.Quantity{}, fmt.Errorf("invalid eviction quantity")
	}
	return q, nil
}

var snapshotKeys = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods, corev1.ResourceEphemeralStorage, v1alpha1.ResourceGPU, v1alpha1.ResourceGPUMemory}

// Encode preserves launch-time capacity/allocatable in a first-request ECS tag.
// Missing resources stay absent; tag limits are checked before any cloud mutation.
func (m *Model) Encode() (string, error) {
	fields := []string{"1"}
	for _, list := range []corev1.ResourceList{m.Capacity, m.Allocatable} {
		for _, key := range snapshotKeys {
			q, ok := list[key]
			if !ok {
				fields = append(fields, "-")
				continue
			}
			value := q.Value()
			if key == corev1.ResourceCPU {
				value = q.MilliValue()
			}
			fields = append(fields, strconv.FormatInt(value, 10))
		}
	}
	value := strings.Join(fields, ",")
	if len(value) > 128 {
		return "", fmt.Errorf("resource snapshot exceeds ECS tag limit")
	}
	return value, nil
}
func Decode(value string) (corev1.ResourceList, corev1.ResourceList, error) {
	if len(value) > 128 {
		return nil, nil, fmt.Errorf("resource snapshot exceeds ECS tag limit")
	}
	fields := strings.Split(value, ",")
	if len(fields) != 13 || fields[0] != "1" {
		return nil, nil, fmt.Errorf("invalid resource snapshot version/length")
	}
	lists := []corev1.ResourceList{{}, {}}
	for i, field := range fields[1:] {
		if field == "-" {
			continue
		}
		value, err := strconv.ParseInt(field, 10, 64)
		if err != nil || value < 0 {
			return nil, nil, fmt.Errorf("invalid resource snapshot quantity")
		}
		key := snapshotKeys[i%6]
		q := resource.NewQuantity(value, resource.BinarySI)
		if key == corev1.ResourceCPU {
			q = resource.NewMilliQuantity(value, resource.DecimalSI)
		}
		lists[i/6][key] = *q
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods} {
		q, ok := lists[0][key]
		if !ok || q.Sign() <= 0 {
			return nil, nil, fmt.Errorf("incomplete resource snapshot")
		}
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods} {
		if _, ok := lists[1][key]; !ok {
			return nil, nil, fmt.Errorf("resource snapshot allocatable is incomplete")
		}
	}
	for key, q := range lists[1] {
		cap, ok := lists[0][key]
		if !ok || q.Cmp(cap) > 0 {
			return nil, nil, fmt.Errorf("resource snapshot allocatable exceeds capacity")
		}
	}
	return lists[0], lists[1], nil
}
