package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestUnavailableOfferingIsolationAndExpiry(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	cache := NewUnavailableOfferingsCacheWithClock(time.Minute, clk)
	cache.MarkUnavailable("ecs.g7.large", "zone-a", "spot")
	require.False(t, cache.IsAvailable("ecs.g7.large", "zone-a", "spot"))
	for _, key := range [][3]string{{"ecs.g7.large", "zone-a", "on-demand"}, {"ecs.g7.large", "zone-b", "spot"}, {"ecs.g8.large", "zone-a", "spot"}} {
		require.True(t, cache.IsAvailable(key[0], key[1], key[2]))
	}
	clk.Step(time.Minute)
	require.True(t, cache.IsAvailable("ecs.g7.large", "zone-a", "spot"))
	cache.MarkUnavailable("ecs.g7.large", "zone-a", "spot")
	require.False(t, cache.IsAvailable("ecs.g7.large", "zone-a", "spot"))
	clk.Step(time.Minute)
	cache.MarkUnavailable("ecs.g8.large", "zone-b", "spot")
	require.Equal(t, 1, cache.Count(), "marks purge stale entries even when offerings are never read again")
}
