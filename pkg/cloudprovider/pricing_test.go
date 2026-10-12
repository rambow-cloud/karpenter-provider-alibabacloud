package cloudprovider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	corescheduling "sigs.k8s.io/karpenter/pkg/controllers/provisioning/scheduling"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// Offline fixtures use the real pricing provider and a complete SDK price response.
// No production guard is disabled to let an unquoted fixture launch.
type offlinePriceClient struct {
	mu       sync.Mutex
	requests []*ecs.DescribePriceRequest
	price    func(*ecs.DescribePriceRequest) (float32, error)
}

func (f *offlinePriceClient) DescribeOfferingPrice(_ context.Context, r *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	value, err := float32(1), error(nil)
	if f.price != nil {
		value, err = f.price(r)
	}
	if err != nil {
		return nil, err
	}
	return &ecs.DescribePriceResponse{Body: &ecs.DescribePriceResponseBody{PriceInfo: &ecs.DescribePriceResponseBodyPriceInfo{Price: &ecs.DescribePriceResponseBodyPriceInfoPrice{Currency: tea.String("CNY"), TradePrice: tea.Float32(value)}}}}, nil
}
func warmOfflinePricing(t *testing.T, ctx context.Context, nc *v1alpha1.ECSNodeClass, types []*instancetype.InstanceType, images []v1alpha1.Image, vswitches []v1alpha1.VSwitch) *pricing.Provider {
	t.Helper()
	p := pricing.NewProvider("cn-hangzhou", &offlinePriceClient{}, pricing.WithContext(ctx))
	warmQuotes(t, ctx, p, nc, types, images, vswitches)
	return p
}
func warmQuotes(t *testing.T, ctx context.Context, p *pricing.Provider, nc *v1alpha1.ECSNodeClass, types []*instancetype.InstanceType, images []v1alpha1.Image, vswitches []v1alpha1.VSwitch) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, it := range types {
		image := selectBootImage(images, it, nc)
		if image.ID == "" {
			continue
		}
		for _, vs := range vswitches {
			for _, ct := range []string{"on-demand", "spot"} {
				q, err := pricing.ForNodeClass(nc, it.Name, vSwitchZone(vs), ct, image.ID)
				require.NoError(t, err)
				_, err = p.Get(ctx, q)
				require.NoError(t, err)
			}
		}
	}
}
func onDemandClaim() *coreapis.NodeClaim {
	return &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{Requirements: []coreapis.NodeSelectorRequirementWithMinValues{{Key: coreapis.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{"on-demand"}}}}}
}

func TestPricedCandidatesNeverUseUnknownPrice(t *testing.T) {
	p := &CloudProvider{}
	nc := &v1alpha1.ECSNodeClass{}
	_, err := p.pricedLaunchCandidates(onDemandClaim(), nc, []*instancetype.InstanceType{{Name: "ecs.g7.large", Architecture: "x86_64"}}, []v1alpha1.Image{{ID: "m-test", Architecture: "x86_64"}}, []v1alpha1.VSwitch{{ID: "vsw-test", ZoneID: "cn-hangzhou-i"}})
	require.ErrorContains(t, err, "pricing client is unavailable")
}

type offeringECS struct{ imageLaunchECS }

func (*offeringECS) DescribeInstanceTypes(context.Context, []string) (*ecs.DescribeInstanceTypesResponse, error) {
	var types []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType
	for _, name := range []string{"ecs.expensive.large", "ecs.cheap.large", "ecs.unquoted.large"} {
		types = append(types, &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{InstanceTypeId: tea.String(name), CpuArchitecture: tea.String("X86_64"), CpuCoreCount: tea.Int32(2), MemorySize: tea.Float32(8)})
	}
	return &ecs.DescribeInstanceTypesResponse{Body: &ecs.DescribeInstanceTypesResponseBody{InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{InstanceType: types}}}, nil
}
func (*offeringECS) DescribeZones(context.Context) (*ecs.DescribeZonesResponse, error) {
	return &ecs.DescribeZonesResponse{Body: &ecs.DescribeZonesResponseBody{Zones: &ecs.DescribeZonesResponseBodyZones{Zone: []*ecs.DescribeZonesResponseBodyZonesZone{{ZoneId: tea.String("cn-hangzhou-i")}, {ZoneId: tea.String("cn-hangzhou-j")}}}}}, nil
}

