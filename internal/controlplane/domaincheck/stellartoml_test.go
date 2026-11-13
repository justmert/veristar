package domaincheck_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/netguard"
)

const (
	issuer   = "GBJVY34OWUI7LWLGUGYHEXPZF27SOUKPVOUULS55NGHCHLDSYQLVODR6"
	contract = "CDGIGIOWG5OESTIEH7OQEYHSDPAOYUO2ZSPWVO37SCONZUYEDN4L7FVM"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "stellartoml", name))
	require.NoError(t, err)
	return b
}

func TestParseValid(t *testing.T) {
	t.Parallel()
	b := fixture(t, "valid.toml")
	st := domaincheck.ParseStellarToml("stellar-verify.test", b)
	require.Equal(t, domaincheck.TomlFound, st.Status)
	require.Nil(t, st.Error)
	require.Equal(t, 200, *st.HTTPStatus)
	require.Equal(t, len(b), *st.Bytes)
	require.Equal(t, "2.7.0", *st.Version)
	require.Equal(t, "public", *st.Network)
	require.Equal(t, "Public Global Stellar Network ; September 2015", *st.NetworkPassphrase)
	require.Equal(t, 3, st.AccountsCount)
	require.Equal(t, 2, st.AccountsValid)
	require.True(t, st.SigningKeyPresent)
	require.True(t, st.SigningKeyValid)
	require.Equal(t, "https://stellar-verify.test", *st.OrgURL)
	require.True(t, *st.OrgURLSameDomain)
	require.Equal(t, 2, st.CurrenciesTotal)
	require.Equal(t, []domaincheck.Currency{
		{Code: "USDX", Issuer: ptr(issuer), Valid: true},
		{Code: "TKN", Contract: ptr(contract), Valid: true},
	}, st.Currencies)
}

func ptr(s string) *string { return &s }

func TestParseSEP41(t *testing.T) {
	t.Parallel()
	st := domaincheck.ParseStellarToml("stellar-verify.test", fixture(t, "sep41-contract.toml"))
	require.Equal(t, domaincheck.TomlFound, st.Status)
	require.Equal(t, "testnet", *st.Network)
	require.False(t, *st.OrgURLSameDomain, "www. is another host")
	require.Nil(t, st.Version)
	require.False(t, st.SigningKeyPresent)
	require.Len(t, st.Currencies, 3)
	require.Equal(t, "SOROBANTOKEN", st.Currencies[0].Code, "code is cut to 12 characters")
	require.True(t, st.Currencies[0].Valid)
	require.False(t, st.Currencies[1].Valid, "issuer and contract together")
	require.False(t, st.Currencies[2].Valid, "a g key as contract")
}

func TestParseWrongNetwork(t *testing.T) {
	t.Parallel()
	st := domaincheck.ParseStellarToml("stellar-verify.test", fixture(t, "wrong-network.toml"))
	require.Equal(t, domaincheck.TomlFound, st.Status)
	require.Equal(t, "other", *st.Network)
	require.Nil(t, st.OrgURL, "top-level ORG_URL is not the sep-1 location")
	require.True(t, st.SigningKeyPresent)
	require.False(t, st.SigningKeyValid)
	require.Equal(t, []domaincheck.Currency{}, st.Currencies)
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	for file, code := range map[string]string{
		"malformed.toml":  domaincheck.CodeTomlSyntax,
		"missing.html":    domaincheck.CodeTomlSyntax,
		"wrong-type.toml": domaincheck.CodeTomlType,
	} {
		st := domaincheck.ParseStellarToml("stellar-verify.test", fixture(t, file))
		require.Equal(t, domaincheck.TomlInvalid, st.Status, file)
		require.Equal(t, code, *st.Error, file)
		require.Nil(t, st.Network)
		require.Zero(t, st.AccountsCount)
		require.Equal(t, []domaincheck.Currency{}, st.Currencies)
	}
}

func TestFetchStellarToml(t *testing.T) {
	t.Parallel()
	valid := fixture(t, "valid.toml")
	// exactly at the cap: the valid file padded with a comment
	pad := domaincheck.StellarTomlMaxSize - len(valid) - 2
	exact := append(append([]byte{}, valid...), []byte("#"+strings.Repeat("x", pad)+"\n")...)
	require.Len(t, exact, domaincheck.StellarTomlMaxSize)
	byHost := map[string][]byte{
		"found.stellar-verify.test": valid,
		"exact.stellar-verify.test": exact,
		"big.stellar-verify.test":   append(append([]byte{}, exact...), '\n'),
	}
	h := newNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "hop.stellar-verify.test" {
			http.Redirect(w, r, "https://found.stellar-verify.test"+domaincheck.StellarTomlPath, http.StatusMovedPermanently)
			return
		}
		b, ok := byHost[r.Host]
		if !ok || r.URL.Path != domaincheck.StellarTomlPath {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fixture(t, "missing.html"))
			return
		}
		_, _ = w.Write(b)
	}), "found", "exact", "big", "absent", "hop")
	g := h.guard(t, true)

	st := domaincheck.FetchStellarToml(t.Context(), g, "found.stellar-verify.test")
	require.Equal(t, domaincheck.TomlFound, st.Status)
	require.Equal(t, 2, st.CurrenciesTotal)

	st = domaincheck.FetchStellarToml(t.Context(), g, "exact.stellar-verify.test")
	require.Equal(t, domaincheck.TomlFound, st.Status)
	require.Equal(t, domaincheck.StellarTomlMaxSize, *st.Bytes)

	st = domaincheck.FetchStellarToml(t.Context(), g, "big.stellar-verify.test")
	require.Equal(t, domaincheck.TomlTooLarge, st.Status)
	require.Nil(t, st.Bytes)

	st = domaincheck.FetchStellarToml(t.Context(), g, "absent.stellar-verify.test")
	require.Equal(t, domaincheck.TomlNotFound, st.Status)
	require.Equal(t, 404, *st.HTTPStatus)
	require.Nil(t, st.Error)

	st = domaincheck.FetchStellarToml(t.Context(), g, "hop.stellar-verify.test")
	require.Equal(t, domaincheck.TomlRefused, st.Status)
	require.Equal(t, netguard.CodeRedirectRefused, *st.Error)
	require.Nil(t, st.HTTPStatus)

	// the same server is loopback for a guard without the test allowance
	st = domaincheck.FetchStellarToml(t.Context(), h.guard(t, false), "found.stellar-verify.test")
	require.Equal(t, domaincheck.TomlRefused, st.Status)
	require.Equal(t, netguard.CodeRefusedAddress, *st.Error)

	st = domaincheck.FetchStellarToml(t.Context(), g, "unknown.stellar-verify.test")
	require.Equal(t, domaincheck.TomlError, st.Status)
	require.Equal(t, netguard.CodeFetchError, *st.Error)
}
