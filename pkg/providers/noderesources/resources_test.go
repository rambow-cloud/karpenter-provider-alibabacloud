package noderesources

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"strings"
	"testing"
)

func fixture() (*instancetype.InstanceType, *v1alpha1.ECSNodeClass, *cluster.NetworkConfig) {
	return &instancetype.InstanceType{CPU: resource.NewQuantity(4, resource.DecimalSI), Memory: resource.NewQuantity(8<<30, resource.BinarySI)}, &v1alpha1.ECSNodeClass{Status: v1alpha1.ECSNodeClassStatus{ACKNodePool: &v1alpha1.ResolvedACKNodePool{ID: "np-test", ClusterID: "c-test"}}}, &cluster.NetworkConfig{ClusterNetwork: "kube-flannel-ds", MaxPods: 64}
}
func zeroVM() context.Context {
	o := options.New()
	o.VMMemoryOverheadPercent = 0
	o.VMMemoryOverhead = "0"
	return o.ToContext(context.Background())
}

func TestACKReservationPolicyAndCoreAllocatable(t *testing.T) {
	for _, tc := range []struct{ cpu, total int64 }{{4, 80}, {32, 150}} {
		t.Run(resource.NewQuantity(tc.cpu, resource.DecimalSI).String(), func(t *testing.T) {
			it, nc, network := fixture()
			it.CPU = resource.NewQuantity(tc.cpu, resource.DecimalSI)
			model, err := Compute(zeroVM(), it, nc, network, "m-test")
			require.NoError(t, err)
			total := model.Overhead.Total()
			require.Equal(t, tc.total, total.Cpu().MilliValue())
			require.EqualValues(t, 959<<20, model.Overhead.KubeReserved.Memory().Value()+model.Overhead.SystemReserved.Memory().Value())
			core := &coreprovider.InstanceType{Capacity: model.Capacity, Overhead: model.Overhead}
			require.Equal(t, model.Allocatable, core.Allocatable())
			require.NotContains(t, model.Capacity, corev1.ResourceEphemeralStorage, "unmeasured nodefs must remain unknown")
		})
	}
	it, nc, n := fixture()
	o := options.New()
	o.VMMemoryOverheadPercent = .125
	o.VMMemoryOverhead = "100Mi"
	m, err := Compute(o.ToContext(context.Background()), it, nc, n, "m-test")
	require.NoError(t, err)
	require.EqualValues(t, 7<<30, m.Capacity.Memory().Value())
	require.EqualValues(t, (7<<30)-(959<<20)-(300<<20), m.Allocatable.Memory().Value(), "VM loss deducted once")
	it.Memory = resource.NewQuantity(1<<30, resource.BinarySI)
	m, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	require.EqualValues(t, 256<<20, m.Overhead.KubeReserved.Memory().Value()+m.Overhead.SystemReserved.Memory().Value())
}

func TestNativeReservationsAndPodLimits(t *testing.T) {
	it, nc, n := fixture()
	nc.Status.ACKNodePool.Kubelet = &v1alpha1.KubeletConfiguration{MaxPods: tea.Int32(20), KubeReserved: map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "200Mi"}, SystemReserved: map[corev1.ResourceName]string{corev1.ResourceCPU: "300m", corev1.ResourceMemory: "300Mi"}, EvictionHard: map[string]string{"memory.available": "10%"}}
	m, err := Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	require.EqualValues(t, 20, m.Capacity.Pods().Value())
	require.EqualValues(t, 3500, m.Allocatable.Cpu().MilliValue())
	require.EqualValues(t, (8<<30)-(500<<20)-858993460, m.Allocatable.Memory().Value())
	n.ClusterNetwork = "terway-eniip"
	it.EniQuantity = 4
	it.EniPrivateIpAddressQuantity = 10
	nc.Status.ACKNodePool.Kubelet.MaxPods = tea.Int32(100)
	m, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	require.EqualValues(t, 33, m.Capacity.Pods().Value())
	it.EniQuantity = 0
	_, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.ErrorContains(t, err, "ENI metadata")
	n.ClusterNetwork = "terway"
	it.EniQuantity = 4
	m, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	require.EqualValues(t, 6, m.Capacity.Pods().Value())
}

