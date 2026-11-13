//go:build live

// the live check fetches a real ecosystem domain's stellar.toml through the
// production guard and resolver. it needs the internet, so it runs only with
// -tags live, from the scheduled workflow stellartoml-live.yml, and never
// gates ci.
package domaincheck_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/netguard"
)

// liveDomain publishes a sep-1 file for the public network (checked
// 2026-11-12: 200, about 1.8 KB, no redirect).
const liveDomain = "stellar.org"

func TestLiveStellarToml(t *testing.T) {
	g, err := netguard.New(net.DefaultResolver)
	require.NoError(t, err)
	st := domaincheck.FetchStellarToml(t.Context(), g, liveDomain)
	require.Equal(t, domaincheck.TomlFound, st.Status, "error %v, http %v", st.Error, st.HTTPStatus)
	require.NotNil(t, st.Network)
	require.Equal(t, "public", *st.Network)
	require.Positive(t, *st.Bytes)
	t.Logf("%s: version %v, accounts %d/%d valid, signing key %t/%t, currencies %d, org_url %v",
		liveDomain, deref(st.Version), st.AccountsValid, st.AccountsCount, st.SigningKeyPresent, st.SigningKeyValid,
		st.CurrenciesTotal, deref(st.OrgURL))
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}
