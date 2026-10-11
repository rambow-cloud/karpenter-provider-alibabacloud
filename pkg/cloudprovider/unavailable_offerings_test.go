package cloudprovider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type shortageECS struct {
	imageLaunchECS
	failCode string
}

func (c *shortageECS) RunInstances(_ context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	c.requests = append(c.requests, req)
	if tea.StringValue(req.VSwitchId) == "vsw-fail" {
		return nil, tea.NewSDKError(map[string]interface{}{"code": c.failCode, "message": "offline launch failure"})
	}
	return &ecs.RunInstancesResponse{Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: []*string{tea.String("i-offline")}}}}, nil
}

func TestActualCreateFeedsSchedulingAvailabilityAndExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := clocktesting.NewFakeClock(time.Now())
	unavailable := cache.NewUnavailableOfferingsCacheWithClock(time.Minute, clk)
	ecsClient := &shortageECS{failCode: "OperationDenied.NoStock"}
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Status: v1alpha1.ECSNodeClassStatus{
		Images:     []v1alpha1.Image{{ID: "m-x86", Architecture: "x86_64"}},
		VSwitches:  []v1alpha1.VSwitch{{ID: "vsw-fail", ZoneID: "cn-hangzhou-i"}},
		Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}},
	}}
	provider := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(class).Build(), instanceTypeProvider: instancetype.NewProvider("cn-hangzhou", ecsClient), instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", ecsClient), unavailableOfferings: unavailable}
	defer provider.instanceProvider.Batcher.Close()
	types := []*instancetype.InstanceType{{Name: "ecs.g7.large", Architecture: "x86_64"}}
	claim := &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{Requirements: []coreapis.NodeSelectorRequirementWithMinValues{{Key: coreapis.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{"spot"}}}}}
	launch := func() error {
		_, err := provider.createInstanceWithRetry(ctx, claim.DeepCopy(), class, types, class.Status.Images, class.Status.VSwitches, []v1alpha1.SecurityGroup{{ID: "sg-1"}}, "", nil)
		return err
	}
	require.Error(t, launch())
	require.Len(t, ecsClient.requests, 1)
	require.Error(t, launch(), "a second NodeClaim must skip the cached combination")
	require.Len(t, ecsClient.requests, 1)
	pool := &coreapis.NodePool{Spec: coreapis.NodePoolSpec{Template: coreapis.NodeClaimTemplate{Spec: coreapis.NodeClaimTemplateSpec{NodeClassRef: &coreapis.NodeClassReference{Name: class.Name}}}}}
	result, err := provider.GetInstanceTypes(ctx, pool)
	require.NoError(t, err)
	require.Len(t, result, 1)
	for _, offering := range result[0].Offerings {
		require.Equal(t, offering.CapacityType() != "spot", offering.Available)
	}
	require.Len(t, result[0].Offerings, 2, "negative availability must preserve drift membership")
	clk.Step(time.Minute)
	result, err = provider.GetInstanceTypes(ctx, pool)
	require.NoError(t, err)
	require.Len(t, result[0].Offerings.Available(), 2)
	require.Error(t, launch())
	require.Len(t, ecsClient.requests, 2)
}

func TestCapacityFeedbackClassificationAndZoneIsolation(t *testing.T) {
	for _, code := range []string{"OperationDenied.NoStock", "Zone.NotOnSale", "InvalidResourceType.NotSupported", "InvalidVSwitchId.IpNotEnough", "Forbidden.RAM", "InvalidParameter", "Throttling.User", "ServiceUnavailable"} {
		t.Run(code, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ecsClient := &shortageECS{failCode: code}
			unavailable := cache.NewUnavailableOfferingsCache()
			provider := &CloudProvider{instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", ecsClient), unavailableOfferings: unavailable}
			defer provider.instanceProvider.Batcher.Close()
			_, err := provider.createInstanceWithRetry(ctx, &coreapis.NodeClaim{}, &v1alpha1.ECSNodeClass{}, []*instancetype.InstanceType{{Name: "ecs.g7.large", Architecture: "x86_64"}}, []v1alpha1.Image{{ID: "m-x86", Architecture: "x86_64"}}, []v1alpha1.VSwitch{{ID: "vsw-fail", Zone: "zone-a", AvailableIPAddressCount: 100}, {ID: "vsw-same-zone", Zone: "zone-a", AvailableIPAddressCount: 90}, {ID: "vsw-other-zone", Zone: "zone-b", AvailableIPAddressCount: 80}}, []v1alpha1.SecurityGroup{{ID: "sg-1"}}, "", nil)
			cached := code == "OperationDenied.NoStock" || code == "Zone.NotOnSale" || code == "InvalidResourceType.NotSupported"
			require.Equal(t, !cached, unavailable.IsAvailable("ecs.g7.large", "zone-a", "on-demand"))
			require.True(t, unavailable.IsAvailable("ecs.g7.large", "zone-b", "on-demand"))
			require.True(t, unavailable.IsAvailable("ecs.g7.large", "zone-a", "spot"))
			if cached {
				require.NoError(t, err)
				require.Len(t, ecsClient.requests, 2)
				require.Equal(t, "vsw-other-zone", tea.StringValue(ecsClient.requests[1].VSwitchId))
			} else if code == "InvalidVSwitchId.IpNotEnough" {
				require.NoError(t, err)
				require.Len(t, ecsClient.requests, 2)
				require.Equal(t, "vsw-same-zone", tea.StringValue(ecsClient.requests[1].VSwitchId))
			} else {
				require.Error(t, err)
				require.Len(t, ecsClient.requests, 1)
			}
		})
	}
	// Unstructured errors (including diagnostic text mentioning a code) never
	// poison shared availability. Timeout/permission/query failures are not stock.
	unavailable := cache.NewUnavailableOfferingsCache()
	_, _ = vswitchFallbackCreate(context.Background(), instance.CreateOptions{InstanceType: "ecs.g7.large"}, []v1alpha1.VSwitch{{ID: "vsw-1", Zone: "zone-a"}}, func(context.Context, instance.CreateOptions) (string, error) {
		return "", fmt.Errorf("query failed; previous OperationDenied.NoStock")
	}, unavailable)
	require.Zero(t, unavailable.Count())
}
