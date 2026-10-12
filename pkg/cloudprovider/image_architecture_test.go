package cloudprovider

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type imageLaunchECS struct {
	clients.ECSClient
	requests []*ecs.RunInstancesRequest
}

func (c *imageLaunchECS) RunInstances(_ context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	c.requests = append(c.requests, req)
	return &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: []*string{tea.String("i-offline")}}}}, nil
}
func (c *imageLaunchECS) DescribeZones(context.Context) (*ecs.DescribeZonesResponse, error) {
	return &ecs.DescribeZonesResponse{Body: &ecs.DescribeZonesResponseBody{Zones: &ecs.DescribeZonesResponseBodyZones{Zone: []*ecs.DescribeZonesResponseBodyZonesZone{{ZoneId: tea.String("cn-hangzhou-i")}}}}}, nil
}
func (c *imageLaunchECS) DescribeAvailableResource(context.Context, *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error) {
	return nil, fmt.Errorf("offline inventory unavailable")
}
func (c *imageLaunchECS) DescribeInstanceTypes(context.Context, []string) (*ecs.DescribeInstanceTypesResponse, error) {
	return &ecs.DescribeInstanceTypesResponse{Body: &ecs.DescribeInstanceTypesResponseBody{InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{InstanceType: []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
		{InstanceTypeId: tea.String("ecs.g8y.large"), CpuArchitecture: tea.String("ARM64"), CpuCoreCount: tea.Int32(2), MemorySize: tea.Float32(8)},
		{InstanceTypeId: tea.String("ecs.g7.large"), CpuArchitecture: tea.String("X86_64"), CpuCoreCount: tea.Int32(2), MemorySize: tea.Float32(8)},
		{InstanceTypeId: tea.String("ecs.unknown.large"), CpuArchitecture: tea.String("unknown"), CpuCoreCount: tea.Int32(2), MemorySize: tea.Float32(8)},
	}}}}, nil
}

func TestSchedulingOnlyExposesInstanceTypesWithKnownImageArchitecture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		images []v1alpha1.Image
		types  []string
	}{
		{"amd64 only", []v1alpha1.Image{{ID: "m-x86", Architecture: "x86_64"}}, []string{"ecs.g7.large"}},
		{"dual architecture", []v1alpha1.Image{{ID: "m-x86", Architecture: "amd64"}, {ID: "m-arm", Architecture: "aarch64"}}, []string{"ecs.g7.large", "ecs.g8y.large"}},
		{"unknown image", []v1alpha1.Image{{ID: "m-unknown"}}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Status: v1alpha1.ECSNodeClassStatus{ACKNodePool: &v1alpha1.ResolvedACKNodePool{ID: "np-offline", ClusterID: "c-offline"}, Images: tc.images, VSwitches: []v1alpha1.VSwitch{{ID: "vsw-offline", ZoneID: "cn-hangzhou-i"}}, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
			provider := &CloudProvider{clusterNetworkConfig: &cluster.NetworkConfig{ClusterNetwork: "kube-flannel-ds", MaxPods: 64}, kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(class).Build(), instanceTypeProvider: instancetype.NewProvider("cn-hangzhou", &imageLaunchECS{})}
			types, err := provider.instanceTypeProvider.List(context.Background())
			require.NoError(t, err)
			provider.pricingProvider = warmOfflinePricing(t, context.Background(), class, types, tc.images, class.Status.VSwitches)
			pool := &coreapis.NodePool{Spec: coreapis.NodePoolSpec{Template: coreapis.NodeClaimTemplate{Spec: coreapis.NodeClaimTemplateSpec{NodeClassRef: &coreapis.NodeClassReference{Name: "class"}}}}}
			result, err := provider.GetInstanceTypes(context.Background(), pool)
			if len(tc.types) == 0 {
				require.ErrorContains(t, err, "no instance types available")
				require.Empty(t, result)
				return
			}
			require.NoError(t, err)
			names := make([]string, 0, len(result))
			for _, it := range result {
				names = append(names, it.Name)
			}
			require.ElementsMatch(t, tc.types, names)
		})
	}
}

func TestActualCreateRejectsMismatchedImagesAndChoosesNewestCompatible(t *testing.T) {
	for _, tc := range []struct {
		name                string
		images              []v1alpha1.Image
		wantType, wantImage string
	}{
		{"skip ARM for x86 image", []v1alpha1.Image{{ID: "m-old", Architecture: "x86_64", CreationTime: "2025-01-01T00:00:00Z"}, {ID: "m-new", Architecture: "amd64", CreationTime: "2026-10-01T00:00:00Z"}}, "ecs.g7.large", "m-new"},
		{"dual architecture retains ARM", []v1alpha1.Image{{ID: "m-x86", Architecture: "x86_64"}, {ID: "m-arm", Architecture: "aarch64"}}, "ecs.g8y.large", "m-arm"},
		{"unknown architecture never launches", []v1alpha1.Image{{ID: "m-unknown"}}, "", ""},
		{"32-bit image cannot authorize amd64", []v1alpha1.Image{{ID: "m-i386", Architecture: "i386"}}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &imageLaunchECS{}
			provider := &CloudProvider{clusterNetworkConfig: &cluster.NetworkConfig{ClusterNetwork: "kube-flannel-ds", MaxPods: 64}, instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", client)}
			types := []*instancetype.InstanceType{{Name: "ecs.g8y.large", Architecture: "ARM64"}, {Name: "ecs.g7.large", Architecture: "X86_64"}}
			nc := &v1alpha1.ECSNodeClass{}
			vs := []v1alpha1.VSwitch{{ID: "vsw-offline", ZoneID: "cn-hangzhou-i"}}
			provider.pricingProvider = warmOfflinePricing(t, ctx, nc, types, tc.images, vs)
			defer provider.instanceProvider.Batcher.Close()
			_, err := provider.createInstanceWithRetry(ctx, onDemandClaim(), nc, types, tc.images, vs, []v1alpha1.SecurityGroup{{ID: "sg-offline"}}, "offline", nil)
			if tc.wantImage == "" {
				require.Error(t, err)
				require.Empty(t, client.requests)
				return
			}
			require.NoError(t, err)
			require.Len(t, client.requests, 1)
			require.Equal(t, tc.wantType, tea.StringValue(client.requests[0].InstanceType))
			require.Equal(t, tc.wantImage, tea.StringValue(client.requests[0].ImageId))
		})
	}
}
