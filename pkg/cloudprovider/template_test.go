package cloudprovider

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"testing"
)

func TestTemplateLayoutReachesRunInstancesWithoutAddingDisks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &imageLaunchECS{}
	p := &CloudProvider{clusterNetworkConfig: &cluster.NetworkConfig{ClusterNetwork: "kube-flannel-ds", MaxPods: 64}, instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", e)}
	defer p.instanceProvider.Batcher.Close()
	disks, err := v1alpha1.NormalizeDisks(v1alpha1.ECSNodeClassSpec{SystemDisk: &v1alpha1.SystemDiskSpec{Size: tea.Int32(80)}, DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_ssd", Size: 100}}})
	require.NoError(t, err)
	nc := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{LaunchTemplateID: tea.String("lt-test")}, Status: v1alpha1.ECSNodeClassStatus{ACKNodePool: &v1alpha1.ResolvedACKNodePool{ID: "np-offline", ClusterID: "c-offline"}, LaunchTemplate: &v1alpha1.ResolvedLaunchTemplate{ID: "lt-test", Version: 7, InstanceType: "ecs.g7.large", Disks: disks, Pricing: &v1alpha1.LaunchTemplatePricing{}}}}
	spec, err := nc.LaunchSpec()
	require.NoError(t, err)
	nc.Spec = spec
	images := []v1alpha1.Image{{ID: "lifsea-test", Architecture: "x86_64", OSType: "linux", ImageType: "ContainerOS"}}
	require.NoError(t, imagefamily.ValidateBootImages(spec, images))
	types := []*instancetype.InstanceType{{Name: "ecs.g8y.large", Architecture: "ARM64"}, {Name: "ecs.g7.large", Architecture: "X86_64", LocalDiskKnown: true}}
	vs := []v1alpha1.VSwitch{{ID: "vsw-test", ZoneID: "cn-hangzhou-i"}}
	p.pricingProvider = warmOfflinePricing(t, ctx, nc, types, images, vs)
	_, err = p.createInstanceWithRetry(ctx, onDemandClaim(), nc, types, images, vs, []v1alpha1.SecurityGroup{{ID: "sg-test"}}, "generated-bootstrap", nil)
	require.NoError(t, err)
	require.Len(t, e.requests, 1)
	r := e.requests[0]
	require.Equal(t, "lt-test", *r.LaunchTemplateId)
	require.EqualValues(t, 7, *r.LaunchTemplateVersion)
	require.Equal(t, "80", *r.SystemDisk.Size)
	require.Len(t, r.DataDisk, 1)
	require.EqualValues(t, 100, *r.DataDisk[0].Size)
	require.Equal(t, "ecs.g7.large", *r.InstanceType)
	nc.Spec.DataDisks[0].Category = "ephemeral_ssd"
	_, err = p.createInstanceWithRetry(ctx, &coreapis.NodeClaim{}, nc, []*instancetype.InstanceType{{Name: "ecs.g7.large", Architecture: "X86_64"}}, images, []v1alpha1.VSwitch{{ID: "vsw-test"}}, []v1alpha1.SecurityGroup{{ID: "sg-test"}}, "generated-bootstrap", nil)
	require.ErrorContains(t, err, "persistent")
	require.Len(t, e.requests, 1, "invalid layout must never be submitted")
}
