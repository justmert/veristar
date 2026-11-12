package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBucket(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	b := newBucket(StartRatePerSecond, StartBurst, now)
	for i := range StartBurst {
		ok, _ := b.take(now)
		require.True(t, ok, i)
	}
	ok, wait := b.take(now)
	require.False(t, ok)
	require.Equal(t, 100*time.Millisecond, wait)
	// one tenth of a second gives one token back
	ok, _ = b.take(now.Add(100 * time.Millisecond))
	require.True(t, ok)
	ok, _ = b.take(now.Add(100 * time.Millisecond))
	require.False(t, ok)
	// a long pause refills to the burst, not beyond
	later := now.Add(time.Hour)
	for range StartBurst {
		ok, _ = b.take(later)
		require.True(t, ok)
	}
	ok, _ = b.take(later)
	require.False(t, ok)
}
