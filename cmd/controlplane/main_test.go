package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// a control plane without configuration exits before connecting and names
// every missing key in one error.
func TestServeNamesEveryMissingKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, config.FromMap(nil), &stdout, &stderr)
	require.Equal(t, 1, code)
	for _, k := range []string{
		"CONTROLPLANE_ADDR", "CONTROLPLANE_OPS_ADDR", "CONSOLE_ORIGIN", "INGEST_ORIGIN",
		"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_REDIRECT_URL", "OAUTH_STATE_KEY",
		"OPERATOR_GITHUB_IDS", "REPORT_SIGNING_SEED", "REPORT_SIGNING_KEY", "UMAMI_URL", "UMAMI_ADMIN_PASSWORD", "UMAMI_USER_PASSWORD_KEY",
		"TRACKER_RELEASE_DIR", "NATS_URL", "PLATFORM_DB_HOST", "PLATFORM_DB_CP_SYSTEM_PASSWORD",
		"PLATFORM_DB_CP_TENANT_PASSWORD", "PLATFORM_DB_CP_OPERATOR_PASSWORD",
		"API_MINUTE_RETENTION_DAYS", "API_HOURLY_RETENTION_MONTHS",
	} {
		require.Contains(t, stderr.String(), k)
	}
	require.NotContains(t, stderr.String(), "PLATFORM_DB_JOBS_PASSWORD", "the control plane loads only its own roles")
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.Equal(t, 2, run(context.Background(), []string{"bogus"}, config.FromMap(nil), &stdout, &stderr))
	require.Equal(t, 0, run(context.Background(), []string{"version"}, config.FromMap(nil), &stdout, &stderr))
	require.NotZero(t, stdout.Len())
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// syncBuffer lets the test read the log while serve writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// the real binary entry point against real postgresql and nats: it starts,
// reports ready, serves the api with its conventions, and stops cleanly.
func TestServeStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	quiet := slog.New(slog.DiscardHandler)
	acfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, acfg, db.Migrations(), quiet)
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	ns := natsctr.StartSingle(t)
	nc, err := nats.Connect(ns.URL)
	require.NoError(t, err)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	nc.Close()

	addr, ops := freeAddr(t), freeAddr(t)
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	reportKey := keypair.MustRandom()
	values := pg.EnvFor("platform")
	for k, v := range map[string]string{
		"CONTROLPLANE_ADDR":           addr,
		"CONTROLPLANE_OPS_ADDR":       ops,
		"CONSOLE_ORIGIN":              "http://" + addr,
		"INGEST_ORIGIN":               "http://127.0.0.1:8080",
		"GITHUB_CLIENT_ID":            "Iv1.cmdtest",
		"GITHUB_CLIENT_SECRET":        "cmd-test-secret",
		"GITHUB_REDIRECT_URL":         "http://" + addr + "/api/auth/github/callback",
		"OAUTH_STATE_KEY":             key,
		"REPORT_SIGNING_SEED":         reportKey.Seed(),
		"REPORT_SIGNING_KEY":          reportKey.Address(),
		"OAUTH_FIXTURE_URL":           "http://127.0.0.1:1",
		"OPERATOR_GITHUB_IDS":         "1",
		"UMAMI_URL":                   "http://127.0.0.1:3000",
		"UMAMI_ADMIN_PASSWORD":        "0123456789abcdef",
		"UMAMI_USER_PASSWORD_KEY":     key,
		"TRACKER_RELEASE_DIR":         filepath.Join(natsctr.RepoRoot(t), "packages", "tracker", "releases"),
		"NATS_URL":                    ns.URL,
		"NATS_REPLICAS":               "1",
		"API_MINUTE_RETENTION_DAYS":   "30",
		"API_HOURLY_RETENTION_MONTHS": "36",
	} {
		values[k] = v
	}

	runCtx, stop := context.WithCancel(ctx)
	var stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(runCtx, nil, config.FromMap(values), io.Discard, &stderr) }()

	get := func(u string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(req)
	}
	require.Eventually(t, func() bool {
		res, err := get("http://" + ops + "/readyz")
		if err != nil {
			return false
		}
		_ = res.Body.Close()
		return res.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "not ready: %s", stderr.String())

	// every tenant-scoped route is mounted and needs a session
	for _, p := range []string{"/api/me", "/api/tenants", "/api/sites/" + "00000000-0000-7000-8000-000000000001" + "/snippet",
		"/api/tenants/00000000-0000-7000-8000-000000000002/web/summary", "/api/operator/tenants", "/api/sites/00000000-0000-7000-8000-000000000001/verification"} {
		res, err := get("http://" + addr + p)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, p)
	}

	res, err := get("http://" + addr + "/api/terms")
	require.NoError(t, err)
	var tm struct{ Version string }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&tm))
	_ = res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, terms.MustCurrent().Version, tm.Version)
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))

	// sign-in starts with a redirect to the identity provider of this build
	nr := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/auth/github/start", nil)
	require.NoError(t, err)
	res, err = nr.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.True(t, strings.HasPrefix(res.Header.Get("Location"), authorizePrefix), res.Header.Get("Location"))

	// the log carries no secret value
	for _, s := range []string{"cmd-test-secret", key, values["PLATFORM_DB_CP_SYSTEM_PASSWORD"]} {
		require.NotContains(t, stderr.String(), s)
	}

	stop()
	select {
	case code := <-done:
		require.Equal(t, 0, code, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("serve did not stop: %s", stderr.String())
	}
}
