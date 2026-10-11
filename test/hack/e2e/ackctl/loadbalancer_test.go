package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	alb "github.com/alibabacloud-go/alb-20200616/v2/client"
	nlb "github.com/alibabacloud-go/nlb-20220430/v4/client"
	slb "github.com/alibabacloud-go/slb-20140515/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type fakeSLBDiscovery func(*slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error)

func (f fakeSLBDiscovery) DescribeLoadBalancers(r *slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error) {
	return f(r)
}

type fakeALBDiscovery func(*alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error)

func (f fakeALBDiscovery) ListLoadBalancers(r *alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error) {
	return f(r)
}

type fakeNLBDiscovery func(*nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error)

func (f fakeNLBDiscovery) ListLoadBalancers(r *nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error) {
	return f(r)
}

func TestV2SLBDiscoveryKeepsRegionTagsAndPagination(t *testing.T) {
	selector := map[string]string{"testing/cluster": "test", "karpenter.sh/managed-by": "true"}
	calls := 0
	client := fakeSLBDiscovery(func(req *slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error) {
		calls++
		require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
		require.Equal(t, int32(calls), tea.Int32Value(req.PageNumber))
		require.Equal(t, int32(50), tea.Int32Value(req.PageSize))
		tags := map[string]string{}
		for _, tag := range req.Tag {
			tags[tea.StringValue(tag.Key)] = tea.StringValue(tag.Value)
		}
		require.Equal(t, selector, tags)
		count := 50
		if calls == 2 {
			count = 1
		}
		rows := make([]*slb.DescribeLoadBalancersResponseBodyLoadBalancersLoadBalancer, count)
		for i := range rows {
			rows[i] = &slb.DescribeLoadBalancersResponseBodyLoadBalancersLoadBalancer{LoadBalancerId: tea.String(fmt.Sprintf("lb-%d-%d", calls, i)), LoadBalancerStatus: tea.String("active")}
		}
		return &slb.DescribeLoadBalancersResponse{Body: &slb.DescribeLoadBalancersResponseBody{LoadBalancers: &slb.DescribeLoadBalancersResponseBodyLoadBalancers{LoadBalancer: rows}}}, nil
	})
	var report SweepReport
	require.NoError(t, describeOwnedSLBs(client, "cn-hangzhou", selector, &report))
	require.Equal(t, 2, calls)
	require.Len(t, report.Resources, 51)
	require.Equal(t, SweepResource{Type: "slb", ID: "lb-2-0", Status: "active"}, report.Resources[50])
}

func TestV2ALBAndNLBDiscoveryKeepsTagsAndTokens(t *testing.T) {
	selector := map[string]string{"testing/cluster": "test"}
	t.Run("alb", func(t *testing.T) {
		calls := 0
		client := fakeALBDiscovery(func(req *alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error) {
			calls++
			require.Equal(t, int32(100), tea.Int32Value(req.MaxResults))
			require.Len(t, req.Tag, 1)
			require.Equal(t, "testing/cluster", tea.StringValue(req.Tag[0].Key))
			require.Equal(t, "test", tea.StringValue(req.Tag[0].Value))
			body := &alb.ListLoadBalancersResponseBody{LoadBalancers: []*alb.ListLoadBalancersResponseBodyLoadBalancers{nil, {LoadBalancerId: tea.String(fmt.Sprintf("alb-%d", calls)), LoadBalancerStatus: tea.String("Active")}}}
			if calls == 1 {
				require.Nil(t, req.NextToken)
				body.NextToken = tea.String("next")
			} else {
				require.Equal(t, "next", tea.StringValue(req.NextToken))
			}
			return &alb.ListLoadBalancersResponse{Body: body}, nil
		})
		var report SweepReport
		require.NoError(t, describeOwnedALBs(client, selector, &report))
		require.Equal(t, 2, calls)
		require.Len(t, report.Resources, 2)
		require.Equal(t, "alb-2", report.Resources[1].ID)
	})
	t.Run("nlb", func(t *testing.T) {
		calls := 0
		client := fakeNLBDiscovery(func(req *nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error) {
			calls++
			require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
			require.Equal(t, int32(100), tea.Int32Value(req.MaxResults))
			require.Len(t, req.Tag, 1)
			require.Equal(t, "testing/cluster", tea.StringValue(req.Tag[0].Key))
			require.Equal(t, "test", tea.StringValue(req.Tag[0].Value))
			body := &nlb.ListLoadBalancersResponseBody{LoadBalancers: []*nlb.ListLoadBalancersResponseBodyLoadBalancers{{LoadBalancerId: tea.String(fmt.Sprintf("nlb-%d", calls)), LoadBalancerStatus: tea.String("Active")}}}
			if calls == 1 {
				require.Nil(t, req.NextToken)
				body.NextToken = tea.String("next")
			} else {
				require.Equal(t, "next", tea.StringValue(req.NextToken))
			}
			return &nlb.ListLoadBalancersResponse{Body: body}, nil
		})
		var report SweepReport
		require.NoError(t, describeOwnedNLBs(client, "cn-hangzhou", selector, &report))
		require.Equal(t, 2, calls)
		require.Len(t, report.Resources, 2)
		require.Equal(t, "nlb-2", report.Resources[1].ID)
	})
}