func TestUnsupportedOrIncompleteResourceInputs(t *testing.T) {
	for name, change := range map[string]func(*instancetype.InstanceType, *v1alpha1.ECSNodeClass, *cluster.NetworkConfig){
		"missing pool": func(_ *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, _ *cluster.NetworkConfig) {
			nc.Status.ACKNodePool = nil
		},
		"unapplied kubelet": func(_ *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, _ *cluster.NetworkConfig) {
			nc.Spec.Kubelet = &v1alpha1.KubeletConfiguration{MaxPods: tea.Int32(20)}
		},
		"unknown podsPerCore": func(_ *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, _ *cluster.NetworkConfig) {
			nc.Status.ACKNodePool.Kubelet = &v1alpha1.KubeletConfiguration{PodsPerCore: tea.Int32(2)}
		},
		"exhausted CPU": func(_ *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, _ *cluster.NetworkConfig) {
			nc.Status.ACKNodePool.Kubelet = &v1alpha1.KubeletConfiguration{KubeReserved: map[corev1.ResourceName]string{corev1.ResourceCPU: "5"}}
		},
		"ambiguous eviction defaults": func(_ *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass, _ *cluster.NetworkConfig) {
			nc.Status.ACKNodePool.Kubelet = &v1alpha1.KubeletConfiguration{EvictionHard: map[string]string{"nodefs.available": "10%"}}
		},
		"unknown network": func(_ *instancetype.InstanceType, _ *v1alpha1.ECSNodeClass, n *cluster.NetworkConfig) {
			n.ClusterNetwork = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			it, nc, n := fixture()
			change(it, nc, n)
			_, err := Compute(zeroVM(), it, nc, n, "m-test")
			require.Error(t, err)
		})
	}
}

func TestMeasuredFilesystemRequiresMatchingImageAndLayout(t *testing.T) {
	it, nc, n := fixture()
	nc.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{Category: "cloud_ssd", Size: tea.Int32(40)}
	nc.Spec.DataDisks = []v1alpha1.DataDiskSpec{{Category: "cloud_ssd", Size: 100}, {Category: "cloud_ssd", Size: 100}}
	hash, err := nc.ResourceConfigurationHash()
	require.NoError(t, err)
	nc.Spec.NodeFilesystem = &v1alpha1.NodeFilesystem{ConfigurationHash: hash, ImageID: "m-test", Capacity: "90Gi", Allocatable: "80Gi"}
	m, err := Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	require.Equal(t, resource.MustParse("80Gi"), m.Allocatable[corev1.ResourceEphemeralStorage])
	_, err = Compute(zeroVM(), it, nc, n, "m-other")
	require.ErrorContains(t, err, "launch image")
	nc.Spec.NodeFilesystem.Capacity = "200Gi"
	_, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.ErrorContains(t, err, "not summed")
	nc.Spec.NodeFilesystem.Capacity = "90Gi"
	nc.Spec.NodeFilesystem.Allocatable = "89Gi"
	_, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.ErrorContains(t, err, "eviction")
	nc.Spec.NodeFilesystem.Allocatable = "80Gi"
	nc.Spec.DataDisks[0].Size = 200
	_, err = Compute(zeroVM(), it, nc, n, "m-test")
	require.ErrorContains(t, err, "different resource configuration")
}

func TestImmutableResourceSnapshot(t *testing.T) {
	it, nc, n := fixture()
	m, err := Compute(zeroVM(), it, nc, n, "m-test")
	require.NoError(t, err)
	tag, err := m.Encode()
	require.NoError(t, err)
	require.LessOrEqual(t, len(tag), 128)
	capacity, allocatable, err := Decode(tag)
	require.NoError(t, err)
	require.True(t, equality.Semantic.DeepEqual(m.Capacity, capacity))
	require.True(t, equality.Semantic.DeepEqual(m.Allocatable, allocatable))
	for _, bad := range []string{"", strings.Replace(tag, "1,", "2,", 1), strings.Replace(tag, ",3920,", ",5000,", 1), strings.Repeat("1", 129), "1,4000,8589934592,64,-,-,-,-,-,-,-,-,-"} {
		_, _, err = Decode(bad)
		require.Error(t, err, bad)
	}
}
