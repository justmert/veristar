//go:build recoveryit

// Package recoveryit checks that the gateway, the replayer, jobs and the
// metrics writer recover
// from a full nats restart without anyone running platformctl: the memory
// buckets (edge_secrets, visit_state, locks) are recreated by the services
// from the topology in internal/natsx, readiness comes back, and events flow
// from a gateway through the replayer into umami and as outcome counters
// through jobs into postgres again, and api metrics flow through the metrics
// writer into postgres again. it runs against real nats (one server,
// then a 3-node cluster) with the production per-service authorization, a
// real postgres with the platform migrations, the shared dev umami and the
// jobs binary built from cmd/jobs.
//
// run it with the dev stack up and a db-ip file fetched (make geo-fetch):
//
//	set -a && . deploy/compose/.env.dev && set +a
//	UMAMI_IT_URL=$UMAMI_URL DBIP_MMDB=$PWD/.scratch/geo/dbip-country-lite.mmdb \
//	  go test -tags recoveryit -race -count=1 -v -timeout 30m ./tests/recovery/
package recoveryit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/justmert/veristar/internal/config"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/metricswriter"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

const (
	// a current desktop chrome; umami's bot check passes it
	desktopUA   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
	clientIPHdr = "X-Edge-Client-Ip"
	visitorIP   = "198.51.100.10"
	// recoverWithin bounds every wait after the restart. it is a test
	// timeout, not the measured result, which the test logs.
	recoverWithin = 2 * time.Minute
)

var memoryBuckets = []string{natsx.BucketEdgeSecrets, natsx.BucketVisitState, natsx.BucketLocks}

// shared is what both subtests use: postgres, umami and the jobs binary.
type shared struct {
	pg      *dbtest.Postgres
	jobsDB  *pdb.DB
	umami   *url.URL
	admin   *umami.Admin
	dbip    string
	jobsBin string
}

func TestFullRestart(t *testing.T) {
	sh := &shared{dbip: os.Getenv("DBIP_MMDB")}
	raw, pw := os.Getenv("UMAMI_IT_URL"), os.Getenv("UMAMI_ADMIN_PASSWORD")
	if raw == "" || pw == "" || sh.dbip == "" {
		t.Fatal("UMAMI_IT_URL, UMAMI_ADMIN_PASSWORD and DBIP_MMDB must be set; see the package comment")
	}
	var err error
	sh.umami, err = url.Parse(raw)
	require.NoError(t, err)
	uc, err := umami.New(sh.umami, nil)
	require.NoError(t, err)
	sh.admin, err = umami.NewAdmin(uc, umami.SeededUsername, pw)
	require.NoError(t, err)
	sh.pg = jobstest.Postgres(t)
	sh.jobsDB = jobstest.JobsDB(t, sh.pg)
	sh.jobsBin = buildJobs(t)

	t.Run("single server", func(t *testing.T) {
		creds, auth := authorization(t)
		srv := natsctr.StartSingle(t, natsctr.WithConfig(auth))
		testRecovery(t, sh, creds, srv.URL, 1, func(t *testing.T) {
			srv.Stop(t)
			srv.Start(t)
		})
	})
	t.Run("three node cluster", func(t *testing.T) {
		creds, auth := authorization(t)
		cluster := natsctr.StartCluster(t, natsctr.WithConfig(auth))
		testRecovery(t, sh, creds, cluster.URLs(), 3, func(t *testing.T) {
			cluster.Restart(t)
		})
	})
}

