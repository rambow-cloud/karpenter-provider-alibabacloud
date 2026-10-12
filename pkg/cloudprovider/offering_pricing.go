package cloudprovider

import (
	"fmt"
	"sort"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	corev1 "k8s.io/api/core/v1"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func vSwitchZone(vs v1alpha1.VSwitch) string {
	if vs.ZoneID != "" {
		return vs.ZoneID
	}
	return vs.Zone
}

func (c *CloudProvider) offeringQuote(nc *v1alpha1.ECSNodeClass, it *instancetype.InstanceType, images []v1alpha1.Image, zone, capacityType string) (pricing.Quote, error) {
	image := selectBootImage(images, it, nc)
	if image.ID == "" {
		return pricing.Quote{}, fmt.Errorf("no compatible image for hourly quote")
	}
	query, err := pricing.ForNodeClass(nc, it.Name, zone, capacityType, image.ID)
	if err != nil {
		return pricing.Quote{}, err
	}
	return c.pricingProvider.Lookup(query)
}

type launchCandidate struct {
	instanceType *instancetype.InstanceType
	image        v1alpha1.Image
	vswitch      v1alpha1.VSwitch
	capacityType string
	price        float64
}

func (c *CloudProvider) pricedLaunchCandidates(claim *coreapis.NodeClaim, nc *v1alpha1.ECSNodeClass, types []*instancetype.InstanceType, images []v1alpha1.Image, vswitches []v1alpha1.VSwitch) ([]launchCandidate, error) {
	reqs := scheduling.NewNodeSelectorRequirementsWithMinValues(claim.Spec.Requirements...)
	var candidates []launchCandidate
	var priceErr error
	for _, it := range types {
		image := selectBootImage(images, it, nc)
		if image.ID == "" {
			continue
		}
		for _, vs := range vswitches {
			zone := vSwitchZone(vs)
			for _, ct := range []string{v1alpha1.CapacityTypeSpot, v1alpha1.CapacityTypeOnDemand} {
				offeringReqs := scheduling.NewRequirements(
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
					scheduling.NewRequirement(coreapis.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, ct),
				)
				if !reqs.IsCompatible(offeringReqs, scheduling.AllowUndefinedWellKnownLabels) {
					continue
				}
				if it.Zones != nil {
					stock := false
					for _, availableCT := range it.Zones[zone].CapacityTypes {
						if availableCT == ct {
							stock = true
						}
					}
					if !stock {
						continue
					}
				}
				if c.unavailableOfferings != nil && !c.unavailableOfferings.IsAvailable(it.Name, zone, ct) {
					continue
				}
				quote, err := c.offeringQuote(nc, it, images, zone, ct)
				if err != nil {
					priceErr = err
					continue
				}
				candidates = append(candidates, launchCandidate{it, image, vs, ct, quote.Hourly})
			}
		}
	}
	if len(candidates) == 0 {
		if priceErr == nil {
			return nil, fmt.Errorf("no compatible stocked launch candidates")
		}
		return nil, fmt.Errorf("no currently priced launch candidates: %w", priceErr)
	}
	// Karpenter WorstLaunchPrice assumes Spot precedence when both types are
	// permitted. Within each capacity type, use the same hourly quote as scheduling.
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.capacityType != b.capacityType {
			return a.capacityType == v1alpha1.CapacityTypeSpot
		}
		if a.price != b.price {
			return a.price < b.price
		}
		return a.vswitch.AvailableIPAddressCount > b.vswitch.AvailableIPAddressCount
	})
	return candidates, nil
}