func TestV2DiscoveryRejectsMissingBodiesAndPropagatesErrors(t *testing.T) {
	cloudErr := errors.New("denied")
	for _, mode := range []string{"nil response", "nil body", "API error"} {
		t.Run(mode, func(t *testing.T) {
			var wantErr error
			if mode == "API error" {
				wantErr = cloudErr
			}
			var report SweepReport
			clb := fakeSLBDiscovery(func(*slb.DescribeLoadBalancersRequest) (*slb.DescribeLoadBalancersResponse, error) {
				if mode == "nil body" {
					return &slb.DescribeLoadBalancersResponse{}, nil
				}
				return nil, wantErr
			})
			a := fakeALBDiscovery(func(*alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error) {
				if mode == "nil body" {
					return &alb.ListLoadBalancersResponse{}, nil
				}
				return nil, wantErr
			})
			n := fakeNLBDiscovery(func(*nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error) {
				if mode == "nil body" {
					return &nlb.ListLoadBalancersResponse{}, nil
				}
				return nil, wantErr
			})
			for _, err := range []error{describeOwnedSLBs(clb, "cn-hangzhou", nil, &report), describeOwnedALBs(a, nil, &report), describeOwnedNLBs(n, "cn-hangzhou", nil, &report)} {
				require.Error(t, err)
				if wantErr != nil {
					require.ErrorIs(t, err, wantErr)
				} else {
					require.ErrorContains(t, err, "empty body")
				}
			}
		})
	}
}

func TestV2DiscoveryRejectsRepeatedPaginationTokens(t *testing.T) {
	var report SweepReport
	albCalls, nlbCalls := 0, 0
	a := fakeALBDiscovery(func(*alb.ListLoadBalancersRequest) (*alb.ListLoadBalancersResponse, error) {
		albCalls++
		return &alb.ListLoadBalancersResponse{Body: &alb.ListLoadBalancersResponseBody{NextToken: tea.String("loop")}}, nil
	})
	n := fakeNLBDiscovery(func(*nlb.ListLoadBalancersRequest) (*nlb.ListLoadBalancersResponse, error) {
		nlbCalls++
		return &nlb.ListLoadBalancersResponse{Body: &nlb.ListLoadBalancersResponseBody{NextToken: tea.String("loop")}}, nil
	})
	require.ErrorContains(t, describeOwnedALBs(a, nil, &report), "repeated pagination token")
	require.ErrorContains(t, describeOwnedNLBs(n, "cn-hangzhou", nil, &report), "repeated pagination token")
	require.Equal(t, 2, albCalls)
	require.Equal(t, 2, nlbCalls)
}

type fakeSLBProtection struct {
	deleteProtection       func(*slb.SetLoadBalancerDeleteProtectionRequest) (*slb.SetLoadBalancerDeleteProtectionResponse, error)
	modificationProtection func(*slb.SetLoadBalancerModificationProtectionRequest) (*slb.SetLoadBalancerModificationProtectionResponse, error)
}