func authorization(t *testing.T) (map[string]string, string) {
	t.Helper()
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		creds[u.Name] = randHex(t, 24)
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	return creds, string(auth)
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// natsEnv is the environment of one service's nats connection.
func natsEnv(urls string, replicas int, service string, creds map[string]string) map[string]string {
	return map[string]string{
		"NATS_URL": urls, "NATS_REPLICAS": strconv.Itoa(replicas),
		"NATS_USER": service, "NATS_PASSWORD": creds[service],
	}
}

func connect(t *testing.T, urls string, replicas int, service string, creds map[string]string) (config.NATS, jetstream.JetStream, func()) {
	t.Helper()
	cfg, err := config.LoadNATS(config.FromMap(natsEnv(urls, replicas, service, creds)))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, service, quiet())
	require.NoError(t, err)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return cfg, js, nc.Close
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// testLog sends a service's log lines to the test log, prefixed with its
// name, so a failure shows what each service saw during the restart.
func testLog(t *testing.T, name string) *slog.Logger {
	return obs.NewLogger(testWriter{t: t, name: name}, slog.LevelInfo)
}

type testWriter struct {
	t    *testing.T
	name string
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(w.name + ": " + strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// buildJobs builds cmd/jobs, so jobs runs as the real process it is.
func buildJobs(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jobs")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/jobs")
	cmd.Dir = natsctr.RepoRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// get returns the status and body of url, 0 when it cannot be reached.
func get(url string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// site is a registered site with its two umami websites.
type site struct {
	ID, Quarantine, Tenant string
	Origin, Host           string
}

func newSite(t *testing.T, sh *shared, cp jetstream.JetStream) site {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	team, _, err := sh.admin.CreateTeam(ctx, "rc-"+uuid.NewString()[:12], "")
	require.NoError(t, err)
	t.Cleanup(func() {
		// deleting the team deletes its websites and their data
		if err := sh.admin.DeleteTeam(context.Background(), team.ID); err != nil {
			t.Errorf("cleanup: delete team: %v", err)
		}
	})
	s := site{ID: uuid.NewString(), Quarantine: uuid.NewString(), Tenant: uuid.NewString(),
		Origin: "https://rc.example", Host: "rc.example"}
	for _, id := range []string{s.ID, s.Quarantine} {
		_, err := sh.admin.CreateWebsite(ctx, umami.NewWebsite{ID: id, Name: "rc-it", Domain: s.Host, TeamID: team.ID})
		require.NoError(t, err)
	}
	pub, err := registry.NewPublisher(ctx, cp)
	require.NoError(t, err)
	require.NoError(t, pub.PutSite(ctx, registry.Site{
		ID: s.ID, TenantID: s.Tenant, Status: registry.StatusActive,
		Origins: []string{s.Origin}, QuarantineWebsiteID: s.Quarantine,
	}))
	return s
}

// gw is one in-process gateway, built as cmd/gateway builds it.
type gw struct {
	node *gateway.Node
	url  string
	ops  string
}

func startGateway(t *testing.T, sh *shared, urls string, replicas int, creds map[string]string, node string) *gw {
	t.Helper()
	env := natsEnv(urls, replicas, natsx.ServiceGateway, creds)
	for k, v := range map[string]string{
		// the listeners are httptest servers; the addresses only pass the
		// config check
		"GATEWAY_NODE_ID": node, "GATEWAY_ADDR": freeAddr(t), "GATEWAY_OPS_ADDR": freeAddr(t),
		"PROOF_BITS": "8", "PROOF_REPLAY_LIMIT": "50", "IPCLUSTER_THRESHOLD": "20", "VISITOR_EVENTS_PER_HOUR": "600",
		"RATE_SITE_PER_SECOND": "200", "RATE_SITE_BURST": "400",
		"RATE_PREFIX_PER_SECOND": "20", "RATE_PREFIX_BURST": "60",
		"GATEWAY_MAX_BODY": "8192", "CLIENT_IP_HEADER": clientIPHdr, "GATEWAY_SECURE_TRANSPORT": "true",
		"DBIP_MMDB": sh.dbip, "TRACKER_RELEASE_DIR": filepath.Join(natsctr.RepoRoot(t), "packages", "tracker", "releases"),
	} {
		env[k] = v
	}
	e := config.FromMap(env)
	natsCfg, _ := config.LoadNATS(e)
	gwCfg, _ := config.LoadGateway(e)
	require.NoError(t, e.Err())
	log := testLog(t, node)
	nc, err := natsx.Connect(natsCfg, natsx.ServiceGateway, log)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	n, err := gateway.NewNode(ctx, gateway.NodeConfig{Gateway: gwCfg, NC: nc, Replicas: natsCfg.Replicas, Log: log, Registry: obs.NewRegistry()})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	pub := httptest.NewServer(n.Handler())
	ops := httptest.NewServer(n.Ops())
	t.Cleanup(func() {
		pub.Close()
		ops.Close()
		cancel()
		<-done
		nc.Close()
	})
	return &gw{node: n, url: pub.URL, ops: ops.URL}
}

func (g *gw) client(s site) *gwclient.Client {
	c := gwclient.New(g.url, false)
	c.Header.Set("Origin", s.Origin)
	c.Header.Set("User-Agent", desktopUA)
	c.Header.Set(clientIPHdr, visitorIP)
	c.Header.Set("Sec-Fetch-Site", "cross-site")
	c.Header.Set("Sec-Fetch-Mode", "cors")
	c.Header.Set("Sec-Fetch-Dest", "empty")
	return c
}

// post sends one pageview to path through poster, with a challenge issued
// by issuer, and reports whether the gateway accepted it.
func post(s site, issuer, poster *gw, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := issuer.client(s).Challenge(ctx, s.ID)
	if err != nil {
		return err
	}
	p := gwclient.Solve(ch)
	status, err := poster.client(s).PostEvent(ctx, gwclient.Pageview(s.ID, s.Host, path, &p), nil)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return &statusError{status}
	}
	return nil
}

type statusError struct{ status int }

func (e *statusError) Error() string { return "status " + http.StatusText(e.status) }

func (g *gw) token(t *testing.T, s site) string {
	t.Helper()
	ip, err := gateway.ParseClientIP(visitorIP)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := g.node.Visitor().Token(ctx, s.ID, ip, desktopUA)
	if err != nil {
		return "error: " + err.Error()
	}
	return tok
}

func startReplayer(t *testing.T, sh *shared, urls string, replicas int, creds map[string]string) string {
	t.Helper()
	log := testLog(t, "replayer")
	cfg, err := config.LoadNATS(config.FromMap(natsEnv(urls, replicas, natsx.ServiceReplayer, creds)))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, natsx.ServiceReplayer, log)
	require.NoError(t, err)
	uc, err := umami.New(sh.umami, nil)
	require.NoError(t, err)
	set := replayer.Settings{NodeID: "replayer-rc", OpsAddr: "127.0.0.1:0", UmamiURLs: []*url.URL{sh.umami}, Workers: 4, MaxInFlight: 64}
	r, err := replayer.New(context.Background(), replayer.Config{Settings: set, NC: nc, Replicas: cfg.Replicas, Umami: []*umami.Client{uc}, Log: log, Registry: obs.NewRegistry()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	ops := httptest.NewServer(r.Ops())
	t.Cleanup(func() {
		ops.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("replayer: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("replayer did not stop within 30 s")
		}
		nc.Close()
	})
	return ops.URL
}

// syncBuf is the jobs process output, written by its pipe and read by the
// test.
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

func startJobs(t *testing.T, sh *shared, urls string, replicas int, creds map[string]string) (string, *syncBuf) {
	t.Helper()
	env := os.Environ()
	for k, v := range sh.pg.EnvFor("platform") {
		env = append(env, k+"="+v)
	}
	for k, v := range natsEnv(urls, replicas, natsx.ServiceJobs, creds) {
		env = append(env, k+"="+v)
	}
	ops := freeAddr(t)
	env = append(env, "JOBS_OPS_ADDR="+ops, "API_MINUTE_RETENTION_DAYS=30", "API_HOURLY_RETENTION_MONTHS=36")
	out := &syncBuf{}
	cmd := exec.Command(sh.jobsBin, "daemon")
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("jobs output:\n%s", out)
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("jobs exited with %v:\n%s", err, out)
			}
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			t.Errorf("jobs did not stop within 30 s:\n%s", out)
		}
	})
	return "http://" + ops, out
}

