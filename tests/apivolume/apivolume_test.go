//go:build apivolume

// Package apivolume is the this stage volume run: 1,000,000 requests across 10
// tenants and 30 services, recorded by the pinned opentelemetry go sdk and
// exported through the built scf-otelcol to a real nats server (production
// permissions), stored by the built metricswriter in a real postgresql, then
// rolled up by the built jobs binary (`jobs run api-rollup`). it checks that
// nothing is lost, that the writer's lag returns to zero, that hourly rows
// equal the minute rows they came from, and measures database growth with
// `jobs capacity`. host load is logged next to every result, because the
// host is shared.
//
// run it with `make api-volume` (it needs the dev umami values from
// deploy/compose/.env.dev only because `jobs run` loads them; umami itself is
// not called by the api tasks). VOLUME_MINUTES (default 20, at most 60) sets
// how long the requests are spread over.
package apivolume

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/histo"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

const (
	totalRequests     = 1_000_000
	tenantCount       = 10
	servicesPerTenant = 3
	routesPerService  = 20
	exportEvery       = 10 * time.Second
)

var statuses = []int{200, 201, 404, 500}

type proc struct {
	name string
	cmd  *exec.Cmd
	logs *syncBuf
	done chan struct{}
}

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

func (p *proc) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func start(t *testing.T, name, bin string, env []string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	p := &proc{name: name, cmd: cmd, logs: &syncBuf{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			s := p.logs.String()
			if len(s) > 6000 {
				s = s[len(s)-6000:]
			}
			t.Logf("%s log:\n%s", name, s)
		}
	})
	return p
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitOK(t *testing.T, url string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not ready", url)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func build(t *testing.T, root, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, out)
	}
	return bin
}

// load is the host's 1, 5 and 15 minute load averages and cpu count.
func load() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "unknown"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "unknown"
	}
	return fmt.Sprintf("load %s %s %s on %d cpus", f[0], f[1], f[2], numCPU())
}

func numCPU() int {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "processor\t:")
}

func randomToken(t *testing.T) string {
	t.Helper()
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 40)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			t.Fatal(err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return "sa_otlp_" + string(b)
}

type tenant struct {
	id    uuid.UUID
	token string
}

// service is one tenant process exporting through the collector.
type service struct {
	tn    tenant
	name  string
	mp    *sdkmetric.MeterProvider
	hist  metric.Float64Histogram
	sent  atomic.Int64
	quota int
}

func newService(t *testing.T, tn tenant, name, httpAddr, grpcAddr string, delta, grpc bool) *service {
	t.Helper()
	ctx := context.Background()
	sel := func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.CumulativeTemporality }
	if delta {
		sel = func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.DeltaTemporality }
	}
	var exp sdkmetric.Exporter
	var err error
	if grpc {
		exp, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(grpcAddr), otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithHeaders(map[string]string{"authorization": "Bearer " + tn.token}),
			otlpmetricgrpc.WithTemporalitySelector(sel), otlpmetricgrpc.WithCompressor("gzip"),
			otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 5 * time.Second, MaxElapsedTime: 5 * time.Minute}))
	} else {
		exp, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpoint(httpAddr), otlpmetrichttp.WithInsecure(),
			otlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer " + tn.token}),
			otlpmetrichttp.WithTemporalitySelector(sel), otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
			otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 5 * time.Second, MaxElapsedTime: 5 * time.Minute}))
	}
	if err != nil {
		t.Fatal(err)
	}
	res := resource.NewSchemaless(attribute.String("service.name", name), attribute.String("service.instance.id", uuid.NewString()))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(exportEvery))))
	h, err := mp.Meter("volume").Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(histo.Bounds[:]...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return &service{tn: tn, name: name, mp: mp, hist: h}
}

