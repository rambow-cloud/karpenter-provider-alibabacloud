package main

import (
	"fmt"

	alb "github.com/alibabacloud-go/alb-20200616/v2/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	nlb "github.com/alibabacloud-go/nlb-20220430/v4/client"
	slb "github.com/alibabacloud-go/slb-20140515/v4/client"
	"github.com/alibabacloud-go/tea/tea"
)

func loadBalancerClientConfig(cfg *Config) (*openapi.Config, error) {
	if cfg == nil || cfg.AlibabaCloud.AccessKeyID == "" || cfg.AlibabaCloud.AccessKeySecret == "" || cfg.AlibabaCloud.RegionID == "" {
		return nil, fmt.Errorf("AlibabaCloud region and access key credentials are required")
	}
	return &openapi.Config{
		AccessKeyId:     tea.String(cfg.AlibabaCloud.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AlibabaCloud.AccessKeySecret),
		RegionId:        tea.String(cfg.AlibabaCloud.RegionID),
		ConnectTimeout:  tea.Int(30_000), // Tea SDK timeouts are milliseconds.
		MaxIdleConns:    tea.Int(100),
	}, nil
}

// Narrow interfaces allow migration regressions to be tested without cloud calls.
type slbDiscoveryClient interface {
	DescribeLoadBalancers(*slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error)
}

type albDiscoveryClient interface {
	ListLoadBalancers(*alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error)
}

type nlbDiscoveryClient interface {
	ListLoadBalancers(*nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error)
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
