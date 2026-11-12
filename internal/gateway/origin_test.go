package gateway

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/registry"
)

func TestCheckOrigin(t *testing.T) {
	site := registry.Site{
		ID:     uuid.NewString(),
		Status: registry.StatusActive,
		Origins: []string{
			"https://example.org",
			"https://*.apps.example.net",
			"http://localhost:5173",
			"https://Shop.Example.com:443",
			"http://[::1]:8080",
			"https://*.com",        // invalid pattern: matches nothing
			"https://bad.example/", // invalid pattern: has a path
		},
	}
	allow := []string{
		"https://example.org",
		"https://EXAMPLE.org",
		"https://example.org:443",
		"https://a.apps.example.net",
		"https://a.b.apps.example.net",
		"http://localhost:5173",
		"https://shop.example.com",
		"http://[::1]:8080",
	}
	for _, o := range allow {
		require.NoError(t, CheckOrigin(site, o), o)
	}
	refuse := map[string]Reason{
		"":                                         ReasonOriginMissing,
		"null":                                     ReasonOrigin,
		"http://example.org":                       ReasonOrigin, // scheme differs
		"https://example.org:8443":                 ReasonOrigin, // port differs
		"https://www.example.org":                  ReasonOrigin, // example.org is registered without a wildcard
		"https://apps.example.net":                 ReasonOrigin, // a wildcard covers subdomains only
		"https://evilapps.example.net":             ReasonOrigin, // a suffix is not a subdomain
		"https://a.apps.example.net.evil.org":      ReasonOrigin,
		"http://a.apps.example.net":                ReasonOrigin,
		"https://x.com":                            ReasonOrigin, // the invalid *.com pattern matches nothing
		"https://bad.example":                      ReasonOrigin,
		"https://example.org/":                     ReasonOrigin, // browsers never send a path
		"https://user@example.org":                 ReasonOrigin,
		"https://*.apps.example.net":               ReasonOrigin, // a header cannot carry a wildcard
		"http://localhost:5174":                    ReasonOrigin,
		"http://localhost":                         ReasonOrigin,
		"file://":                                  ReasonOrigin,
		"chrome-extension://abcdef":                ReasonOrigin,
		"https://example.org?x=1":                  ReasonOrigin,
		"https://example.org#":                     ReasonOrigin,
		"https://example.org:":                     ReasonOrigin,
		"https://example.org, https://example.org": ReasonOrigin,
	}
	for o, want := range refuse {
		require.Equal(t, want, pfReason(t, CheckOrigin(site, o)), o)
	}
	require.Equal(t, ReasonOrigin, pfReason(t, CheckOrigin(registry.Site{}, "https://example.org")))
}

func TestValidOriginPattern(t *testing.T) {
	for _, p := range []string{
		"https://example.org", "http://localhost:3000", "https://*.example.org", "https://*.a.example.org:8443",
		"http://127.0.0.1:8080", "http://[::1]", "https://xn--bcher-kva.example",
	} {
		require.NoError(t, ValidOriginPattern(p), p)
	}
	for _, p := range []string{
		"", "example.org", "*.example.org", "https://", "https://*.org", "https://a.*.example.org",
		"https://*example.org", "https://example.org/", "https://example.org/path", "ftp://example.org",
		"https://*.127.0.0.1", "https://bücher.example", "https://-a.example", "https://a_b.example",
		"https://example.org:", "https://u:p@example.org",
	} {
		require.Error(t, ValidOriginPattern(p), p)
	}
}
