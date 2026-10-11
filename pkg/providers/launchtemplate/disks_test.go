package launchtemplate

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"testing"
)

type versionSet = ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSet
type versionData = ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateData
type systemDisk = ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateDataSystemDisk
type diskList = ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateDataDataDisks
type templateDisk = ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSetsLaunchTemplateVersionSetLaunchTemplateDataDataDisksDataDisk
type diskTemplateClient struct {
	MockECSClient
	rows    []*versionSet
	request *ecs.DescribeLaunchTemplateVersionsRequest
}

func (c *diskTemplateClient) DescribeLaunchTemplateVersions(_ context.Context, r *ecs.DescribeLaunchTemplateVersionsRequest) (*ecs.DescribeLaunchTemplateVersionsResponse, error) {
	c.request = r
	return &ecs.DescribeLaunchTemplateVersionsResponse{Body: &ecs.DescribeLaunchTemplateVersionsResponseBody{LaunchTemplateVersionSets: &ecs.DescribeLaunchTemplateVersionsResponseBodyLaunchTemplateVersionSets{LaunchTemplateVersionSet: c.rows}}}, nil
}
func TestTemplateDiskResolutionAndVersionIdentity(t *testing.T) {
	data := &versionData{ImageId: tea.String("m-linux"), SystemDisk: &systemDisk{Category: tea.String("cloud_essd"), Size: tea.Int32(80), Encrypted: tea.String("true"), PerformanceLevel: tea.String("PL1")}, DataDisks: &diskList{DataDisk: []*templateDisk{{Category: tea.String("cloud_ssd"), Size: tea.Int32(100), Device: tea.String("/dev/xvdb"), DeleteWithInstance: tea.Bool(false)}}}}
	c := &diskTemplateClient{rows: []*versionSet{{LaunchTemplateId: tea.String("lt-test"), VersionNumber: tea.Int64(3), LaunchTemplateData: data}}}
	p := NewProvider("cn-hangzhou", c)
	result, err := p.GetData(context.Background(), "lt-test", nil)
	require.NoError(t, err)
	require.True(t, tea.BoolValue(c.request.DefaultVersion))
	require.EqualValues(t, 3, result.Version)
	require.EqualValues(t, 80, result.Disks.SystemDisk.Size)
	require.True(t, *result.Disks.SystemDisk.Encrypted)
	require.Equal(t, "PL1", result.Disks.SystemDisk.PerformanceLevel)
	require.Len(t, result.Disks.DataDisks, 1)
	require.EqualValues(t, 100, result.Disks.DataDisks[0].Size)
	require.False(t, *result.Disks.DataDisks[0].DeleteWithInstance)
	_, err = p.GetData(context.Background(), "lt-test", tea.Int64(2))
	require.ErrorContains(t, err, "mismatch")
	c.rows[0].LaunchTemplateId = tea.String("lt-foreign")
	_, err = p.GetData(context.Background(), "lt-test", nil)
	require.ErrorContains(t, err, "identity")
	c.rows = []*versionSet{nil}
	_, err = p.GetData(context.Background(), "lt-test", nil)
	require.Error(t, err)
}
func TestInvalidTemplateDisksFailDuringRead(t *testing.T) {
	for _, disk := range []*templateDisk{nil, {Category: tea.String("cloud_essd")}, {Category: tea.String("cloud_essd_xc1"), Size: tea.Int32(100)}, {Category: tea.String("cloud_ssd"), Size: tea.Int32(100), Encrypted: tea.String("unknown")}} {
		_, err := resolveTemplateDisks(&versionData{DataDisks: &diskList{DataDisk: []*templateDisk{disk}}})
		require.Error(t, err)
	}
}
func TestResolvedTemplateSpecAndHashAgree(t *testing.T) {
	disks, err := v1alpha1.NormalizeDisks(v1alpha1.ECSNodeClassSpec{DataDisks: []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 80}}})
	require.NoError(t, err)
	nc := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{LaunchTemplateID: tea.String("lt-test")}, Status: v1alpha1.ECSNodeClassStatus{LaunchTemplate: &v1alpha1.ResolvedLaunchTemplate{ID: "lt-test", Version: 3, Disks: disks}}}
	spec, err := nc.LaunchSpec()
	require.NoError(t, err)
	require.EqualValues(t, 3, *spec.LaunchTemplateVersion)
	require.EqualValues(t, 80, spec.DataDisks[0].Size)
	hash, err := nc.Hash()
	require.NoError(t, err)
	effective := nc.DeepCopy()
	effective.Spec = spec
	effectiveHash, err := effective.Hash()
	require.NoError(t, err)
	require.Equal(t, hash, effectiveHash)
	nc.Status.LaunchTemplate.Version = 4
	changed, err := nc.Hash()
	require.NoError(t, err)
	require.NotEqual(t, hash, changed)
	require.Nil(t, nc.Spec.LaunchTemplateVersion)
	require.Empty(t, nc.Spec.DataDisks)
}
