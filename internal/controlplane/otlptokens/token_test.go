package otlptokens

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/registry"
)

func TestGenerate(t *testing.T) {
	re := regexp.MustCompile(`^sa_otlp_[0-9A-Za-z]{40}$`)
	seen := map[string]bool{}
	counts := map[rune]int{}
	for range 2000 {
		g, err := Generate()
		require.NoError(t, err)
		require.Regexp(t, re, g.Plain)
		require.Equal(t, g.Plain[:12], g.Display)
		require.Equal(t, sha256.Sum256([]byte(g.Plain)), g.Hash)
		require.Equal(t, registry.HashOTLPToken(g.Plain), g.HexHash())
		require.False(t, seen[g.Plain])
		seen[g.Plain] = true
		for _, r := range strings.TrimPrefix(g.Plain, Prefix) {
			counts[r]++
		}
	}
	// 80,000 draws over 62 symbols: every symbol shows up, none dominates
	require.Len(t, counts, 62)
	for r, n := range counts {
		require.Greater(t, n, 1000, string(r))
		require.Less(t, n, 1600, string(r))
	}
}

func TestRegistryStatus(t *testing.T) {
	for _, c := range []struct{ token, tenant, want string }{
		{"active", "active", "active"},
		{"active", "suspended", "suspended"},
		{"active", "pending", "pending"},
		{"active", "rejected", "rejected"},
		{"active", "removed", "removed"},
		{"revoked", "active", "revoked"},
		{"revoked", "suspended", "revoked"},
		{"revoked", "removed", "revoked"},
	} {
		require.Equal(t, c.want, RegistryStatus(c.token, c.tenant), "%s/%s", c.token, c.tenant)
	}
	h := sha256.Sum256([]byte("x"))
	tenant, token := uuid.New(), uuid.New()
	e, err := Entry(tenant, token, h[:], "active", "active")
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(h[:]), e.Hash)
	require.NoError(t, e.Validate())
	_, err = Entry(tenant, token, h[:31], "active", "active")
	require.Error(t, err)
}