// run records the service's quota evenly over d, in steps of 100 ms.
func (s *service) run(ctx context.Context, d time.Duration, seed uint64) {
	rng := mrand.New(mrand.NewPCG(seed, seed+1))
	steps := int(d / (100 * time.Millisecond))
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	done := 0
	for step := 1; step <= steps; step++ {
		target := s.quota * step / steps
		for ; done < target; done++ {
			route := fmt.Sprintf("/v1/%s/r%02d", s.name, rng.IntN(routesPerService))
			status := statuses[rng.IntN(len(statuses))]
			v := math.Exp(math.Log(0.001) + rng.Float64()*(math.Log(3)-math.Log(0.001)))
			s.hist.Record(ctx, v, metric.WithAttributes(
				attribute.String("http.request.method", http.MethodGet),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status)))
			s.sent.Add(1)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func TestVolume(t *testing.T) {
	minutes := 20
	if v := os.Getenv("VOLUME_MINUTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 60 {
			t.Fatalf("VOLUME_MINUTES %q must be 1 to 60", v)
		}
		minutes = n
	}
	for _, k := range []string{"UMAMI_URL", "UMAMI_ADMIN_PASSWORD", "UMAMI_USER_PASSWORD_KEY"} {
		if os.Getenv(k) == "" {
			t.Fatalf("%s is not set; run through make api-volume", k)
		}
	}
	ctx := context.Background()
	root := natsctr.RepoRoot(t)
	col := filepath.Join(root, "dist", "scf-otelcol", "scf-otelcol")
	if _, err := os.Stat(col); err != nil {
		t.Fatalf("collector binary missing (%v); run make collector-build", err)
	}
	writerBin := build(t, root, "./cmd/metricswriter")
	jobsBin := build(t, root, "./cmd/jobs")
	t.Logf("start: %s", load())

	// postgresql
	pg, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	acfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.Migrate(ctx, acfg, db.Migrations(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		t.Fatal(err)
	}
	super, err := pg.Superuser(ctx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close(context.Background()) })

	// nats with the production permissions
	creds := map[string]string{}
	for _, u := range natsx.Users() {
		creds[u.Name] = "pw-" + u.Name + "-" + uuid.NewString()
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), creds)
	if err != nil {
		t.Fatal(err)
	}
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	conn := func(user string) jetstream.JetStream {
		nc, err := nats.Connect(srv.URL, nats.UserInfo(user, creds[user]), nats.CustomInboxPrefix(natsx.InboxPrefix(user)), nats.MaxReconnects(-1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(nc.Close)
		js, err := jetstream.New(nc)
		if err != nil {
			t.Fatal(err)
		}
		return js
	}
	admin := conn(natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(ctx, admin, spec); err != nil {
		t.Fatal(err)
	}
	pub, err := registry.NewPublisher(ctx, conn(natsx.ServiceControlPlane))
	if err != nil {
		t.Fatal(err)
	}

	// collector
	httpAddr, grpcAddr, health := freeAddr(t), freeAddr(t), freeAddr(t)
	start(t, "collector", col, []string{
		"NATS_URL=" + srv.URL, "NATS_USER=collector", "NATS_PASSWORD=" + creds["collector"],
		"COLLECTOR_GRPC_ADDR=" + grpcAddr, "COLLECTOR_HTTP_ADDR=" + httpAddr, "COLLECTOR_HEALTH_ADDR=" + health,
		"COLLECTOR_MEMORY_LIMIT_MIB=512", "COLLECTOR_MEMORY_SPIKE_MIB=128",
		"COLLECTOR_RATE_REQUESTS_PER_SECOND=50", "COLLECTOR_RATE_REQUESTS_BURST=100",
		"COLLECTOR_RATE_ITEMS_PER_SECOND=50000", "COLLECTOR_RATE_ITEMS_BURST=200000",
	}, "--config", filepath.Join(root, "collector", "otelcol.yaml"))
	waitOK(t, "http://"+health+"/", time.Minute)

	// writer, with the dev values (docs/reference/api-storage.md)
	dbEnv := func() []string {
		e := pg.EnvFor("platform")
		out := []string{}
		for k, v := range e {
			if strings.HasPrefix(k, "PLATFORM_DB_ADMIN") {
				continue
			}
			out = append(out, k+"="+v)
		}
		return out
	}
	writerOps := freeAddr(t)
	writer := start(t, "metricswriter", writerBin, append(dbEnv(),
		"NATS_URL="+srv.URL, "NATS_REPLICAS=1", "NATS_USER=metricswriter", "NATS_PASSWORD="+creds["metricswriter"],
		"METRICSWRITER_NODE_ID=volume", "METRICSWRITER_OPS_ADDR="+writerOps, "METRICSWRITER_BATCH=256",
		"METRICSWRITER_FIRST_POINT_THRESHOLD=120s", "METRICSWRITER_ROUTE_CAP=300", "METRICSWRITER_SERVICE_CAP=50"))
	waitOK(t, "http://"+writerOps+"/readyz", time.Minute)

	// tenants with real tokens: rows in cp as the control plane writes them,
	// entries in registry_otlp as tests/collector writes them
	var user uuid.UUID
	if err := super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (7001, 'volume') returning id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	tenants := make([]tenant, tenantCount)
	for i := range tenants {
		tn := tenant{token: randomToken(t)}
		if err := super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ($1, $1, 'active', $2) returning id`,
			fmt.Sprintf("volume-%02d", i), user).Scan(&tn.id); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(tn.token))
		var tokID uuid.UUID
		if err := super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'volume', $2, $3, $4) returning id`,
			tn.id, tn.token[:12], h[:], user).Scan(&tokID); err != nil {
			t.Fatal(err)
		}
		if err := pub.PutOTLPToken(ctx, registry.OTLPToken{Hash: registry.HashOTLPToken(tn.token), TenantID: tn.id.String(), TokenID: tokID.String(), Status: registry.StatusActive}); err != nil {
			t.Fatal(err)
		}
		waitToken(t, httpAddr, tn.token)
		tenants[i] = tn
	}

	dbBefore := dbSize(t, super)
	var services []*service
	for i, tn := range tenants {
		for j := range servicesPerTenant {
			n := i*servicesPerTenant + j
			// a mix of delta and cumulative exporters over http and grpc
			services = append(services, newService(t, tn, fmt.Sprintf("svc%02d", n), httpAddr, grpcAddr, n%2 == 0, n%3 == 0))
		}
	}
	for i, s := range services {
		s.quota = totalRequests / len(services)
		if i < totalRequests%len(services) {
			s.quota++
		}
	}

	// lag sampler: pending plus unacknowledged on the writer's consumer
	cons, err := admin.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	var maxLag atomic.Uint64
	sampleCtx, stopSample := context.WithCancel(ctx)
	var sampleWG sync.WaitGroup
	sampleWG.Go(func() {
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		for {
			if info, err := cons.Info(sampleCtx); err == nil {
				lag := info.NumPending + uint64(info.NumAckPending)
				if lag > maxLag.Load() {
					maxLag.Store(lag)
				}
			}
			select {
			case <-sampleCtx.Done():
				return
			case <-tk.C:
			}
		}
	})
	loadTick := time.NewTicker(5 * time.Minute)
	loadDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-loadDone:
				return
			case <-loadTick.C:
				t.Logf("during the run: %s", load())
			}
		}
	}()

	began := time.Now()
	runFor := time.Duration(minutes) * time.Minute
	var wg sync.WaitGroup
	for i, s := range services {
		wg.Go(func() { s.run(ctx, runFor, uint64(i+1)) })
	}
	wg.Wait()
	for _, s := range services {
		fctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		if err := s.mp.Shutdown(fctx); err != nil {
			cancel()
			t.Fatalf("%s final export: %v", s.name, err)
		}
		cancel()
	}
	sendDone := time.Now()
	loadTick.Stop()
	close(loadDone)
	var sent int64
	for _, s := range services {
		sent += s.sent.Load()
	}
	t.Logf("%d requests recorded by %d services of %d tenants in %s; %s", sent, len(services), len(tenants), sendDone.Sub(began).Round(time.Second), load())
	if sent != totalRequests {
		t.Fatalf("recorded %d, want %d", sent, totalRequests)
	}

	// lag returns to zero
	deadline := time.Now().Add(5 * time.Minute)
	stable := 0
	for {
		info, err := cons.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			if stable++; stable == 3 {
				break
			}
		} else {
			stable = 0
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer lag did not return to zero: pending %d, ack pending %d", info.NumPending, info.NumAckPending)
		}
		time.Sleep(time.Second)
	}
	stopSample()
	sampleWG.Wait()
	sinfo, err := admin.Stream(ctx, natsx.StreamAPIMetrics)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("writer lag back to zero %s after the last export; highest lag %d messages; %d messages published on API_METRICS; writer pending gauge %s",
		time.Since(sendDone).Round(time.Second), maxLag.Load(), sinfo.CachedInfo().State.LastSeq, scrape(t, writerOps, "metricswriter_consumer_pending_messages"))

	// no loss: every request is in a minute row, per tenant and in total
	var stored int64
	for _, tn := range tenants {
		var n, want int64
		if err := super.QueryRow(ctx, `select coalesce(sum(count), 0)::bigint from api.metrics_minute where tenant_id = $1`, tn.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		for _, s := range services {
			if s.tn.id == tn.id {
				want += s.sent.Load()
			}
		}
		if n != want {
			t.Errorf("tenant %s: stored %d, sent %d", tn.id, n, want)
		}
		stored += n
	}
	if stored != sent {
		t.Fatalf("stored sum(count) %d, sent %d", stored, sent)
	}
	var minuteRows, series int64
	if err := super.QueryRow(ctx, `select count(*), count(distinct (tenant_id, service, method, route, status_code, source)) from api.metrics_minute`).Scan(&minuteRows, &series); err != nil {
		t.Fatal(err)
	}
	t.Logf("no loss: stored sum(count) %d equals sent %d, in %d minute rows of %d series; writer %s",
		stored, sent, minuteRows, series, scrapeAll(t, writerOps, "metricswriter_messages_total", "metricswriter_batches_total"))

	// rollups with the real task
	jobsEnv := append(dbEnv(),
		"NATS_URL="+srv.URL, "NATS_REPLICAS=1", "NATS_USER=jobs", "NATS_PASSWORD="+creds["jobs"],
		"UMAMI_URL="+os.Getenv("UMAMI_URL"), "UMAMI_ADMIN_PASSWORD="+os.Getenv("UMAMI_ADMIN_PASSWORD"),
		"UMAMI_USER_PASSWORD_KEY="+os.Getenv("UMAMI_USER_PASSWORD_KEY"),
		"API_MINUTE_RETENTION_DAYS=30", "API_HOURLY_RETENTION_MONTHS=36")
	rolled := time.Now()
	for i := 0; ; i++ {
		var pending int64
		if err := super.QueryRow(ctx, `select count(*) from api.rollup_pending`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if i == 20 {
			t.Fatalf("%d hours still pending after 20 rollup runs", pending)
		}
		cmd := exec.Command(jobsBin, "run", "api-rollup")
		cmd.Env = jobsEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("jobs run api-rollup: %v\n%s", err, out)
		}
	}
	t.Logf("rollup took %s; %s", time.Since(rolled).Round(time.Millisecond), load())
	var mismatches, hourlyRows, hourlySum int64
	err = super.QueryRow(ctx, `
		with m as (
		  select tenant_id, date_trunc('hour', bucket_start, 'UTC') as hour, service, method, route, status_code, source,
		         sum(count)::bigint as count, api.sum_buckets(counts) as counts
		  from api.metrics_minute group by 1, 2, 3, 4, 5, 6, 7)
		select count(*) from m
		full join api.metrics_hourly h
		  on h.tenant_id = m.tenant_id and h.bucket_start = m.hour and h.service = m.service and h.method = m.method
		 and h.route = m.route and h.status_code = m.status_code and h.source = m.source
		where m.count is distinct from h.count or m.counts is distinct from h.counts`).Scan(&mismatches)
	if err != nil {
		t.Fatal(err)
	}
	if err := super.QueryRow(ctx, `select count(*), coalesce(sum(count), 0)::bigint from api.metrics_hourly`).Scan(&hourlyRows, &hourlySum); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 || hourlySum != sent {
		t.Fatalf("hourly rows differ from minute rows in %d groups; hourly sum %d, sent %d", mismatches, hourlySum, sent)
	}
	t.Logf("rollup: %d hourly rows, sum %d, equal to the minute rows in every dimension and bucket", hourlyRows, hourlySum)

	// growth and per-row size
	if _, err := super.Exec(ctx, `vacuum (analyze) api.metrics_minute, api.metrics_hourly, api.metrics_daily, api.series_state, api.resource_seen, api.ingest_dedup`); err != nil {
		t.Fatal(err)
	}
	dbAfter := dbSize(t, super)
	cmd := exec.Command(jobsBin, "capacity")
	cmd.Env = dbEnv()
	capOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("jobs capacity: %v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, capOut, "", "  "); err != nil {
		t.Fatalf("jobs capacity output is not json: %v\n%s", err, capOut)
	}
	t.Logf("database platform grew from %d to %d bytes (+%d); writer state tables: %s", dbBefore, dbAfter, dbAfter-dbBefore, stateSizes(t, super))
	t.Logf("jobs capacity:\n%s", pretty.String())
	t.Logf("end: %s", load())
	writer.stop()
}

