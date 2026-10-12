package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecsutil "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/utils/ecs"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"golang.org/x/time/rate"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	ErrPending = errors.New("hourly price refresh pending")
	ErrBusy    = errors.New("hourly price refresh capacity exhausted")
)

// Query describes the same image, disks and billing context used by RunInstances.
type Query struct {
	InstanceType, Zone, CapacityType, ImageID string
	Disks                                     v1alpha1.NormalizedDisks
	SpotStrategy                              string
	Marketplace                               bool
	Template                                  *v1alpha1.LaunchTemplatePricing
}

func ForNodeClass(nc *v1alpha1.ECSNodeClass, instanceType, zone, capacityType, imageID string) (Query, error) {
	spec, err := nc.LaunchSpec()
	if err != nil {
		return Query{}, err
	}
	disks, err := v1alpha1.NormalizeDisks(spec)
	if err != nil {
		return Query{}, err
	}
	q := Query{InstanceType: instanceType, Zone: zone, CapacityType: capacityType, ImageID: imageID, Disks: disks, SpotStrategy: ecsutil.SpotStrategyForCapacityType(capacityType, spec.SpotStrategy)}
	for _, image := range nc.Status.Images {
		if image.ID == imageID && image.OwnerAlias == "marketplace" {
			q.Marketplace = true
		}
	}
	if spec.LaunchTemplateID != nil {
		if nc.Status.LaunchTemplate == nil || nc.Status.LaunchTemplate.Pricing == nil {
			return Query{}, fmt.Errorf("launch template pricing context is unresolved")
		}
		q.Template = nc.Status.LaunchTemplate.Pricing.DeepCopy()
	}
	return q, nil
}

func (q Query) request(region string) (*ecs.DescribePriceRequest, error) {
	if region == "" || q.InstanceType == "" || q.Zone == "" || q.ImageID == "" {
		return nil, fmt.Errorf("hourly price requires region, instance type, zone and image")
	}
	if q.CapacityType != v1alpha1.CapacityTypeOnDemand && q.CapacityType != v1alpha1.CapacityTypeSpot {
		return nil, fmt.Errorf("unsupported price capacity type %q", q.CapacityType)
	}
	if (q.CapacityType == v1alpha1.CapacityTypeOnDemand && q.SpotStrategy != "NoSpot") || (q.CapacityType == v1alpha1.CapacityTypeSpot && q.SpotStrategy != "SpotAsPriceGo" && q.SpotStrategy != "SpotWithPriceLimit") {
		return nil, fmt.Errorf("price spot strategy does not match capacity type")
	}
	if q.Disks.SystemDisk.Category == "" || q.Disks.SystemDisk.Size <= 0 {
		return nil, fmt.Errorf("hourly price requires normalized disks")
	}
	r := &ecs.DescribePriceRequest{
		RegionId: tea.String(region), ResourceType: tea.String("instance"), InstanceType: tea.String(q.InstanceType),
		ZoneId: tea.String(q.Zone), ImageId: tea.String(q.ImageID), InstanceNetworkType: tea.String("vpc"),
		Amount: tea.Int32(1), PriceUnit: tea.String("Hour"), Period: tea.Int32(1),
		SpotStrategy: tea.String(q.SpotStrategy), InternetChargeType: tea.String("PayByTraffic"), InternetMaxBandwidthOut: tea.Int32(0),
		SystemDisk: &ecs.DescribePriceRequestSystemDisk{Category: tea.String(q.Disks.SystemDisk.Category), Size: tea.Int32(q.Disks.SystemDisk.Size)},
	}
	if q.CapacityType == v1alpha1.CapacityTypeSpot {
		r.SpotDuration = tea.Int32(1)
	}
	if q.Disks.SystemDisk.Category == "cloud_essd" {
		r.SystemDisk.PerformanceLevel = tea.String(q.Disks.SystemDisk.PerformanceLevel)
	}
	for _, d := range q.Disks.DataDisks {
		disk := &ecs.DescribePriceRequestDataDisk{Category: tea.String(d.Category), Size: tea.Int64(int64(d.Size))}
		if d.Category == "cloud_essd" {
			disk.PerformanceLevel = tea.String(d.PerformanceLevel)
		}
		r.DataDisk = append(r.DataDisk, disk)
	}
	if t := q.Template; t != nil {
		if t.UnsupportedReason != "" {
			return nil, fmt.Errorf("hourly price unavailable: %s", t.UnsupportedReason)
		}
		if t.InternetChargeType != "" {
			if t.InternetChargeType != "PayByTraffic" && t.InternetChargeType != "PayByBandwidth" {
				return nil, fmt.Errorf("unsupported template internet charge type")
			}
			r.InternetChargeType = tea.String(t.InternetChargeType)
		}
		if t.InternetMaxBandwidthOut < 0 || t.InternetMaxBandwidthOut > 100 {
			return nil, fmt.Errorf("template bandwidth cannot be represented by DescribePrice")
		}
		r.InternetMaxBandwidthOut = tea.Int32(t.InternetMaxBandwidthOut)
		if t.IoOptimized != "" {
			r.IoOptimized = tea.String(t.IoOptimized)
		}
		if q.CapacityType == v1alpha1.CapacityTypeSpot && t.SpotDuration != nil {
			if *t.SpotDuration != 0 && *t.SpotDuration != 1 {
				return nil, fmt.Errorf("unsupported template spot duration")
			}
			r.SpotDuration = tea.Int32(*t.SpotDuration)
		}
	}
	return r, nil
}

