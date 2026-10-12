package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type networkFake struct {
	clients.CSClient
	addons   *cs.ListClusterAddonInstancesResponse
	addon    *cs.GetClusterAddonInstanceResponse
	detail   *cs.DescribeClusterDetailResponse
	errAt    string
	contexts []context.Context
}

func (f *networkFake) ListClusterAddonInstances(ctx context.Context, _ string) (*cs.ListClusterAddonInstancesResponse, error) {
	f.contexts = append(f.contexts, ctx)
	if f.errAt == "list" {
		return nil, fmt.Errorf("Forbidden.RAM")
	}
	return f.addons, nil
}
func (f *networkFake) GetClusterAddonInstance(ctx context.Context, _, _ string) (*cs.GetClusterAddonInstanceResponse, error) {
	f.contexts = append(f.contexts, ctx)
	if f.errAt == "addon" {
		return nil, fmt.Errorf("Forbidden.RAM")
	}
	return f.addon, nil
}
func (f *networkFake) DescribeClusterDetail(ctx context.Context, _ string) (*cs.DescribeClusterDetailResponse, error) {
	f.contexts = append(f.contexts, ctx)
	if f.errAt == "detail" {
		return nil, fmt.Errorf("Forbidden.RAM")
	}
	return f.detail, nil
}
func networkFixture(name string) *networkFake {
	return &networkFake{
		addons: &cs.ListClusterAddonInstancesResponse{Body: &cs.ListClusterAddonInstancesResponseBody{Addons: []*cs.ListClusterAddonInstancesResponseBodyAddons{{Name: tea.String(name), State: tea.String("active")}}}},
		addon:  &cs.GetClusterAddonInstanceResponse{Body: &cs.GetClusterAddonInstanceResponseBody{Name: tea.String(name), Config: tea.String("{}")}},
		detail: &cs.DescribeClusterDetailResponse{Body: &cs.DescribeClusterDetailResponseBody{IpStack: tea.String("ipv4"), NodeCidrMask: tea.String("26")}},
	}
}
func TestNetworkDiscoveryNeverInventsDensityAfterFailure(t *testing.T) {
	for name, change := range map[string]func(*networkFake){
		"list error": func(f *networkFake) { f.errAt = "list" }, "addon error": func(f *networkFake) { f.errAt = "addon" }, "detail error": func(f *networkFake) { f.errAt = "detail" },
		"nil list": func(f *networkFake) { f.addons = nil }, "empty list": func(f *networkFake) { f.addons.Body.Addons = nil }, "nil addon": func(f *networkFake) { f.addon = nil }, "nil detail": func(f *networkFake) { f.detail = nil },
		"unknown network": func(f *networkFake) { f.addons.Body.Addons[0].Name = tea.String("unknown") }, "transition": func(f *networkFake) { f.addons.Body.Addons[0].State = tea.String("updating") },
		"duplicate": func(f *networkFake) { f.addons.Body.Addons = append(f.addons.Body.Addons, f.addons.Body.Addons[0]) }, "conflict": func(f *networkFake) {
			f.addons.Body.Addons = append(f.addons.Body.Addons, &cs.ListClusterAddonInstancesResponseBodyAddons{Name: tea.String("terway-eniip"), State: tea.String("active")})
		},
		"missing stack": func(f *networkFake) { f.detail.Body.IpStack = nil }, "unknown stack": func(f *networkFake) { f.detail.Body.IpStack = tea.String("future") },
		"missing mask": func(f *networkFake) { f.detail.Body.NodeCidrMask = nil }, "invalid mask": func(f *networkFake) { f.detail.Body.NodeCidrMask = tea.String("bad") }, "mask32": func(f *networkFake) { f.detail.Body.NodeCidrMask = tea.String("32") },
		"wrong identity": func(f *networkFake) { f.addon.Body.Name = tea.String("terway") },
		"changed state":  func(f *networkFake) { f.addon.Body.State = tea.String("updating") },
	} {
		t.Run(name, func(t *testing.T) {
			f := networkFixture("kube-flannel-ds")
			change(f)
			config, err := InitializeClusterNetworkConfig(context.Background(), f, "cluster")
			require.Error(t, err)
			require.Nil(t, config)
		})
	}
}
func TestNetworkReadsRetainContextAndKnownLimits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := networkFixture("kube-flannel-ds")
	config, err := InitializeClusterNetworkConfig(ctx, f, "cluster")
	require.NoError(t, err)
	require.EqualValues(t, 64, config.MaxPods)
	for _, actual := range f.contexts {
		require.Same(t, ctx, actual)
	}
	f.detail.Body.NodeCidrMask = tea.String("16")
	config, err = InitializeClusterNetworkConfig(ctx, f, "cluster")
	require.NoError(t, err)
	require.EqualValues(t, 256, config.MaxPods, "ACK Flannel cannot advertise 65536 pods")
	f.contexts = nil
	cancel()
	_, err = InitializeClusterNetworkConfig(ctx, f, "cluster")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, f.contexts)
}
func TestTerwayConfigurationIsExplicitAndValidated(t *testing.T) {
	for _, value := range []*string{nil, tea.String(""), tea.String("null"), tea.String("[]"), tea.String("bad"), tea.String(`{"ENITrunking":null}`), tea.String(`{"ENITrunking":"true"}`)} {
		f := networkFixture("terway-eniip")
		f.addon.Body.Config = value
		_, err := InitializeClusterNetworkConfig(context.Background(), f, "cluster")
		require.Error(t, err)
	}
	f := networkFixture("terway-eniip")
	f.addon.Body.Config = tea.String(`{"ENITrunking":true,"OtherAddonSetting":1}`)
	config, err := InitializeClusterNetworkConfig(context.Background(), f, "cluster")
	require.NoError(t, err)
	require.True(t, config.TrunkEniEnabled)
}