// pathViews is the number of views of path on a website today, read
// through umami's own api.
func pathViews(t *testing.T, sh *shared, website, path string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	day := time.Now().UTC().Truncate(24 * time.Hour)
	rows, err := sh.admin.Metrics(ctx, website, umami.MetricsQuery{
		Range: umami.Range{Start: day.Add(-24 * time.Hour), End: day.Add(48*time.Hour - time.Millisecond)},
		Type:  "path", Limit: 50,
	})
	if err != nil {
		return -1
	}
	for _, r := range rows {
		if r.X == path {
			return r.Y
		}
	}
	return 0
}

func created(t *testing.T, js jetstream.JetStream, bucket string) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kv, err := js.KeyValue(ctx, bucket)
	require.NoError(t, err)
	st, err := kv.Status(ctx)
	require.NoError(t, err)
	info, ok := st.(*jetstream.KeyValueBucketStatus)
	require.True(t, ok)
	return info.StreamInfo().Created
}

// within polls cond every 100 ms and returns how long it took to hold.
func within(t *testing.T, since time.Time, what string, cond func() bool) time.Duration {
	t.Helper()
	deadline := time.Now().Add(recoverWithin)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s of nats being back", what, recoverWithin)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return time.Since(since)
}

func ready(url string) func() bool {
	return func() bool {
		code, _ := get(url + "/readyz")
		return code == http.StatusOK
	}
}