func (f fakeSLBProtection) SetLoadBalancerDeleteProtection(r *slb.SetLoadBalancerDeleteProtectionRequest) (*slb.SetLoadBalancerDeleteProtectionResponse, error) {
	return f.deleteProtection(r)
}
func (f fakeSLBProtection) SetLoadBalancerModificationProtection(r *slb.SetLoadBalancerModificationProtectionRequest) (*slb.SetLoadBalancerModificationProtectionResponse, error) {
	return f.modificationProtection(r)
}

func TestV2SLBProtectionPreservesRequestsAndStopsOnError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		apiErr := errors.New("protection denied")
		client := fakeSLBProtection{
			deleteProtection: func(req *slb.SetLoadBalancerDeleteProtectionRequest) (*slb.SetLoadBalancerDeleteProtectionResponse, error) {
				calls++
				require.Equal(t, "lb-owned", tea.StringValue(req.LoadBalancerId))
				require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
				require.Equal(t, "off", tea.StringValue(req.DeleteProtection))
				if fail {
					return nil, apiErr
				}
				return &slb.SetLoadBalancerDeleteProtectionResponse{}, nil
			},
			modificationProtection: func(req *slb.SetLoadBalancerModificationProtectionRequest) (*slb.SetLoadBalancerModificationProtectionResponse, error) {
				calls++
				require.Equal(t, "lb-owned", tea.StringValue(req.LoadBalancerId))
				require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
				require.Equal(t, "NonProtection", tea.StringValue(req.ModificationProtectionStatus))
				return &slb.SetLoadBalancerModificationProtectionResponse{}, nil
			},
		}
		err := disableSLBProtection(client, "cn-hangzhou", "lb-owned")
		if fail {
			require.ErrorIs(t, err, apiErr)
			require.Equal(t, 1, calls)
		} else {
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		}
	}
}

type fakeSLBListener struct {
	slbListenerClient
	describe     func(*slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error)
	start        func(*slb.StartLoadBalancerListenerRequest) (*slb.StartLoadBalancerListenerResponse, error)
	addACL       func(*slb.AddAccessControlListEntryRequest) (*slb.AddAccessControlListEntryResponse, error)
	describeACL  func(*slb.DescribeAccessControlListAttributeRequest) (*slb.DescribeAccessControlListAttributeResponse, error)
	setAttribute func(*slb.SetLoadBalancerTCPListenerAttributeRequest) (*slb.SetLoadBalancerTCPListenerAttributeResponse, error)
}

func (f fakeSLBListener) DescribeLoadBalancerTCPListenerAttribute(r *slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error) {
	return f.describe(r)
}
func (f fakeSLBListener) StartLoadBalancerListener(r *slb.StartLoadBalancerListenerRequest) (*slb.StartLoadBalancerListenerResponse, error) {
	return f.start(r)
}
func (f fakeSLBListener) AddAccessControlListEntry(r *slb.AddAccessControlListEntryRequest) (*slb.AddAccessControlListEntryResponse, error) {
	return f.addACL(r)
}
func (f fakeSLBListener) DescribeAccessControlListAttribute(r *slb.DescribeAccessControlListAttributeRequest) (*slb.DescribeAccessControlListAttributeResponse, error) {
	return f.describeACL(r)
}
func (f fakeSLBListener) SetLoadBalancerTCPListenerAttribute(r *slb.SetLoadBalancerTCPListenerAttributeRequest) (*slb.SetLoadBalancerTCPListenerAttributeResponse, error) {
	return f.setAttribute(r)
}

