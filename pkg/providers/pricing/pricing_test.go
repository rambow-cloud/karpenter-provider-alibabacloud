package pricing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	clocktesting "k8s.io/utils/clock/testing"
)

type priceFake struct {
	mu       sync.Mutex
	requests []*ecs.DescribePriceRequest
	fn       func(context.Context, *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error)
}

func (f *priceFake) DescribeOfferingPrice(ctx context.Context, r *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	return f.fn(ctx, r)
}
func (f *priceFake) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
func response(price float32, currency string) *ecs.DescribePriceResponse {
	return &ecs.DescribePriceResponse{Body: &ecs.DescribePriceResponseBody{PriceInfo: &ecs.DescribePriceResponseBodyPriceInfo{Price: &ecs.DescribePriceResponseBodyPriceInfoPrice{TradePrice: tea.Float32(price), Currency: tea.String(currency)}}}}
}
func query(t *testing.T, capacityType string) Query {
	t.Helper()
	q, err := ForNodeClass(&v1alpha1.ECSNodeClass{}, "ecs.g7.large", "cn-hangzhou-i", capacityType, "m-test")
	require.NoError(t, err)
	return q
}
func provider(f *priceFake) *Provider {
	p := NewProvider("cn-hangzhou", f)
	p.limiter = rate.NewLimiter(rate.Inf, 4)
	return p
}

func TestHourlyRequestUsesLaunchGeometryAndSeparatesQuotes(t *testing.T) {
	f := &priceFake{fn: func(_ context.Context, r *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
		if tea.StringValue(r.SpotStrategy) == "NoSpot" {
			return response(1, "CNY"), nil
		}
		return response(.2, "CNY"), nil
	}}
	p := provider(f)
	nc := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{SystemDisk: &v1alpha1.SystemDiskSpec{Size: tea.Int32(80), PerformanceLevel: tea.String("PL1")}, DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_ssd", Size: 100}}}}
	q, err := ForNodeClass(nc, "ecs.g7.large", "cn-hangzhou-i", "on-demand", "m-paid")
	require.NoError(t, err)
	quote, err := p.Get(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, Quote{1, "CNY"}, quote)
	r := f.requests[0]
	require.Equal(t, "Hour", *r.PriceUnit)
	require.EqualValues(t, 1, *r.Period)
	require.EqualValues(t, 1, *r.Amount)
	require.Equal(t, "vpc", *r.InstanceNetworkType)
	require.Equal(t, "m-paid", *r.ImageId)
	require.Equal(t, "cn-hangzhou-i", *r.ZoneId)
	require.Equal(t, "NoSpot", *r.SpotStrategy)
	require.EqualValues(t, 80, *r.SystemDisk.Size)
	require.Equal(t, "PL1", *r.SystemDisk.PerformanceLevel)
	require.Len(t, r.DataDisk, 1)
	require.EqualValues(t, 100, *r.DataDisk[0].Size)
	_, err = p.Get(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, 1, f.count())
	for _, change := range []func(*Query){
		func(q *Query) { q.CapacityType = "spot"; q.SpotStrategy = "SpotAsPriceGo" },
		func(q *Query) { q.Zone = "cn-hangzhou-j" },
		func(q *Query) { q.ImageID = "m-other" },
		func(q *Query) { q.Disks.SystemDisk.Size = 120 },
		func(q *Query) {
			q.Template = &v1alpha1.LaunchTemplatePricing{InternetChargeType: "PayByBandwidth", InternetMaxBandwidthOut: 5}
		},
	} {
		changed := q
		change(&changed)
		_, err = p.Get(context.Background(), changed)
		require.NoError(t, err)
	}
	require.Equal(t, 6, f.count(), "billing tuples must not share prices")
	require.EqualValues(t, 1, *f.requests[1].SpotDuration)
}

func TestRefreshCoalescesAndBoundsInFlightRequests(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	f := &priceFake{fn: func(ctx context.Context, _ *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return response(1, "CNY"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	p := provider(f)
	q := query(t, "on-demand")
	_, err := p.Lookup(q)
	require.ErrorIs(t, err, ErrPending)
	<-entered
	for i := 0; i < 20; i++ {
		_, err = p.Lookup(q)
		require.ErrorIs(t, err, ErrPending)
	}
	for i := 0; i < 3; i++ {
		other := q
		other.Zone = fmt.Sprintf("zone-%d", i)
		_, err = p.Lookup(other)
		require.ErrorIs(t, err, ErrPending)
		<-entered
	}
	other := q
	other.Zone = "zone-busy"
	_, err = p.Lookup(other)
	require.ErrorIs(t, err, ErrBusy)
	require.Equal(t, 4, f.count())
	require.Len(t, p.cache, 4)
	close(release)
	quote, err := p.Get(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, 1.0, quote.Hourly)
}

func TestExpiryAndFailureBackoff(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	f := &priceFake{fn: func(_ context.Context, r *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
		if tea.StringValue(r.ImageId) == "m-fail" {
			return nil, fmt.Errorf("Forbidden.RAM")
		}
		return response(float32(clk.Now().Unix()%100+1), "USD"), nil
	}}
	p := provider(f)
	p.now = clk.Now
	spot, demand := query(t, "spot"), query(t, "on-demand")
	before, err := p.Get(context.Background(), spot)
	require.NoError(t, err)
	_, err = p.Get(context.Background(), demand)
	require.NoError(t, err)
	bad := demand
	bad.ImageID = "m-fail"
	_, err = p.Get(context.Background(), bad)
	require.ErrorContains(t, err, "Forbidden.RAM")
	for i := 0; i < 10; i++ {
		_, err = p.Lookup(bad)
		require.ErrorContains(t, err, "Forbidden.RAM")
	}
	require.Equal(t, 3, f.count())
	clk.Step(30 * time.Second)
	_, err = p.Get(context.Background(), bad)
	require.ErrorContains(t, err, "Forbidden.RAM")
	require.Equal(t, 4, f.count())
	clk.Step(31 * time.Second)
	after, err := p.Get(context.Background(), spot)
	require.NoError(t, err)
	require.NotEqual(t, before.Hourly, after.Hourly)
	_, err = p.Get(context.Background(), demand)
	require.NoError(t, err)
	require.Equal(t, 5, f.count(), "on-demand remains fresh for five minutes")
	clk.Step(5 * time.Minute)
	_, err = p.Get(context.Background(), demand)
	require.NoError(t, err)
	require.Equal(t, 6, f.count())
}

func TestUnknownInvalidAndMixedCurrencyPricesAreUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *ecs.DescribePriceResponse
	}{
		{"nil", nil}, {"empty", &ecs.DescribePriceResponse{}},
		{"currency", response(1, "")}, {"unsupported currency", response(1, "EUR")},
		{"negative", response(-1, "USD")}, {"nan", response(float32(math.NaN()), "USD")}, {"infinity", response(float32(math.Inf(1)), "USD")},
		{"missing transaction", &ecs.DescribePriceResponse{Body: &ecs.DescribePriceResponseBody{PriceInfo: &ecs.DescribePriceResponseBodyPriceInfo{Price: &ecs.DescribePriceResponseBodyPriceInfoPrice{Currency: tea.String("USD")}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quote, err := parsePrice(tc.r, false)
			require.Error(t, err)
			require.Zero(t, quote.Hourly)
		})
	}
	f := &priceFake{fn: func(_ context.Context, r *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
		if tea.StringValue(r.ZoneId) == "other" {
			return response(0.1, "USD"), nil
		}
		return response(0, "CNY"), nil
	}}
	p := provider(f)
	q := query(t, "on-demand")
	quote, err := p.Get(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, Quote{0, "CNY"}, quote, "an explicitly quoted promotion is different from missing metadata")
	q.Zone = "other"
	_, err = p.Get(context.Background(), q)
	require.ErrorContains(t, err, "mixed hourly price currency")
	_, err = p.Lookup(q)
	require.Error(t, err)
	require.Equal(t, 2, f.count())
}

func TestMarketplaceLicenseAndTemplateLimitations(t *testing.T) {
	_, err := parsePrice(response(1, "CNY"), true)
	require.ErrorContains(t, err, "missing marketplace")
	nc := &v1alpha1.ECSNodeClass{Status: v1alpha1.ECSNodeClassStatus{Images: []v1alpha1.Image{{ID: "m-paid", OwnerAlias: "marketplace"}}}}
	marketplace, err := ForNodeClass(nc, "ecs.g7.large", "cn-hangzhou-i", "on-demand", "m-paid")
	require.NoError(t, err)
	require.True(t, marketplace.Marketplace)
	r := response(1, "CNY")
	r.Body.PriceInfo.RelatedPrice = &ecs.DescribePriceResponseBodyPriceInfoRelatedPrice{MarketplaceImagePrice: &ecs.DescribePriceResponseBodyPriceInfoRelatedPriceMarketplaceImagePrice{Currency: tea.String("CNY"), TradePrice: tea.Float32(.5)}}
	quote, err := parsePrice(r, true)
	require.NoError(t, err)
	require.Equal(t, 1.5, quote.Hourly)
	r.Body.PriceInfo.RelatedPrice.MarketplaceImagePrice.Currency = tea.String("USD")
	_, err = parsePrice(r, true)
	require.ErrorContains(t, err, "marketplace")
	q := query(t, "spot")
	q.Template = &v1alpha1.LaunchTemplatePricing{SpotDuration: tea.Int32(0)}
	req, err := q.request("cn-hangzhou")
	require.NoError(t, err)
	require.EqualValues(t, 0, *req.SpotDuration)
	q.Template.UnsupportedReason = "unmodeled system disk IOPS"
	_, err = q.request("cn-hangzhou")
	require.ErrorContains(t, err, "unmodeled")
}

func TestCanceledRefreshAndMissingClientNeverQuoteFree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &priceFake{fn: func(ctx context.Context, _ *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	p := NewProvider("cn-hangzhou", f, WithContext(ctx))
	q := query(t, "on-demand")
	_, err := p.Lookup(q)
	require.ErrorIs(t, err, ErrPending)
	cancel()
	_, err = p.Get(context.Background(), q)
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewProvider("cn-hangzhou", nil).Lookup(q)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrPending))
}
