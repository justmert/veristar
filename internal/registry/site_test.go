package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/registry"
)

// utm collection travels from cp.sites to the gateway in the registry entry.
// an entry without the field, as every entry written before it existed, must
// decode as off, and off is not written at all, so old gateways and new
// entries agree on the default.
func TestSiteCollectUTM(t *testing.T) {
	s := site(registry.StatusActive)

	raw, err := json.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "collect_utm")
	got, err := registry.DecodeSite(s.ID, raw)
	require.NoError(t, err)
	require.False(t, got.CollectUTM)

	s.CollectUTM = true
	raw, err = json.Marshal(s)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"collect_utm":true`)
	got, err = registry.DecodeSite(s.ID, raw)
	require.NoError(t, err)
	require.True(t, got.CollectUTM)
}

// the site's scale travels the same way: absent means no baseline, and only
// zero or a power of two within the bound is accepted.
func TestSiteScale(t *testing.T) {
	s := site(registry.StatusActive)
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "scale")

	s.Scale = 4096
	raw, err = json.Marshal(s)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"scale":4096`)
	got, err := registry.DecodeSite(s.ID, raw)
	require.NoError(t, err)
	require.Equal(t, 4096, got.Scale)

	for _, bad := range []int{-1, 3, 1000, registry.MaxScale * 2} {
		s.Scale = bad
		require.Error(t, s.Validate(), "scale %d", bad)
	}
}