// Quote is an hourly transaction price in the provider's single account currency.
type Quote struct {
	Hourly   float64
	Currency string
}

type priceClient interface {
	DescribeOfferingPrice(context.Context, *ecs.DescribePriceRequest) (*ecs.DescribePriceResponse, error)
}
type entry struct {
	quote   Quote
	err     error
	expires time.Time
	done    chan struct{}
}

// Provider refreshes lazily, with bounded workers/cache and negative caching.
// Lookup never blocks scheduling on thousands of individual ECS price requests.
type Provider struct {
	region   string
	client   priceClient
	ctx      context.Context
	mu       sync.Mutex
	cache    map[string]*entry
	currency string
	workers  chan struct{}
	limiter  *rate.Limiter
	now      func() time.Time
}

type Option func(*Provider)

func WithContext(ctx context.Context) Option { return func(p *Provider) { p.ctx = ctx } }
func NewProvider(region string, client interface{}, options ...Option) *Provider {
	p := &Provider{region: region, ctx: context.Background(), cache: map[string]*entry{}, workers: make(chan struct{}, 4), limiter: rate.NewLimiter(5, 4), now: time.Now}
	p.client, _ = client.(priceClient)
	for _, option := range options {
		option(p)
	}
	return p
}

// Lookup returns only unexpired quotes. Unknown quotes are errors, never free.
func (p *Provider) Lookup(q Query) (Quote, error) {
	if p == nil || p.client == nil {
		return Quote{}, fmt.Errorf("complete ECS hourly pricing client is unavailable")
	}
	if err := p.ctx.Err(); err != nil {
		return Quote{}, err
	}
	r, err := q.request(p.region)
	if err != nil {
		return Quote{}, err
	}
	encoded, err := json.Marshal(struct {
		Request     *ecs.DescribePriceRequest
		Marketplace bool
	}{r, q.Marketplace})
	if err != nil {
		return Quote{}, err
	}
	key := string(encoded)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if e := p.cache[key]; e != nil {
		if e.done != nil {
			return Quote{}, ErrPending
		}
		if now.Before(e.expires) {
			return e.quote, e.err
		}
		delete(p.cache, key)
	}
	if len(p.cache) >= 4096 {
		for k, e := range p.cache {
			if e.done == nil && !now.Before(e.expires) {
				delete(p.cache, k)
			}
		}
		if len(p.cache) >= 4096 {
			return Quote{}, ErrBusy
		}
	}
	select {
	case p.workers <- struct{}{}:
	default:
		return Quote{}, ErrBusy
	}
	e := &entry{done: make(chan struct{})}
	p.cache[key] = e
	go p.refresh(e, r, q.CapacityType, q.Marketplace)
	return Quote{}, ErrPending
}

