package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/gateway"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// syncBuf is a log sink the daemon goroutine and the test share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"bogus"}, config.FromMap(nil), &out, &errOut))
	require.Contains(t, errOut.String(), "usage: jobs")
	require.Equal(t, 0, run(t.Context(), []string{"help"}, config.FromMap(nil), &out, &errOut))
	require.Contains(t, out.String(), "run <task>")
}

func TestMissingConfigNamesEveryKey(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"daemon"}, config.FromMap(map[string]string{}), &out, &errOut))
	for _, k := range []string{
		"NATS_URL", "NATS_REPLICAS", "PLATFORM_DB_HOST", "PLATFORM_DB_JOBS_PASSWORD", "PLATFORM_DB_CP_SYSTEM_PASSWORD",
		"UMAMI_URL", "UMAMI_ADMIN_PASSWORD", "UMAMI_USER_PASSWORD_KEY", "JOBS_OPS_ADDR",
	} {
		require.Contains(t, errOut.String(), k)
	}
}

func password(t *testing.T) string {
	b := make([]byte, 24)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func get(t *testing.T, url string) (int, string) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestDaemonAndRun runs the real command against nats with the production
// authorization (each service as its own user) and postgres with the real
// migrations and roles.
func TestDaemonAndRun(t *testing.T) {
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		creds[u.Name] = password(t)
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	conn := func(service string) jetstream.JetStream {
		cfg, err := config.LoadNATS(config.FromMap(map[string]string{
			"NATS_URL": srv.URL, "NATS_REPLICAS": "1", "NATS_USER": service, "NATS_PASSWORD": creds[service],
		}))
		require.NoError(t, err)
		opts, err := natsx.Options(cfg, service, quiet())
		require.NoError(t, err)
		nc, err := nats.Connect(srv.URL, opts...)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		js, err := jetstream.New(nc)
		require.NoError(t, err)
		return js
	}
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), conn(natsx.ServicePlatformctl), spec)
	require.NoError(t, err)

	site, tenant := uuid.New(), uuid.New()
	pub, err := registry.NewPublisher(t.Context(), conn(natsx.ServiceControlPlane))
	require.NoError(t, err)
	require.NoError(t, pub.PutSite(t.Context(), registry.Site{
		ID: site.String(), TenantID: tenant.String(), Status: registry.StatusActive,
		Origins: []string{"https://example.org"}, QuarantineWebsiteID: uuid.NewString(),
	}))
	stats, err := gateway.NewStats(gateway.StatsConfig{
		Node: "edge-1", JS: conn(natsx.ServiceGateway), Log: quiet(), Registerer: prometheus.NewRegistry(),
	})
	require.NoError(t, err)

	pg := jobstest.Postgres(t)
	jobsDB := jobstest.JobsDB(t, pg)
	envMap := pg.EnvFor("platform")
	envMap["NATS_URL"] = srv.URL
	envMap["NATS_REPLICAS"] = "1"
	envMap["NATS_USER"] = natsx.ServiceJobs
	envMap["NATS_PASSWORD"] = creds[natsx.ServiceJobs]
	ops := freeAddr(t)
	envMap["JOBS_OPS_ADDR"] = ops
	// reconcile and umami-rotate run against a private umami
	um := umamictr.Start(t)
	envMap["UMAMI_URL"] = um.URL.String()
	envMap["UMAMI_ADMIN_PASSWORD"] = um.AdminPassword
	pwKey := make([]byte, 32)
	_, err = rand.Read(pwKey)
	require.NoError(t, err)
	envMap["UMAMI_USER_PASSWORD_KEY"] = base64.StdEncoding.EncodeToString(pwKey)
	key := site.String() + "/"

	// the daemon leads, serves its ops endpoints and drains as stats arrive
	logs := &syncBuf{}
	dctx, stopDaemon := context.WithCancel(t.Context())
	defer stopDaemon()
	exit := make(chan int, 1)
	go func() { exit <- run(dctx, []string{"daemon"}, config.FromMap(envMap), io.Discard, logs) }()
	require.Eventually(t, func() bool {
		code, _ := get(t, "http://"+ops+"/readyz")
		return code == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "daemon never ready: %s", logs)
	code, body := get(t, "http://"+ops+"/healthz")
	require.Equal(t, http.StatusOK, code, body)
	require.Eventually(t, func() bool {
		_, m := get(t, "http://"+ops+"/metrics")
		return strings.Contains(m, "\njobs_leader 1\n")
	}, 10*time.Second, 100*time.Millisecond)

	for range 5 {
		require.NoError(t, stats.Accepted(site.String()))
	}
	require.NoError(t, stats.Rejected(site.String(), gateway.ReasonOrigin))
	require.NoError(t, stats.Flush(t.Context()))
	require.Eventually(t, func() bool {
		tot := jobstest.Totals(t, jobsDB)
		return tot[key+"accepted"] == 5 && tot[key+"rejected_origin"] == 1
	}, 15*time.Second, 100*time.Millisecond, "daemon did not apply the stats: %s", logs)
	_, m := get(t, "http://"+ops+"/metrics")
	require.Contains(t, m, "jobs_edgestats_applied_total 1")
	require.Contains(t, m, "\njobs_edgestats_dropped_total 0\n", "the dropped-stats series exists before any drop")
	require.Contains(t, m, "\njobs_statsdedup_pruned_total 0\n")

	// a one-off run is refused while the daemon leads
	var onceErr syncBuf
	require.Equal(t, 1, run(t.Context(), []string{"run", "edgestats"}, config.FromMap(envMap), io.Discard, &onceErr))
	require.Contains(t, onceErr.String(), "lock is held")

	stopDaemon()
	select {
	case code := <-exit:
		require.Equal(t, 0, code, logs.String())
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not stop")
	}

	// with the daemon gone and its lock released, jobs run drains the queue
	require.NoError(t, stats.Quarantined(site.String()))
	require.NoError(t, stats.Accepted(site.String()))
	require.NoError(t, stats.Flush(t.Context()))
	var runLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "edgestats"}, config.FromMap(envMap), io.Discard, &runLog), runLog.String())
	require.Contains(t, runLog.String(), `"messages":1`)
	tot := jobstest.Totals(t, jobsDB)
	require.Equal(t, int64(6), tot[key+"accepted"])
	require.Equal(t, int64(1), tot[key+"quarantined"])
	require.Equal(t, int64(1), tot[key+"rejected_origin"])

	var pruneLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "statsdedup-prune"}, config.FromMap(envMap), io.Discard, &pruneLog), pruneLog.String())
	require.Contains(t, pruneLog.String(), `"deleted":0`, "fresh ids are kept")
	require.Len(t, jobstest.DedupIDs(t, jobsDB), 2)

	// the control plane tasks run once too: no tenants, no platform users
	var reconcileLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "reconcile"}, config.FromMap(envMap), io.Discard, &reconcileLog), reconcileLog.String())
	var rotateLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "umami-rotate"}, config.FromMap(envMap), io.Discard, &rotateLog), rotateLog.String())

	var sessionLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "session-prune"}, config.FromMap(envMap), io.Discard, &sessionLog), sessionLog.String())
	require.Contains(t, sessionLog.String(), "expired sessions pruned")

	// no sites yet: the run checks nothing and succeeds
	var verifyLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "verify-domains"}, config.FromMap(envMap), io.Discard, &verifyLog), verifyLog.String())

	var unknown syncBuf
	require.Equal(t, 1, run(t.Context(), []string{"run", "nope"}, config.FromMap(envMap), io.Discard, &unknown))
	require.Contains(t, unknown.String(), "unknown task")
	all := logs.String() + runLog.String() + reconcileLog.String() + rotateLog.String()
	require.NotContains(t, all, creds[natsx.ServiceJobs], "no secret in the logs")
	require.NotContains(t, all, um.AdminPassword, "no secret in the logs")
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }
