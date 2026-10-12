package cloudprovider

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestContainerLocalDiskMetadataControlsActualLaunch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		known bool
		count int32
		want  bool
	}{{"unknown", false, 0, false}, {"local", true, 1, false}, {"diskless", true, 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := &imageLaunchECS{}
			p := &CloudProvider{instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", e)}
			defer p.instanceProvider.Batcher.Close()
			nc := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 40}}}}
			it := &instancetype.InstanceType{Name: "ecs.g7.large", Architecture: "X86_64", LocalDiskKnown: tc.known, LocalDiskCount: tc.count}
			images := []v1alpha1.Image{{ID: "lifsea-test", ImageType: "ContainerOS", OSType: "linux", Architecture: "x86_64"}}
			vs := []v1alpha1.VSwitch{{ID: "vsw-test", ZoneID: "cn-hangzhou-i"}}
			p.pricingProvider = warmOfflinePricing(t, ctx, nc, []*instancetype.InstanceType{it}, images, vs)
			_, err := p.createInstanceWithRetry(ctx, onDemandClaim(), nc, []*instancetype.InstanceType{it}, images, vs, []v1alpha1.SecurityGroup{{ID: "sg-test"}}, "bootstrap", nil)
			if tc.want {
				require.NoError(t, err)
				require.Len(t, e.requests, 1)
			} else {
				require.Error(t, err)
				require.Empty(t, e.requests)
			}
		})
	}
}
