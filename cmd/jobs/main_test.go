package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
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
		"API_MINUTE_RETENTION_DAYS", "API_HOURLY_RETENTION_MONTHS",
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

	site, tenant, quarantine := uuid.New(), uuid.New(), uuid.New()
	pub, err := registry.NewPublisher(t.Context(), conn(natsx.ServiceControlPlane))
	require.NoError(t, err)
	require.NoError(t, pub.PutSite(t.Context(), registry.Site{
		ID: site.String(), TenantID: tenant.String(), Status: registry.StatusActive,
		Origins: []string{"https://example.org"}, QuarantineWebsiteID: quarantine.String(),
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
	// aggregate-web reads both websites of the registered site, and skips
	// nothing but removed tenants, so the tenant and its websites exist
	team, _, err := um.Admin.CreateTeam(t.Context(), "jobs-test", "")
	require.NoError(t, err)
	for _, id := range []uuid.UUID{site, quarantine} {
		_, err := um.Admin.CreateWebsite(t.Context(), umami.NewWebsite{ID: id.String(), Name: "jobs", Domain: "example.org", TeamID: team.ID})
		require.NoError(t, err)
	}
	su, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	_, err = su.Exec(t.Context(), `insert into cp.tenants (id, slug, name, status) values ($1, 'jobs-test', 'Jobs test', 'active')`, tenant)
	require.NoError(t, err)
	require.NoError(t, su.Close(t.Context()))
	envMap["UMAMI_URL"] = um.URL.String()
	envMap["UMAMI_ADMIN_PASSWORD"] = um.AdminPassword
	pwKey := make([]byte, 32)
	_, err = rand.Read(pwKey)
	require.NoError(t, err)
	envMap["UMAMI_USER_PASSWORD_KEY"] = base64.StdEncoding.EncodeToString(pwKey)
	envMap["API_MINUTE_RETENTION_DAYS"] = "30"
	envMap["API_HOURLY_RETENTION_MONTHS"] = "36"
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

	// the api tasks run once: partitions ahead exist, nothing is pending and
	// nothing has expired on a fresh database
	for _, task := range []string{"api-partition", "api-rollup", "api-retention"} {
		var l syncBuf
		require.Equal(t, 0, run(t.Context(), []string{"run", task}, config.FromMap(envMap), io.Discard, &l), "%s: %s", task, l.String())
	}
	// aggregate-web runs its regular pass, then a backfill of a range
	var aggLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "aggregate-web"}, config.FromMap(envMap), io.Discard, &aggLog), aggLog.String())
	today := time.Now().UTC().Truncate(24 * time.Hour)
	backfill := []string{"run", "aggregate-web", "--from", today.AddDate(0, 0, -9).Format(time.DateOnly), "--to", today.Format(time.DateOnly)}
	require.Equal(t, 0, run(t.Context(), backfill, config.FromMap(envMap), io.Discard, &aggLog), aggLog.String())
	require.Contains(t, aggLog.String(), `"days":10`)
	var aggRows int
	require.NoError(t, jobsDB.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from agg.web_daily where site_id = $1`, site).Scan(&aggRows)
	}))
	require.Equal(t, 20, aggRows, "ten days, two bands")
	// integrity grades the reread window from those aggregates; seven
	// events today are too few for a grade, so each row says so
	var gradeLog syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"run", "integrity"}, config.FromMap(envMap), io.Discard, &gradeLog), gradeLog.String())
	require.Contains(t, gradeLog.String(), `"days":4`)
	gradeBackfill := []string{"run", "integrity", "--from", today.AddDate(0, 0, -9).Format(time.DateOnly), "--to", today.Format(time.DateOnly)}
	require.Equal(t, 0, run(t.Context(), gradeBackfill, config.FromMap(envMap), io.Discard, &gradeLog), gradeLog.String())
	var graded, ungraded int
	require.NoError(t, jobsDB.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) filter (where grade is not null),
		    count(*) filter (where grade is null and reasons @> '[{"code":"too_little_traffic"}]')
		  from agg.web_integrity_daily where site_id = $1`, site).Scan(&graded, &ungraded)
	}))
	require.Equal(t, 0, graded)
	require.Equal(t, 10, ungraded)
	for _, bad := range [][]string{
		{"run", "aggregate-web", "--from", "2026-11-01"},
		{"run", "aggregate-web", "--from", "2026-11-02", "--to", "2026-11-01"},
		{"run", "aggregate-web", "--from", "yesterday", "--to", "2026-11-01"},
		{"run", "aggregate-web", "--from", "2026-11-01", "--to", "2026-11-01", "extra"},
		{"run", "edgestats", "--from", "2026-11-01", "--to", "2026-11-01"},
		{"run", "integrity", "--from", "2026-11-01"},
	} {
		var l syncBuf
		require.Equal(t, 2, run(t.Context(), bad, config.FromMap(envMap), io.Discard, &l), strings.Join(bad, " "))
		require.Contains(t, l.String(), "usage: jobs")
	}

	// an hourly horizon under the rfp's 24 months stops the process
	low := maps.Clone(envMap)
	low["API_HOURLY_RETENTION_MONTHS"] = "23"
	var lowLog syncBuf
	require.Equal(t, 1, run(t.Context(), []string{"run", "api-retention"}, config.FromMap(low), io.Discard, &lowLog))
	require.Contains(t, lowLog.String(), "API_HOURLY_RETENTION_MONTHS")

	// capacity reads the api tables with the jobs role alone
	var capOut, capErr syncBuf
	require.Equal(t, 0, run(t.Context(), []string{"capacity"}, config.FromMap(pg.EnvFor("platform")), &capOut, &capErr), capErr.String())
	require.Contains(t, capOut.String(), `"table": "api.metrics_minute"`)
	require.Contains(t, capOut.String(), `"bytes_per_row": null`, "an empty table has no per-row figure")

	var unknown syncBuf
	require.Equal(t, 1, run(t.Context(), []string{"run", "nope"}, config.FromMap(envMap), io.Discard, &unknown))
	require.Contains(t, unknown.String(), "unknown task")
	all := logs.String() + runLog.String() + reconcileLog.String() + rotateLog.String()
	require.NotContains(t, all, creds[natsx.ServiceJobs], "no secret in the logs")
	require.NotContains(t, all, um.AdminPassword, "no secret in the logs")
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestParseRunArgs(t *testing.T) {
	o, err := parseRunArgs("umami-rotate", []string{"--force"})
	require.NoError(t, err)
	require.True(t, o.force)
	o, err = parseRunArgs("umami-rotate", nil)
	require.NoError(t, err)
	require.False(t, o.force)
	for _, bad := range [][]string{{"--force", "x"}, {"--from", "2026-11-01"}} {
		_, err = parseRunArgs("umami-rotate", bad)
		require.Error(t, err, bad)
	}
	_, err = parseRunArgs("reconcile", []string{"--force"})
	require.Error(t, err)
	o, err = parseRunArgs("aggregate-web", []string{"--from", "2026-11-01", "--to", "2026-11-02"})
	require.NoError(t, err)
	require.NotNil(t, o.days)
}
