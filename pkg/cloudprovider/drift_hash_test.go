package cloudprovider

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type driftECS struct{ clients.ECSClient }

func (*driftECS) DescribeInstances(context.Context, *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{
		TotalCount: tea.Int32(1), PageNumber: tea.Int32(1), PageSize: tea.Int32(100),
		Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{{
			InstanceId: tea.String("i-worker"), RegionId: tea.String("cn-hangzhou"), ZoneId: tea.String("zone-a"), InstanceType: tea.String("ecs.g7.large"), ImageId: tea.String("m-worker"), Status: tea.String("Running"), Cpu: tea.Int32(2), Memory: tea.Int32(8192), CreationTime: tea.String("2026-10-01T00:00:00Z"),
			VpcAttributes:    &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributes{VSwitchId: tea.String("vsw-worker")},
			SecurityGroupIds: &ecs.DescribeInstancesResponseBodyInstancesInstanceSecurityGroupIds{SecurityGroupId: tea.StringSlice([]string{"sg-a", "sg-b"})},
		}}},
	}}, nil
}

func TestActualDriftUsesStaticHashAndDynamicMembership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*v1alpha1.ECSNodeClass, *coreapis.NodeClaim)
		want   coreprovider.DriftReason
	}{
		{"unchanged", func(*v1alpha1.ECSNodeClass, *coreapis.NodeClaim) {}, ""},
		{"equivalent selectors", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: tea.String("acs:family")}, {ID: tea.String("m-worker")}}
			n.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{Tags: map[string]string{"env": "prod"}}}
			n.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{Name: tea.String("workers")}}
			n.Status.SecurityGroups[0], n.Status.SecurityGroups[1] = n.Status.SecurityGroups[1], n.Status.SecurityGroups[0]
		}, ""},
		{"older allowed image retained", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Status.Images = append(n.Status.Images, v1alpha1.Image{ID: "m-newer"})
		}, ""},
		{"image removed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Status.Images = []v1alpha1.Image{{ID: "m-newer"}}
		}, ImageDrift},
		{"vswitch removed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Status.VSwitches = []v1alpha1.VSwitch{{ID: "vsw-new"}}
		}, VSwitchDrift},
		{"security group changed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Status.SecurityGroups = []v1alpha1.SecurityGroup{{ID: "sg-a"}}
		}, SecurityGroupDrift},
		{"bootstrap changed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) { n.Spec.UserData = tea.String("echo changed") }, NodeClassDrift},
		{"disk changed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) {
			n.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{Size: tea.Int32(80)}
		}, NodeClassDrift},
		{"role changed", func(n *v1alpha1.ECSNodeClass, _ *coreapis.NodeClaim) { n.Spec.Role = tea.String("other-role") }, NodeClassDrift},
		{"wrong hash version", func(_ *v1alpha1.ECSNodeClass, c *coreapis.NodeClaim) {
			c.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] = "v1"
		}, NodeClassDrift},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Spec: v1alpha1.ECSNodeClassSpec{ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-worker")}}}, Status: v1alpha1.ECSNodeClassStatus{Images: []v1alpha1.Image{{ID: "m-worker"}}, VSwitches: []v1alpha1.VSwitch{{ID: "vsw-worker"}}, SecurityGroups: []v1alpha1.SecurityGroup{{ID: "sg-a"}, {ID: "sg-b"}}, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
			hash, err := n.Hash()
			require.NoError(t, err)
			claim := &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Name: n.Name}}, Status: coreapis.NodeClaimStatus{ProviderID: "cn-hangzhou.i-worker"}}
			setNodeClassHashAnnotation(claim, hash)
			tc.change(n, claim)
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			provider := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(n).Build(), instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", &driftECS{})}
			defer provider.instanceProvider.Batcher.Close()
			got, err := provider.IsDrifted(ctx, claim)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