func waitToken(t *testing.T, addr, token string) {
	t.Helper()
	empty, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(pmetric.NewMetrics())
	deadline := time.Now().Add(15 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/metrics", bytes.NewReader(empty))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("token not accepted by the collector")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func dbSize(t *testing.T, c *pgx.Conn) int64 {
	t.Helper()
	var n int64
	if err := c.QueryRow(context.Background(), `select pg_database_size('platform')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stateSizes(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var parts []string
	for _, rel := range []string{"api.series_state", "api.resource_seen", "api.ingest_dedup", "api.route_registry"} {
		var rows, bytes int64
		if err := c.QueryRow(context.Background(), `select (select count(*) from `+rel+`), pg_total_relation_size($1::regclass)`, rel).Scan(&rows, &bytes); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fmt.Sprintf("%s %d rows %d bytes", rel, rows, bytes))
	}
	return strings.Join(parts, "; ")
}

func scrape(t *testing.T, addr, name string) string {
	t.Helper()
	return scrapeAll(t, addr, name)
}

// scrapeAll returns every sample line of the named metrics.
func scrapeAll(t *testing.T, addr string, names ...string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		return "unavailable: " + err.Error()
	}
	defer resp.Body.Close()
	var out []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		for _, n := range names {
			if strings.HasPrefix(line, n+" ") || strings.HasPrefix(line, n+"{") {
				out = append(out, line)
			}
		}
	}
	return strings.Join(out, ", ")
}