func testRecovery(t *testing.T, sh *shared, creds map[string]string, urls string, replicas int, restart func(*testing.T)) {
	_, admin, closeAdmin := connect(t, urls, replicas, natsx.ServicePlatformctl, creds)
	defer closeAdmin()
	spec, err := natsx.Topology(replicas)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), admin, spec)
	require.NoError(t, err)
	_, cp, closeCP := connect(t, urls, replicas, natsx.ServiceControlPlane, creds)
	defer closeCP()
	s := newSite(t, sh, cp)
	key := s.ID + "/"

	gw1 := startGateway(t, sh, urls, replicas, creds, "edge-1")
	gw2 := startGateway(t, sh, urls, replicas, creds, "edge-2")
	replayerOps := startReplayer(t, sh, urls, replicas, creds)
	jobsOps, jobsOut := startJobs(t, sh, urls, replicas, creds)
	writerOps := startWriter(t, sh, urls, replicas, creds)
	_, collectorJS, closeCollector := connect(t, urls, replicas, natsx.ServiceCollector, creds)
	defer closeCollector()
	api := newAPITenant(t, sh)
	for _, u := range []string{gw1.ops, gw2.ops, replayerOps, jobsOps, writerOps} {
		within(t, time.Now(), u+" ready before the restart", ready(u))
	}

	// normal operation before the restart: an event reaches umami and its
	// outcome counter reaches postgres
	within(t, time.Now(), "an event accepted before the restart", func() bool { return post(s, gw1, gw2, "/before") == nil })
	within(t, time.Now(), "the event in umami before the restart", func() bool { return pathViews(t, sh, s.ID, "/before") == 1 })
	require.NoError(t, gw2.node.Stats().Flush(t.Context()))
	within(t, time.Now(), "the outcome counter in postgres before the restart", func() bool {
		return jobstest.Totals(t, sh.jobsDB)[key+"accepted"] == 1
	})
	publishMetric(t, collectorJS, api)
	within(t, time.Now(), "the api metric in postgres before the restart", func() bool { return apiCount(t, sh, api) == 1 })
	before := map[string]time.Time{}
	for _, b := range memoryBuckets {
		before[b] = created(t, admin, b)
	}
	tokenBefore := gw1.token(t, s)
	require.Equal(t, tokenBefore, gw2.token(t, s))

	// readiness is sampled from the moment nats goes down, so the report
	// shows how long each service was out of rotation, not only that it
	// was ready at some point afterwards
	probes := map[string]string{
		"gateway edge-1": gw1.ops, "gateway edge-2": gw2.ops, "replayer": replayerOps, "jobs": jobsOps,
		"metricswriter": writerOps,
	}
	sampler := newSampler(probes)
	defer sampler.stop()

	// the restart; nothing below runs platformctl or touches a bucket
	stopped := time.Now()
	restart(t)
	back := time.Now()
	t.Logf("nats stopped and healthy again after %s", back.Sub(stopped).Round(time.Millisecond))

	times := map[string]time.Duration{}
	for name, u := range probes {
		times[name+" ready"] = within(t, back, name+" ready", ready(u))
	}
	// each service's own work, measured from nats being healthy again:
	// gateway acceptance, replayer delivery into umami, jobs writing the
	// outcome counter into postgres
	times["edge-1 accepts"] = within(t, back, "an event accepted after the restart", func() bool { return post(s, gw1, gw1, "/after") == nil })
	times["replayer delivers"] = within(t, back, "the event in umami after the restart", func() bool { return pathViews(t, sh, s.ID, "/after") == 1 })
	require.NoError(t, gw1.node.Stats().Flush(t.Context()))
	times["jobs writes"] = within(t, back, "the outcome counter in postgres after the restart", func() bool {
		return jobstest.Totals(t, sh.jobsDB)[key+"accepted"] == 2
	})
	// the writer takes its lock again (after recreating locks when it was
	// lost) and stores a metric published after the restart
	times["writer stores"] = within(t, back, "the api metric in postgres after the restart", func() bool {
		if api.sent < 2 {
			if err := tryPublishMetric(collectorJS, api); err != nil {
				return false
			}
		}
		return apiCount(t, sh, api) >= 2
	})

	recreated := 0
	for _, b := range memoryBuckets {
		if !created(t, admin, b).Equal(before[b]) {
			recreated++
		}
	}
	t.Logf("%d of %d memory buckets were recreated by the services", recreated, len(memoryBuckets))
	if replicas == 1 {
		// a single server loses memory streams entirely, so this run proves
		// the services recreate them
		require.Equal(t, len(memoryBuckets), recreated, "a single server restart must lose every memory bucket")
	}

	// a gateway started after the restart, and the two that kept running,
	// agree on the day's secrets once the survivors verified theirs
	gw3 := startGateway(t, sh, urls, replicas, creds, "edge-3")
	times["edge-3 ready"] = within(t, back, "gateway edge-3 ready", ready(gw3.ops))
	times["secrets agree"] = within(t, back, "all gateways on the same daily secret", func() bool {
		t1, t2, t3 := gw1.token(t, s), gw2.token(t, s), gw3.token(t, s)
		return t1 == t2 && t2 == t3 && !strings.HasPrefix(t1, "error")
	})
	if replicas == 1 {
		require.NotEqual(t, tokenBefore, gw3.token(t, s), "edge_secrets was emptied, so the day's secret is new (documented trade-off)")
	}
	// a challenge of the new gateway is accepted by a survivor, so they
	// share the proof key too
	times["cross-gateway event"] = within(t, back, "a challenge of edge-3 accepted by edge-2", func() bool { return post(s, gw3, gw2, "/cross") == nil })
	within(t, back, "the cross-gateway event in umami", func() bool { return pathViews(t, sh, s.ID, "/cross") == 1 })
	require.NoError(t, gw2.node.Stats().Flush(t.Context()))
	within(t, back, "the second outcome counter in postgres", func() bool {
		return jobstest.Totals(t, sh.jobsDB)[key+"accepted"] == 3
	})

	sampler.stop()
	for _, name := range []string{"gateway edge-1", "gateway edge-2", "replayer", "jobs", "metricswriter"} {
		t.Logf("%-16s not ready for %s in total during and after the restart", name, sampler.notReady(name).Round(10*time.Millisecond))
	}
	for _, k := range []string{
		"gateway edge-1 ready", "gateway edge-2 ready", "replayer ready", "jobs ready",
		"metricswriter ready", "edge-1 accepts", "replayer delivers", "jobs writes", "writer stores",
		"edge-3 ready", "secrets agree", "cross-gateway event",
	} {
		t.Logf("after nats was healthy: %-22s %s", k, times[k].Round(10*time.Millisecond))
	}
	require.NotContains(t, strings.ToLower(jobsOut.String()), "permissions violation", jobsOut.String())
}