func TestSchedulingLaunchAndConsolidationShareHourlyPrices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &offeringECS{}
	quoteClient := &offlinePriceClient{price: func(r *ecs.DescribePriceRequest) (float32, error) {
		if tea.StringValue(r.InstanceType) == "ecs.unquoted.large" {
			return 0, context.DeadlineExceeded
		}
		base := float32(2)
		if tea.StringValue(r.InstanceType) == "ecs.cheap.large" {
			base = 1
		}
		if tea.StringValue(r.ZoneId) == "cn-hangzhou-j" {
			base += .5
		}
		if tea.StringValue(r.SpotStrategy) != "NoSpot" {
			base /= 4
		}
		return base, nil
	}}
	pricer := pricing.NewProvider("cn-hangzhou", quoteClient, pricing.WithContext(ctx))
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	nc := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Status: v1alpha1.ECSNodeClassStatus{Images: []v1alpha1.Image{{ID: "m-test", Architecture: "x86_64"}}, VSwitches: []v1alpha1.VSwitch{{ID: "vsw-j", ZoneID: "cn-hangzhou-j", AvailableIPAddressCount: 100}, {ID: "vsw-i", ZoneID: "cn-hangzhou-i", AvailableIPAddressCount: 10}}, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	p := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc).Build(), instanceTypeProvider: instancetype.NewProvider("cn-hangzhou", e), instanceProvider: instance.NewProvider(ctx, "cn-hangzhou", e), pricingProvider: pricer}
	t.Cleanup(p.instanceProvider.Batcher.Close)
	pool := &coreapis.NodePool{Spec: coreapis.NodePoolSpec{Template: coreapis.NodeClaimTemplate{Spec: coreapis.NodeClaimTemplateSpec{NodeClassRef: &coreapis.NodeClassReference{Name: nc.Name}}}}}
	types, err := p.instanceTypeProvider.List(ctx)
	require.NoError(t, err)
	for _, it := range types {
		for _, vs := range nc.Status.VSwitches {
			for _, ct := range []string{"on-demand", "spot"} {
				q, err := pricing.ForNodeClass(nc, it.Name, vSwitchZone(vs), ct, "m-test")
				require.NoError(t, err)
				_, err = pricer.Get(ctx, q)
				if it.Name == "ecs.unquoted.large" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}
		}
	}
	result, err := p.GetInstanceTypes(ctx, pool)
	require.NoError(t, err)
	require.Len(t, result, 3)
	for _, it := range result {
		require.Len(t, it.Offerings, 4, "unknown prices retain drift membership")
		if it.Name == "ecs.unquoted.large" {
			require.Empty(t, it.Offerings.Available())
		}
	}
	reqs := scheduling.NewRequirements(scheduling.NewRequirement(coreapis.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, "on-demand"))
	ordered := coreprovider.InstanceTypes(result).OrderByPrice(reqs)
	require.Equal(t, "ecs.cheap.large", ordered[0].Name)
	// Execute Karpenter's actual strict replacement-price filter, not a copy.
	replacement := &corescheduling.NodeClaim{NodeClaimTemplate: corescheduling.NodeClaimTemplate{InstanceTypeOptions: result}}
	_, err = replacement.RemoveInstanceTypeOptionsByPriceAndMinValues(reqs, 2)
	require.NoError(t, err)
	require.Len(t, replacement.InstanceTypeOptions, 1)
	require.Equal(t, "ecs.cheap.large", replacement.InstanceTypeOptions[0].Name)
	_, err = p.createInstanceWithRetry(ctx, onDemandClaim(), nc, types, nc.Status.Images, nc.Status.VSwitches, []v1alpha1.SecurityGroup{{ID: "sg-test"}}, "bootstrap", nil)
	require.NoError(t, err)
	require.Len(t, e.requests, 1)
	require.Equal(t, "ecs.cheap.large", *e.requests[0].InstanceType)
	require.Equal(t, "vsw-i", *e.requests[0].VSwitchId, "price takes priority over free-IP count")
	require.Equal(t, "NoSpot", *e.requests[0].SpotStrategy)
	// When both capacity types are allowed, preserve Karpenter's Spot precedence.
	_, err = p.createInstanceWithRetry(ctx, &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{Requirements: []coreapis.NodeSelectorRequirementWithMinValues{{Key: coreapis.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{"spot", "on-demand"}}}}}, nc, types, nc.Status.Images, nc.Status.VSwitches, []v1alpha1.SecurityGroup{{ID: "sg-test"}}, "bootstrap", nil)
	require.NoError(t, err)
	require.Len(t, e.requests, 2)
	require.Equal(t, "SpotAsPriceGo", *e.requests[1].SpotStrategy)
	require.Equal(t, "ecs.cheap.large", *e.requests[1].InstanceType)
}
