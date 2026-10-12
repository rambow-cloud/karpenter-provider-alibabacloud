package main

import (
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	slb "github.com/alibabacloud-go/slb-20140515/v4/client"
)

func loadBalancerClientConfig(cfg *Config) (*openapi.Config, error) {
	return cloudClientConfig(cfg)
}

// Narrow interfaces allow migration regressions to be tested without cloud calls.
type slbDiscoveryClient interface {
	DescribeLoadBalancers(*slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error)
}

type slbProtectionClient interface {
	SetLoadBalancerDeleteProtection(*slb.SetLoadBalancerDeleteProtectionRequest) (*slb.SetLoadBalancerDeleteProtectionResponse, error)
	SetLoadBalancerModificationProtection(*slb.SetLoadBalancerModificationProtectionRequest) (*slb.SetLoadBalancerModificationProtectionResponse, error)
}

type slbListenerClient interface {
	DescribeLoadBalancerTCPListenerAttribute(*slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error)
	SetLoadBalancerTCPListenerAttribute(*slb.SetLoadBalancerTCPListenerAttributeRequest) (*slb.SetLoadBalancerTCPListenerAttributeResponse, error)
	SetListenerAccessControlStatus(*slb.SetListenerAccessControlStatusRequest) (*slb.SetListenerAccessControlStatusResponse, error)
	StartLoadBalancerListener(*slb.StartLoadBalancerListenerRequest) (*slb.StartLoadBalancerListenerResponse, error)
	AddAccessControlListEntry(*slb.AddAccessControlListEntryRequest) (*slb.AddAccessControlListEntryResponse, error)
	DescribeAccessControlListAttribute(*slb.DescribeAccessControlListAttributeRequest) (*slb.DescribeAccessControlListAttributeResponse, error)
}