// sampler polls /readyz of several services every 100 ms and adds up how
// long each one answered anything but 200.
type sampler struct {
	mu   sync.Mutex
	down map[string]time.Duration
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

func newSampler(probes map[string]string) *sampler {
	s := &sampler{down: map[string]time.Duration{}, done: make(chan struct{})}
	for name, u := range probes {
		s.wg.Go(func() {
			const every = 100 * time.Millisecond
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				if !ready(u)() {
					s.mu.Lock()
					s.down[name] += every
					s.mu.Unlock()
				}
				select {
				case <-s.done:
					return
				case <-t.C:
				}
			}
		})
	}
	return s
}

func (s *sampler) stop() {
	s.once.Do(func() { close(s.done) })
	s.wg.Wait()
}

func (s *sampler) notReady(name string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down[name]
}

// apiTenant is a tenant with an active otlp token in the platform database,
// so the metrics writer accepts its messages.
type apiTenant struct {
	tenant, token uuid.UUID
	sent          int
}

func newAPITenant(t *testing.T, sh *shared) *apiTenant {
	t.Helper()
	ctx := context.Background()
	c, err := sh.pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	var user uuid.UUID
	require.NoError(t, c.QueryRow(ctx, `insert into cp.users (github_id, github_login) values ($1, 'rc') returning id`,
		time.Now().UnixNano()%1_000_000_000+1).Scan(&user))
	a := &apiTenant{}
	slug := "rc-" + randHex(t, 6)
	require.NoError(t, c.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ($1, $1, 'active', $2) returning id`,
		slug, user).Scan(&a.tenant))
	hash := make([]byte, 32)
	_, err = rand.Read(hash)
	require.NoError(t, err)
	require.NoError(t, c.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'rc', 'sa_otlp_rc', $2, $3) returning id`,
		a.tenant, hash, user).Scan(&a.token))
	return a
}

