package sites

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
)

func TestValidDomain(t *testing.T) {
	for _, d := range []string{"example.org", "a.b-c.example.org", "xn--bcher-kva.example", "123.example"} {
		require.True(t, ValidDomain(d), d)
	}
	for _, d := range []string{"", "localhost", "Example.org", "example.org.", "-a.org", "a-.org", "a..org",
		"10.0.0.1", "1.2.3.4", "example.org:80", "bücher.example", strings.Repeat("a", 64) + ".org"} {
		require.False(t, ValidDomain(d), d)
	}
}

func TestCheckOrigins(t *testing.T) {
	var f tenants.Fields
	CheckOrigins(&f, []string{"https://example.org", "http://localhost:3000", "http://127.0.0.1", "https://x.example.org:8443"}, "example.org")
	require.NoError(t, f.Err())
	for o, code := range map[string]string{
		"https://example.org/":       "not_bare_origin",
		"https://example.org:443":    "not_bare_origin",
		"https://example.org:0":      "not_bare_origin",
		"http://example.org":         "not_bare_origin",
		"https://user@example.org":   "not_bare_origin",
		"https://EXAMPLE.org":        "not_bare_origin",
		"example.org":                "not_bare_origin",
		"https://notexample.org":     "origin_outside_domain",
		"https://example.org.evil.x": "origin_outside_domain",
		"https://localhost":          "origin_outside_domain",
	} {
		var f tenants.Fields
		CheckOrigins(&f, []string{o}, "example.org")
		e, ok := httpx.AsError(f.Err())
		require.True(t, ok, o)
		require.Equal(t, code, e.Fields["origins[0]"], o)
	}
}