// Get waits for a single quote; callers can bound waiting with their context.
func (p *Provider) Get(ctx context.Context, q Query) (Quote, error) {
	if err := ctx.Err(); err != nil {
		return Quote{}, err
	}
	quote, err := p.Lookup(q)
	if !errors.Is(err, ErrPending) {
		return quote, err
	}
	r, err := q.request(p.region)
	if err != nil {
		return Quote{}, err
	}
	encoded, err := json.Marshal(struct {
		Request     *ecs.DescribePriceRequest
		Marketplace bool
	}{r, q.Marketplace})
	if err != nil {
		return Quote{}, err
	}
	p.mu.Lock()
	e := p.cache[string(encoded)]
	var done chan struct{}
	if e != nil {
		done = e.done
	}
	p.mu.Unlock()
	if done != nil {
		select {
		case <-ctx.Done():
			return Quote{}, ctx.Err()
		case <-done:
		}
	}
	return p.Lookup(q)
}

func (p *Provider) refresh(e *entry, r *ecs.DescribePriceRequest, capacityType string, marketplace bool) {
	defer func() { <-p.workers }()
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()
	err := p.limiter.Wait(ctx)
	var response *ecs.DescribePriceResponse
	if err == nil {
		response, err = p.client.DescribeOfferingPrice(ctx, r)
	}
	var quote Quote
	if err == nil {
		quote, err = parsePrice(response, marketplace)
	}
	p.mu.Lock()
	if err == nil {
		if p.currency == "" {
			p.currency = quote.Currency
		}
		if p.currency != quote.Currency {
			err = fmt.Errorf("mixed hourly price currency: expected %s, received %s", p.currency, quote.Currency)
		}
	}
	ttl := 5 * time.Minute
	if capacityType == v1alpha1.CapacityTypeSpot {
		ttl = time.Minute
	}
	if err != nil {
		ttl = 30 * time.Second
		quote = Quote{}
	}
	e.quote, e.err, e.expires = quote, err, p.now().Add(ttl)
	close(e.done)
	e.done = nil
	p.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		log.FromContext(p.ctx).V(1).Info("hourly quote unavailable", "instanceType", tea.StringValue(r.InstanceType), "zone", tea.StringValue(r.ZoneId), "capacityType", capacityType, "error", err)
	}
}

func parsePrice(r *ecs.DescribePriceResponse, marketplace bool) (Quote, error) {
	if r == nil || r.Body == nil || r.Body.PriceInfo == nil || r.Body.PriceInfo.Price == nil {
		return Quote{}, fmt.Errorf("missing hourly price response")
	}
	price := r.Body.PriceInfo.Price
	currency := tea.StringValue(price.Currency)
	if currency != "CNY" && currency != "USD" {
		return Quote{}, fmt.Errorf("missing or unsupported hourly price currency %q", currency)
	}
	if price.TradePrice == nil {
		return Quote{}, fmt.Errorf("missing hourly transaction price")
	}
	total := float64(*price.TradePrice)
	if math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
		return Quote{}, fmt.Errorf("invalid hourly transaction price")
	}
	// Marketplace licenses are reported separately from the ECS transaction price.
	if marketplace && (r.Body.PriceInfo.RelatedPrice == nil || r.Body.PriceInfo.RelatedPrice.MarketplaceImagePrice == nil) {
		return Quote{}, fmt.Errorf("missing marketplace image licensing price")
	}
	if related := r.Body.PriceInfo.RelatedPrice; related != nil && related.MarketplaceImagePrice != nil {
		license := related.MarketplaceImagePrice
		if tea.StringValue(license.Currency) != currency || license.TradePrice == nil {
			return Quote{}, fmt.Errorf("incomplete or mixed-currency marketplace image price")
		}
		value := float64(*license.TradePrice)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return Quote{}, fmt.Errorf("invalid marketplace image price")
		}
		total += value
	}
	return Quote{Hourly: total, Currency: currency}, nil
}
