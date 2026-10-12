package launchtemplate

import (
	"context"
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestTemplatePricingSnapshotUsesResolvedVersion(t *testing.T) {
	d := &versionData{InternetChargeType: tea.String("PayByBandwidth"), InternetMaxBandwidthOut: tea.Int32(5), IoOptimized: tea.String("optimized"), SpotDuration: tea.Int32(0)}
	c := &diskTemplateClient{rows: []*versionSet{{LaunchTemplateId: tea.String("lt-test"), VersionNumber: tea.Int64(3), LaunchTemplateData: d}}}
	p := NewProvider("cn-hangzhou", c)
	data, err := p.GetData(context.Background(), "lt-test", tea.Int64(3))
	require.NoError(t, err)
	require.Equal(t, "PayByBandwidth", data.Pricing.InternetChargeType)
	require.EqualValues(t, 5, data.Pricing.InternetMaxBandwidthOut)
	require.EqualValues(t, 0, *data.Pricing.SpotDuration)
	require.Empty(t, data.Pricing.UnsupportedReason)
	d.SystemDisk = &systemDisk{Category: tea.String("cloud_essd"), Size: tea.Int32(80), Iops: tea.Int32(10000)}
	data, err = p.GetData(context.Background(), "lt-test", tea.Int64(3))
	require.NoError(t, err)
	require.Empty(t, data.Pricing.UnsupportedReason, "reported total IOPS is not provisioned IOPS")
	d.SystemDisk = &systemDisk{ProvisionedIops: tea.Int64(100)}
	data, err = p.GetData(context.Background(), "lt-test", tea.Int64(3))
	require.NoError(t, err)
	require.Contains(t, data.Pricing.UnsupportedReason, "system disk")
	d.SystemDisk = nil
	d.DataDisks = &diskList{DataDisk: []*templateDisk{{Category: tea.String("cloud_auto"), Size: tea.Int32(100), BurstingEnabled: tea.Bool(true)}}}
	data, err = p.GetData(context.Background(), "lt-test", tea.Int64(3))
	require.NoError(t, err)
	require.Contains(t, data.Pricing.UnsupportedReason, "data disk")
}