func TestV2SLBListenerPreservesRestrictedACLAndRegion(t *testing.T) {
	calls := []string{}
	client := fakeSLBListener{
		describe: func(req *slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error) {
			calls = append(calls, "describe")
			require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
			require.Equal(t, "lb-owned", tea.StringValue(req.LoadBalancerId))
			require.Equal(t, int32(6443), tea.Int32Value(req.ListenerPort))
			return &slb.DescribeLoadBalancerTCPListenerAttributeResponse{Body: &slb.DescribeLoadBalancerTCPListenerAttributeResponseBody{AclStatus: tea.String("on"), AclId: tea.String("acl-owned")}}, nil
		},
		addACL: func(req *slb.AddAccessControlListEntryRequest) (*slb.AddAccessControlListEntryResponse, error) {
			calls = append(calls, "add")
			require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
			require.Equal(t, "acl-owned", tea.StringValue(req.AclId))
			var entries []map[string]string
			require.NoError(t, json.Unmarshal([]byte(tea.StringValue(req.AclEntrys)), &entries))
			require.Equal(t, []map[string]string{{"entry": "192.0.2.0/24", "comment": "karpenter-e2e"}}, entries)
			return &slb.AddAccessControlListEntryResponse{}, nil
		},
		describeACL: func(req *slb.DescribeAccessControlListAttributeRequest) (*slb.DescribeAccessControlListAttributeResponse, error) {
			calls = append(calls, "acl")
			require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
			require.Equal(t, "acl-owned", tea.StringValue(req.AclId))
			return &slb.DescribeAccessControlListAttributeResponse{Body: &slb.DescribeAccessControlListAttributeResponseBody{TotalAclEntry: tea.Int32(1)}}, nil
		},
		start: func(req *slb.StartLoadBalancerListenerRequest) (*slb.StartLoadBalancerListenerResponse, error) {
			calls = append(calls, "start")
			require.Equal(t, "cn-hangzhou", tea.StringValue(req.RegionId))
			require.Equal(t, "lb-owned", tea.StringValue(req.LoadBalancerId))
			require.Equal(t, int32(6443), tea.Int32Value(req.ListenerPort))
			require.Equal(t, "tcp", tea.StringValue(req.ListenerProtocol))
			return &slb.StartLoadBalancerListenerResponse{}, nil
		},
	}
	require.NoError(t, ensureAPISLBListenerAccess(client, "cn-hangzhou", "lb-owned", []string{"192.0.2.0/24"}))
	require.Equal(t, []string{"describe", "add", "acl", "start"}, calls)
}

func TestV2SLBListenerMissingBodyStopsBeforeMutations(t *testing.T) {
	client := fakeSLBListener{describe: func(*slb.DescribeLoadBalancerTCPListenerAttributeRequest) (*slb.DescribeLoadBalancerTCPListenerAttributeResponse, error) {
		return &slb.DescribeLoadBalancerTCPListenerAttributeResponse{}, nil
	}}
	require.ErrorContains(t, ensureAPISLBListenerAccess(client, "cn-hangzhou", "lb-owned", []string{"192.0.2.0/24"}), "empty body")
	client.describeACL = func(*slb.DescribeAccessControlListAttributeRequest) (*slb.DescribeAccessControlListAttributeResponse, error) {
		return &slb.DescribeAccessControlListAttributeResponse{}, nil
	}
	client.addACL = func(*slb.AddAccessControlListEntryRequest) (*slb.AddAccessControlListEntryResponse, error) {
		return &slb.AddAccessControlListEntryResponse{}, nil
	}
	require.ErrorContains(t, addSLBACLAllowlists(client, "cn-hangzhou", "acl-owned", []string{"192.0.2.0/24"}), "empty body")
}

func TestV2LoadBalancerFactoriesValidateConfiguration(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"}}, {AlibabaCloud: CloudConfig{AccessKeyID: "test", AccessKeySecret: "test"}}} {
		_, err := newSLBClient(cfg)
		require.Error(t, err)
		_, err = newALBClient(cfg)
		require.Error(t, err)
		_, err = newNLBClient(cfg)
		require.Error(t, err)
	}
	cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou", AccessKeyID: "local-test", AccessKeySecret: "local-test"}}
	clb, err := newSLBClient(cfg)
	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", tea.StringValue(clb.RegionId))
	require.Equal(t, 30_000, tea.IntValue(clb.ConnectTimeout))
	a, err := newALBClient(cfg)
	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", tea.StringValue(a.RegionId))
	require.Equal(t, 30_000, tea.IntValue(a.ConnectTimeout))
	n, err := newNLBClient(cfg)
	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", tea.StringValue(n.RegionId))
	require.Equal(t, 30_000, tea.IntValue(n.ConnectTimeout))
}