// tryPublishMetric publishes one delta request on API_METRICS as the
// collector does after its own checks.
func tryPublishMetric(js jetstream.JetStream, a *apiTenant) error {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	ra := rm.Resource().Attributes()
	ra.PutStr("service.name", "rc")
	ra.PutStr("scf.tenant.id", a.tenant.String())
	ra.PutStr("scf.token.id", a.token.String())
	ra.PutStr("scf.resource.hash", strings.Repeat("ab", 32))
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("http.server.request.duration")
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	dp := h.DataPoints().AppendEmpty()
	now := time.Now()
	dp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Second)))
	dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
	dp.ExplicitBounds().FromRaw([]float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10})
	counts := make([]uint64, 15)
	counts[2] = 1
	dp.BucketCounts().FromRaw(counts)
	dp.SetCount(1)
	dp.SetSum(0.02)
	dp.Attributes().PutStr("http.route", "/rc")
	body, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = natsx.PublishDedup(ctx, js, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, a.tenant.String()), uuid.NewString(), body, nil)
	if err == nil {
		a.sent++
	}
	return err
}

func publishMetric(t *testing.T, js jetstream.JetStream, a *apiTenant) {
	t.Helper()
	require.NoError(t, tryPublishMetric(js, a))
}

// apiCount is the tenant's stored request count.
func apiCount(t *testing.T, sh *shared, a *apiTenant) int64 {
	t.Helper()
	ctx := context.Background()
	c, err := sh.pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	var n int64
	require.NoError(t, c.QueryRow(ctx, `select coalesce(sum(count), 0)::bigint from api.metrics_minute where tenant_id = $1`, a.tenant).Scan(&n))
	return n
}

// startWriter runs an in-process metrics writer, built as cmd/metricswriter
// builds it, and returns its ops url.
func startWriter(t *testing.T, sh *shared, urls string, replicas int, creds map[string]string) string {
	t.Helper()
	log := testLog(t, "metricswriter")
	cfg, err := config.LoadNATS(config.FromMap(natsEnv(urls, replicas, natsx.ServiceMetricsWriter, creds)))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, natsx.ServiceMetricsWriter, log)
	require.NoError(t, err)
	pcfg, err := pdb.LoadPoolConfig(sh.pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	d, err := pdb.Open(context.Background(), pcfg, natsx.ServiceMetricsWriter)
	require.NoError(t, err)
	set := metricswriter.Settings{NodeID: "writer-rc", OpsAddr: "127.0.0.1:0", Batch: 64,
		FirstPointThreshold: metricswriter.DefaultFirstPointThreshold, RouteCap: 300, ServiceCap: 50}
	w, err := metricswriter.New(metricswriter.Config{Settings: set, NC: nc, Replicas: cfg.Replicas, DB: d, Log: log, Registry: obs.NewRegistry()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	ops := httptest.NewServer(w.Ops())
	t.Cleanup(func() {
		ops.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("metricswriter: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("metricswriter did not stop within 30 s")
		}
		nc.Close()
		d.Close()
	})
	return ops.URL
}
