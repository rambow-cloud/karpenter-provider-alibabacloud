package instancetype

import (
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLocalDiskMetadataPreservesUnknownAndZero(t *testing.T) {
	raw := &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{InstanceTypeId: tea.String("ecs-test"), CpuCoreCount: tea.Int32(2), MemorySize: tea.Float32(8)}
	p := &Provider{}
	require.False(t, p.convertECSInstanceType(raw, nil).LocalDiskKnown)
	raw.LocalStorageAmount = tea.Int32(0)
	require.True(t, p.convertECSInstanceType(raw, nil).LocalDiskKnown)
	raw.LocalStorageAmount = tea.Int32(2)
	raw.LocalStorageCapacity = tea.Int64(100)
	result := p.convertECSInstanceType(raw, nil)
	require.EqualValues(t, 2, result.LocalDiskCount)
	require.EqualValues(t, 100, result.LocalDiskCapacityGiB)
}
