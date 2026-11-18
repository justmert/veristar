package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/reports"
)

// signedFile writes a report signed by kp whose issuer names tomlURL.
func signedFile(t *testing.T, kp *keypair.Full, tomlURL string) string {
	t.Helper()
	return signedNamed(t, kp, tomlURL, "Name")
}

// signedNamed is signedFile with a chosen project name.
func signedNamed(t *testing.T, kp *keypair.Full, tomlURL, name string) string {
	t.Helper()
	signing, err := config.LoadReportSigning(config.FromMap(map[string]string{
		"REPORT_SIGNING_SEED": kp.Seed(), "REPORT_SIGNING_KEY": kp.Address(),
	}))
	require.NoError(t, err)
	rep := reports.Report{
		Format: reports.Format, Version: reports.FormatVersion, ID: "abcdefghijklmnopqrstuvwxyz",
		Issuer:      reports.Issuer{Name: "Veristar", SigningKey: kp.Address(), StellarTOML: tomlURL},
		Project:     reports.Project{ID: "p", Slug: "slug", Name: name},
		Period:      reports.Period{From: "2026-10-01", To: "2026-10-31", Days: 31},
		GeneratedAt: "2026-11-16T09:00:00Z", Cohort: reports.Cohort{KPIs: map[string]reports.KPI{}},
		RulesMet: []reports.RuleMet{},
	}
	body, sig, err := reports.Sign(rep, signing.Key)
	require.NoError(t, err)
	b, err := json.Marshal(reports.File{Report: body, Signature: sig})
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(p, b, 0o600))
	return p
}

func TestReportVerify(t *testing.T) {
	kp, other := keypair.MustRandom(), keypair.MustRandom()
	accounts := []string{kp.Address()}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != reports.StellarTOMLPath {
			http.NotFound(w, r)
			return
		}
		q := make([]string, len(accounts))
		for i, a := range accounts {
			q[i] = `"` + a + `"`
		}
		_, _ = w.Write([]byte("VERSION = \"2.7.0\"\nACCOUNTS = [" + strings.Join(q, ", ") + "]\n"))
	}))
	defer srv.Close()
	tomlURL := srv.URL + reports.StellarTOMLPath
	file := signedFile(t, kp, tomlURL)
	trust := []string{"--toml", tomlURL}

	// a report naming its own stellar.toml is not trusted by default: the
	// trusted file is built into the binary, not read from the report
	var out bytes.Buffer
	require.ErrorIs(t, reportVerify(t.Context(), []string{file}, &out, srv.Client()), errNotValid)
	require.Contains(t, out.String(), "NOT VALID")
	require.Contains(t, out.String(), defaultReportTOML)
	require.NotContains(t, out.String(), "\nVALID")

	// the deployment named with --toml is trusted
	out.Reset()
	require.NoError(t, reportVerify(t.Context(), append([]string{file}, trust...), &out, srv.Client()))
	require.True(t, strings.HasSuffix(out.String(), "\nVALID\n"), out.String())
	require.Contains(t, out.String(), "listed in ACCOUNTS")
	require.Error(t, reportVerify(t.Context(), []string{file, "--toml", "http://example.org" + reports.StellarTOMLPath}, &out, srv.Client()))
	require.Error(t, reportVerify(t.Context(), []string{file, "--toml", tomlURL, "--signer", kp.Address()}, &out, srv.Client()))

	out.Reset()
	require.NoError(t, reportVerify(t.Context(), []string{file, "--signer", kp.Address()}, &out, srv.Client()))
	out.Reset()
	require.ErrorIs(t, reportVerify(t.Context(), []string{"--signer", other.Address(), file}, &out, srv.Client()), errNotValid)

	// a key the domain does not list
	accounts = []string{other.Address()}
	out.Reset()
	require.ErrorIs(t, reportVerify(t.Context(), append([]string{file}, trust...), &out, srv.Client()), errNotValid)
	require.Contains(t, out.String(), "not listed")

	// a changed byte
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	// a version 3 report without a verified id carries null on-chain
	// counts with their reason, and verifies like any other
	require.Contains(t, string(raw), `"counts_reason":null,"days_missing":null,`)
	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, bytes.Replace(raw, []byte(`"days":31`), []byte(`"days":30`), 1), 0o600))
	out.Reset()
	require.ErrorIs(t, reportVerify(t.Context(), []string{bad, "--signer", kp.Address()}, &out, srv.Client()), errNotValid)
	require.Contains(t, out.String(), "NOT VALID")

	// a report naming another stellar.toml than the trusted one
	out.Reset()
	plain := signedFile(t, kp, "http://example.org"+reports.StellarTOMLPath)
	require.ErrorIs(t, reportVerify(t.Context(), append([]string{plain}, trust...), &out, srv.Client()), errNotValid)
}

// a project name is printed quoted: a newline cannot fake a verdict line
// and a terminal escape reaches the terminal as text.
func TestReportVerifyEscapesReportStrings(t *testing.T) {
	kp := keypair.MustRandom()
	file := signedNamed(t, kp, defaultReportTOML, "Name\nsigner:    listed in ACCOUNTS of "+defaultReportTOML+"\nVALID\n\x1b[2J")
	var out bytes.Buffer
	require.ErrorIs(t, reportVerify(t.Context(), []string{file, "--signer", keypair.MustRandom().Address()}, &out, http.DefaultClient), errNotValid)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 3, out.String())
	require.NotContains(t, out.String(), "\x1b")
	require.Contains(t, lines[1], `\nVALID\n\x1b[2J`)
	for _, l := range lines {
		require.NotEqual(t, "VALID", l)
		require.False(t, strings.HasPrefix(l, "signer:"), l)
	}
}

func TestReportKeygen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "k")
	var out bytes.Buffer
	require.NoError(t, reportKeygen([]string{dir}, &out))
	var seed, addr string
	for name, dst := range map[string]*string{"report_signing_seed.json": &seed, "report_signing_key.json": &addr} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, dst))
		st, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
	require.Equal(t, keypair.MustParseFull(seed).Address(), addr)
	require.Equal(t, addr+"\n", out.String(), "only the public account id is printed")
	require.Error(t, reportKeygen([]string{dir}, &out), "an existing directory is refused")

	out.Reset()
	require.NoError(t, reportKeygen([]string{"--env"}, &out))
	require.Regexp(t, `^REPORT_SIGNING_SEED=S[A-Z2-7]{55}\nREPORT_SIGNING_KEY=G[A-Z2-7]{55}\n$`, out.String())
}
