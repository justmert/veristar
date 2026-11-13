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
