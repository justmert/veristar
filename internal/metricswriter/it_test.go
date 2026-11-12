//go:build metricswriterit

// the this stage exactness suite: the real scf-otelcol binary and the built
// metricswriter binary against a real nats server (production user
// permissions) and a real postgresql, driven by the pinned opentelemetry go
// sdk exporters. run it with `make metricswriter-it`, which builds the
// collector first. it lives in this package so the redelivery case can
// commit a batch through the writer's own code and then not ack it, which
// is exactly what a writer that dies between commit and ack leaves behind.
package metricswriter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"slices"
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
	"github.com/stellar/go-stellar-sdk/strkey"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/justmert/stellar-analytics/db"
	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/histo"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

// the writer settings of the suite. the first-point threshold is shortened
// from the 120 s default so that "an app that has been running longer than
// the threshold" takes seconds instead of minutes; the rule is the same.
// the one-hour case uses points that really started an hour ago.
const (
	itThreshold  = 5 * time.Second
	itRouteCap   = 300
	itServiceCap = 50
	exportEvery  = 300 * time.Millisecond
)

var canonicalBounds = histo.Bounds[:]

type suite struct {
	root      string
	pg        *dbtest.Postgres
	super     *pgx.Conn
	nats      *natsctr.Server
	creds     map[string]string
	admin     jetstream.JetStream
	pub       *registry.Publisher
	httpAddr  string
	grpcAddr  string
	collector *proc
	writerBin string
	userID    uuid.UUID
	writers   int
	// top is the whole suite's test: processes that outlive one subtest
	// are cleaned up with it
	top *testing.T
}

type proc struct {
	cmd  *exec.Cmd
	logs *syncBuf
	ops  string
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

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func setup(t *testing.T) *suite {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s := &suite{root: natsctr.RepoRoot(t), creds: map[string]string{}, top: t}
	col := filepath.Join(s.root, "dist", "scf-otelcol", "scf-otelcol")
	if _, err := os.Stat(col); err != nil {
		t.Fatalf("collector binary missing (%v); run make collector-build", err)
	}

	// the writer binary, built from this checkout
	s.writerBin = filepath.Join(t.TempDir(), "metricswriter")
	build := exec.Command("go", "build", "-o", s.writerBin, "./cmd/metricswriter")
	build.Dir = s.root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build metricswriter: %v\n%s", err, out)
	}

	// postgresql, migrated, with role passwords
	var err error
	s.pg, err = dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.pg.Terminate(context.Background()) })
	acfg, err := pdb.LoadAdminConfig(s.pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.Migrate(ctx, acfg, db.Migrations(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	rcfg, err := pdb.LoadRolesConfig(s.pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		t.Fatal(err)
	}
	s.super, err = s.pg.Superuser(ctx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.super.Close(context.Background()) })
	if err := s.super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4242, 'it') returning id`).Scan(&s.userID); err != nil {
		t.Fatal(err)
	}

	// nats with the production permission set
	for _, u := range natsx.Users() {
		s.creds[u.Name] = "pw-" + u.Name + "-" + uuid.NewString()
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), s.creds)
	if err != nil {
		t.Fatal(err)
	}
	s.nats = natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	s.admin = s.js(t, natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(ctx, s.admin, spec); err != nil {
		t.Fatal(err)
	}
	s.pub, err = registry.NewPublisher(ctx, s.js(t, natsx.ServiceControlPlane))
	if err != nil {
		t.Fatal(err)
	}

	s.httpAddr, s.grpcAddr = freeAddr(t), freeAddr(t)
	s.startCollector(t)
	return s
}

func (s *suite) conn(t *testing.T, user string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.nats.URL, nats.UserInfo(user, s.creds[user]), nats.CustomInboxPrefix(natsx.InboxPrefix(user)),
		nats.MaxReconnects(-1), nats.ReconnectWait(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func (s *suite) js(t *testing.T, user string) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.New(s.conn(t, user))
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func (s *suite) startCollector(t *testing.T) {
	t.Helper()
	health := freeAddr(t)
	cmd := exec.Command(filepath.Join(s.root, "dist", "scf-otelcol", "scf-otelcol"), "--config", filepath.Join(s.root, "collector", "otelcol.yaml"))
	cmd.Env = []string{
		"NATS_URL=" + s.nats.URL, "NATS_USER=collector", "NATS_PASSWORD=" + s.creds["collector"],
		"COLLECTOR_GRPC_ADDR=" + s.grpcAddr, "COLLECTOR_HTTP_ADDR=" + s.httpAddr, "COLLECTOR_HEALTH_ADDR=" + health,
		"COLLECTOR_MEMORY_LIMIT_MIB=512", "COLLECTOR_MEMORY_SPIKE_MIB=128",
		"COLLECTOR_RATE_REQUESTS_PER_SECOND=500", "COLLECTOR_RATE_REQUESTS_BURST=1000",
		"COLLECTOR_RATE_ITEMS_PER_SECOND=100000", "COLLECTOR_RATE_ITEMS_BURST=200000",
	}
	p := &proc{cmd: cmd, logs: &syncBuf{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(p.done) }()
	s.collector = p
	t.Cleanup(func() {
		p.stop(syscall.SIGINT)
		if t.Failed() {
			t.Logf("collector log:\n%s", tail(p.logs.String(), 4000))
		}
	})
	waitHTTP(t, "http://"+health+"/", 60*time.Second)
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func waitHTTP(t *testing.T, url string, d time.Duration) {
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
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *proc) stop(sig syscall.Signal) {
	if p == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(sig)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func (s *suite) writerEnv(ops string) []string {
	env := []string{
		"NATS_URL=" + s.nats.URL, "NATS_REPLICAS=1",
		"NATS_USER=" + natsx.ServiceMetricsWriter, "NATS_PASSWORD=" + s.creds[natsx.ServiceMetricsWriter],
		"METRICSWRITER_NODE_ID=it-" + strconv.Itoa(s.writers),
		"METRICSWRITER_OPS_ADDR=" + ops,
		"METRICSWRITER_BATCH=64",
		"METRICSWRITER_FIRST_POINT_THRESHOLD=" + itThreshold.String(),
		"METRICSWRITER_ROUTE_CAP=" + strconv.Itoa(itRouteCap),
		"METRICSWRITER_SERVICE_CAP=" + strconv.Itoa(itServiceCap),
	}
	for k, v := range s.pg.EnvFor("platform") {
		if strings.HasPrefix(k, "PLATFORM_DB_ADMIN") {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// startWriter runs the built binary and waits until it reports ready.
func (s *suite) startWriter(t *testing.T) *proc {
	t.Helper()
	s.writers++
	ops := freeAddr(t)
	cmd := exec.Command(s.writerBin)
	cmd.Env = s.writerEnv(ops)
	p := &proc{cmd: cmd, logs: &syncBuf{}, ops: ops, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(p.done) }()
	s.top.Cleanup(func() {
		p.stop(syscall.SIGINT)
		if s.top.Failed() {
			s.top.Logf("writer %s log:\n%s", ops, tail(p.logs.String(), 6000))
		}
	})
	waitHTTP(t, "http://"+ops+"/readyz", 30*time.Second)
	return p
}

// metric reads one sample from a writer's /metrics.
func (p *proc) metric(t *testing.T, name string) float64 {
	t.Helper()
	resp, err := http.Get("http://" + p.ops + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, name+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatal(err)
			}
			return f
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

type tenant struct {
	id    uuid.UUID
	token string
	tokID uuid.UUID
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

// newTenant creates the tenant and token rows as the control plane would,
// publishes the token to registry_otlp and waits until the collector
// accepts it.
func (s *suite) newTenant(t *testing.T) tenant {
	t.Helper()
	ctx := context.Background()
	tn := tenant{token: randomToken(t)}
	slug := "it-" + strings.ReplaceAll(uuid.NewString()[:13], "-", "")
	if err := s.super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ($1, $1, 'active', $2) returning id`,
		slug, s.userID).Scan(&tn.id); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(tn.token))
	if err := s.super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', $2, $3, $4) returning id`,
		tn.id, tn.token[:12], h[:], s.userID).Scan(&tn.tokID); err != nil {
		t.Fatal(err)
	}
	err := s.pub.PutOTLPToken(ctx, registry.OTLPToken{Hash: registry.HashOTLPToken(tn.token), TenantID: tn.id.String(), TokenID: tn.tokID.String(), Status: registry.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(pmetric.NewMetrics())
	deadline := time.Now().Add(10 * time.Second)
	for {
		if code := s.postMetrics(t, tn.token, empty); code == http.StatusOK {
			return tn
		}
		if time.Now().After(deadline) {
			t.Fatal("token not accepted by the collector")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *suite) postMetrics(t *testing.T, token string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+s.httpAddr+"/v1/metrics", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// drained waits until the writer consumer holds nothing: no pending and no
// unacknowledged message.
func (s *suite) drained(t *testing.T, d time.Duration) {
	t.Helper()
	ctx := context.Background()
	cons, err := s.admin.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(d)
	stable := 0
	for {
		info, err := cons.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			// twice in a row, so a message between fetch and info is not missed
			if stable++; stable == 2 {
				return
			}
		} else {
			stable = 0
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue not drained at %s: pending %d, ack pending %d, redelivered %d, delivered %+v, ack floor %+v",
				time.Now().UTC().Format(time.RFC3339Nano), info.NumPending, info.NumAckPending, info.NumRedelivered, info.Delivered, info.AckFloor)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

type dimKey struct {
	route  string
	status int16
}

// stored returns sum(count) of the tenant's minute rows of one source, by
// route and status.
func (s *suite) stored(t *testing.T, tenantID uuid.UUID, source string) (map[dimKey]int64, int64) {
	t.Helper()
	rows, err := s.super.Query(context.Background(),
		`select route, status_code, sum(count)::bigint from api.metrics_minute where tenant_id = $1 and source = $2 group by 1, 2`, tenantID, source)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[dimKey]int64{}
	var total int64
	for rows.Next() {
		var k dimKey
		var n int64
		if err := rows.Scan(&k.route, &k.status, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
		total += n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out, total
}

// waitStored waits until the tenant's metric rows hold at least n requests.
func (s *suite) waitStored(t *testing.T, tenantID uuid.UUID, n int64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		_, total := s.stored(t, tenantID, SourceMetric)
		if total >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored %d of %d requests", total, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// expectExact drains and compares the stored counts with what was sent.
func (s *suite) expectExact(t *testing.T, tn tenant, source string, sent map[dimKey]int64) {
	t.Helper()
	s.drained(t, 90*time.Second)
	got, total := s.stored(t, tn.id, source)
	var want int64
	for k, n := range sent {
		want += n
		if got[k] != n {
			t.Errorf("route %q status %d: stored %d, sent %d", k.route, k.status, got[k], n)
		}
	}
	for k, n := range got {
		if _, ok := sent[k]; !ok {
			t.Errorf("stored %d for route %q status %d that was never sent", n, k.route, k.status)
		}
	}
	if total != want {
		t.Fatalf("sum(count) %d, sent %d", total, want)
	}
	t.Logf("sum(count) %d equals requests sent %d", total, want)
}

// app is one tenant process: a meter provider exporting through the
// collector, with a histogram on the canonical bounds.
type app struct {
	mp   *sdkmetric.MeterProvider
	hist metric.Float64Histogram
	gate *gatedExporter
	mu   sync.Mutex
	sent map[dimKey]int64
	vals []float64
}

// gatedExporter wraps the real otlp exporter. while closed it drops exports
// the way an unreachable backend loses them, so the sdk keeps counting
// without the writer ever seeing those points.
type gatedExporter struct {
	sdkmetric.Exporter
	open atomic.Bool
}

func (g *gatedExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if !g.open.Load() {
		return nil
	}
	return g.Exporter.Export(ctx, rm)
}

type appOpts struct {
	delta   bool
	grpc    bool
	service string
	closed  bool // start with exports dropped
}

func (s *suite) newApp(t *testing.T, tn tenant, o appOpts) *app {
	t.Helper()
	ctx := context.Background()
	sel := func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.CumulativeTemporality }
	if o.delta {
		sel = func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.DeltaTemporality }
	}
	var exp sdkmetric.Exporter
	var err error
	if o.grpc {
		exp, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(s.grpcAddr), otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithHeaders(map[string]string{"authorization": "Bearer " + tn.token}),
			otlpmetricgrpc.WithTemporalitySelector(sel),
			otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: true, InitialInterval: 200 * time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: 2 * time.Minute}))
	} else {
		exp, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpoint(s.httpAddr), otlpmetrichttp.WithInsecure(),
			otlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer " + tn.token}),
			otlpmetrichttp.WithTemporalitySelector(sel),
			otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: true, InitialInterval: 200 * time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: 2 * time.Minute}))
	}
	if err != nil {
		t.Fatal(err)
	}
	g := &gatedExporter{Exporter: exp}
	g.open.Store(!o.closed)
	svc := o.service
	if svc == "" {
		svc = "api"
	}
	// identical resource attributes for every app of a service: no instance
	// id, so replicas and restarts share a resource hash
	res := resource.NewSchemaless(attribute.String("service.name", svc), attribute.String("deployment.environment.name", "it"))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(g, sdkmetric.WithInterval(exportEvery))))
	h, err := mp.Meter("it").Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(canonicalBounds...))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{mp: mp, hist: h, gate: g, sent: map[dimKey]int64{}}
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return a
}

// record records one request; counted says whether it belongs to what the
// writer must store.
func (a *app) record(route string, status int, v float64, counted bool) {
	a.hist.Record(context.Background(), v, metric.WithAttributes(
		attribute.String("http.request.method", http.MethodGet),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status)))
	if !counted {
		return
	}
	a.mu.Lock()
	a.sent[dimKey{route, int16(status)}]++
	a.vals = append(a.vals, v)
	a.mu.Unlock()
}

// flush exports what is recorded now and fails the test if the collector
// did not accept it.
func (a *app) flush(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.mp.ForceFlush(ctx); err != nil {
		t.Fatalf("export: %v", err)
	}
}

func (a *app) shutdown(t *testing.T) {
	t.Helper()
	a.flush(t)
	if err := a.mp.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func merge(dst map[dimKey]int64, srcs ...*app) map[dimKey]int64 {
	if dst == nil {
		dst = map[dimKey]int64{}
	}
	for _, a := range srcs {
		a.mu.Lock()
		for k, n := range a.sent {
			dst[k] += n
		}
		a.mu.Unlock()
	}
	return dst
}

var statuses = []int{200, 201, 404, 500}

// load records n requests over the given routes and statuses with a pause,
// so they spread over several exports.
func (a *app) load(n int, routes []string, pause time.Duration) {
	for i := range n {
		a.record(routes[i%len(routes)], statuses[(i/len(routes))%len(statuses)], 0.001+float64(i%50)*0.004, true)
		if pause > 0 && i%25 == 24 {
			time.Sleep(pause)
		}
	}
}

func TestMetricsWriter(t *testing.T) {
	s := setup(t)
	w := s.startWriter(t)
	routes := []string{"/accounts/{account}", "/tx", "/health"}

	t.Run("delta exporter is exact", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{delta: true})
		a.load(1200, routes, 20*time.Millisecond)
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
	})

	t.Run("cumulative exporter over grpc is exact", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{grpc: true})
		a.load(1200, routes, 20*time.Millisecond)
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
	})

	t.Run("writer killed and restarted mid-run", func(t *testing.T) {
		tn := s.newTenant(t)
		d := s.newApp(t, tn, appOpts{delta: true, service: "delta"})
		c := s.newApp(t, tn, appOpts{service: "cumulative"})
		var wg sync.WaitGroup
		for _, a := range []*app{d, c} {
			wg.Go(func() { a.load(3000, routes, 25*time.Millisecond) })
		}
		// kill -9 twice while both apps send, so batches die mid-flight
		for range 2 {
			time.Sleep(700 * time.Millisecond)
			w.stop(syscall.SIGKILL)
			time.Sleep(300 * time.Millisecond)
			w = s.startWriter(t)
		}
		wg.Wait()
		d.shutdown(t)
		c.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, d, c))
	})

	t.Run("batch redelivered after a commit without ack", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{})
		// the subprocess writer stops cleanly and releases the lock, so the
		// batch below is taken by this test's own writer
		w.stop(syscall.SIGINT)
		a.load(600, routes, 10*time.Millisecond)
		a.flush(t)
		committed := s.commitWithoutAck(t)
		if committed == 0 {
			t.Fatal("no message was committed")
		}
		w = s.startWriter(t)
		a.load(300, routes, 10*time.Millisecond)
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
		if dup := w.metric(t, `metricswriter_messages_total{outcome="duplicate"}`); dup < float64(committed) {
			t.Fatalf("%v duplicates skipped, %d messages were committed without ack", dup, committed)
		} else {
			t.Logf("%d messages committed without ack, %v redeliveries skipped by ingest_dedup", committed, dup)
		}
	})

	t.Run("app restart resets cumulative counts", func(t *testing.T) {
		tn := s.newTenant(t)
		a1 := s.newApp(t, tn, appOpts{})
		a1.load(500, routes, 20*time.Millisecond)
		a1.shutdown(t)
		a2 := s.newApp(t, tn, appOpts{})
		a2.load(300, routes, 20*time.Millisecond)
		a2.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a1, a2))
	})

	t.Run("two replicas without instance id", func(t *testing.T) {
		tn := s.newTenant(t)
		r1 := s.newApp(t, tn, appOpts{})
		r2 := s.newApp(t, tn, appOpts{})
		var wg sync.WaitGroup
		for _, a := range []*app{r1, r2} {
			wg.Go(func() { a.load(800, routes, 15*time.Millisecond) })
		}
		wg.Wait()
		r1.shutdown(t)
		r2.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, r1, r2))
	})

	t.Run("followed app gets a new route and then a new status", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{})
		a.load(200, []string{"/old"}, 20*time.Millisecond)
		a.flush(t)
		// the app outlives the threshold before the new series appear, so
		// only the resource rule can count their first points
		time.Sleep(itThreshold + 2*time.Second)
		a.load(50, []string{"/old"}, 0)
		a.flush(t)
		// the app keeps exporting, so the queue is never empty; wait until
		// the writer has applied everything so far instead
		s.waitStored(t, tn.id, 250, 30*time.Second)
		a.record("/new", 200, 0.02, true)
		a.record("/new", 200, 0.03, true)
		a.flush(t)
		time.Sleep(time.Second)
		a.record("/old", 503, 0.04, true)
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
	})

	t.Run("app running longer than the threshold before the writer sees it", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{closed: true})
		// history the writer never sees: exports are dropped
		for range 400 {
			a.record("/hist", 200, 0.01, false)
		}
		time.Sleep(itThreshold + 2*time.Second)
		for range 100 {
			a.record("/hist", 200, 0.01, false)
		}
		a.gate.open.Store(true)
		// the first export the writer sees carries the whole history and is
		// the baseline; everything recorded after it counts
		a.flush(t)
		a.load(300, []string{"/hist", "/later"}, 10*time.Millisecond)
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
	})

	t.Run("app started an hour ago, raw otlp", func(t *testing.T) {
		tn := s.newTenant(t)
		start := time.Now().Add(-time.Hour)
		send := func(at time.Time, count uint64) {
			md := pmetric.NewMetrics()
			rm := md.ResourceMetrics().AppendEmpty()
			rm.Resource().Attributes().PutStr("service.name", "hourly")
			m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			m.SetName("http.server.request.duration")
			m.SetUnit("s")
			h := m.SetEmptyHistogram()
			h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
			dp := h.DataPoints().AppendEmpty()
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
			dp.SetTimestamp(pcommon.NewTimestampFromTime(at))
			dp.ExplicitBounds().FromRaw(canonicalBounds)
			counts := make([]uint64, 15)
			counts[3] = count
			dp.BucketCounts().FromRaw(counts)
			dp.SetCount(count)
			dp.SetSum(0.04 * float64(count))
			dp.Attributes().PutStr("http.request.method", "GET")
			dp.Attributes().PutStr("http.route", "/hour")
			dp.Attributes().PutInt("http.response.status_code", 200)
			body, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
			if err != nil {
				t.Fatal(err)
			}
			if code := s.postMetrics(t, tn.token, body); code != http.StatusOK {
				t.Fatalf("collector answered %d", code)
			}
		}
		now := time.Now()
		send(now, 36000)                     // an hour of history: the baseline
		send(now.Add(10*time.Second), 36042) // 42 new requests
		send(now.Add(20*time.Second), 36100) // 58 more
		s.expectExact(t, tn, SourceMetric, map[dimKey]int64{{"/hour", 200}: 100})
	})

	t.Run("latency percentiles land in the true percentile's bucket", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{delta: true})
		rng := mrand.New(mrand.NewPCG(7, 11))
		for i := range 5000 {
			// log-uniform from 1 ms to 4 s, so every percentile has data
			// around it; values exactly on a bound are avoided by the draw
			v := math.Exp(math.Log(0.001) + rng.Float64()*(math.Log(4)-math.Log(0.001)))
			a.record("/lat", 200, v, true)
			if i%500 == 499 {
				a.flush(t)
			}
		}
		a.shutdown(t)
		s.expectExact(t, tn, SourceMetric, merge(nil, a))
		vals := slices.Clone(a.vals)
		slices.Sort(vals)
		for _, q := range []float64{0.5, 0.95, 0.99} {
			var est float64
			err := s.super.QueryRow(context.Background(),
				`select api.histogram_quantile($2, api.sum_buckets(counts)) from api.metrics_minute where tenant_id = $1`, tn.id, q).Scan(&est)
			if err != nil {
				t.Fatal(err)
			}
			// nearest rank: the smallest value with at least q*n values at
			// or below it, which is the value whose bucket reaches rank q*n
			truth := vals[int(math.Ceil(q*float64(len(vals))))-1]
			if bucketOf(est) != bucketOf(truth) {
				t.Errorf("p%v: stored %v (bucket %d), true %v (bucket %d)", q*100, est, bucketOf(est), truth, bucketOf(truth))
			} else {
				t.Logf("p%v: stored %.4f s, true %.4f s, bucket %d", q*100, est, truth, bucketOf(truth))
			}
		}
	})

	t.Run("routes beyond the daily cap become other with a warning", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{delta: true})
		for i := range 400 {
			a.record("/cap/"+letters(i), 200, 0.01, true)
			a.record("/cap/"+letters(i), 200, 0.02, true)
		}
		a.shutdown(t)
		s.drained(t, 90*time.Second)
		got, total := s.stored(t, tn.id, SourceMetric)
		if total != 800 {
			t.Fatalf("sum(count) %d, sent 800", total)
		}
		var named int
		for k, n := range got {
			if k.route == Other {
				if n != 200 {
					t.Errorf("{other} holds %d, want 200", n)
				}
				continue
			}
			named++
		}
		if named != itRouteCap {
			t.Fatalf("%d named routes, want %d", named, itRouteCap)
		}
		var rows, warn int64
		if err := s.super.QueryRow(context.Background(),
			`select count(*), coalesce(sum(count), 0)::bigint from api.ingest_warnings where tenant_id = $1 and kind = 'cardinality_overflow'`, tn.id).Scan(&rows, &warn); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || warn == 0 {
			t.Fatalf("cardinality_overflow: %d rows, count %d", rows, warn)
		}
		var registered int64
		if err := s.super.QueryRow(context.Background(),
			`select count(*) from api.route_registry where tenant_id = $1 and route <> '{other}'`, tn.id).Scan(&registered); err != nil {
			t.Fatal(err)
		}
		if registered != itRouteCap {
			t.Fatalf("route registry holds %d named routes", registered)
		}
		t.Logf("400 routes: %d named, {other} 200 requests, one warning row with count %d", named, warn)
	})

	t.Run("secret seed in a path counts a warning and token last seen is stamped", func(t *testing.T) {
		tn := s.newTenant(t)
		a := s.newApp(t, tn, appOpts{delta: true})
		a.record("/keys/"+newSeed(t), 200, 0.01, false)
		a.shutdown(t)
		s.drained(t, 60*time.Second)
		got, total := s.stored(t, tn.id, SourceMetric)
		if total != 1 || got[dimKey{"/keys/{redacted-secret}", 200}] != 1 {
			t.Fatalf("stored %v", got)
		}
		var n int64
		if err := s.super.QueryRow(context.Background(),
			`select coalesce(sum(count), 0)::bigint from api.ingest_warnings where tenant_id = $1 and kind = 'secret_in_path'`, tn.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < 1 {
			t.Fatal("no secret_in_path warning")
		}
		var seen *time.Time
		if err := s.super.QueryRow(context.Background(), `select last_seen_at from cp.otlp_tokens where id = $1`, tn.tokID).Scan(&seen); err != nil {
			t.Fatal(err)
		}
		if seen == nil || time.Since(*seen) > 5*time.Minute {
			t.Fatalf("token last_seen_at %v", seen)
		}
	})

	t.Run("queued messages of a purged tenant or a revoked token write no row", func(t *testing.T) {
		purged := s.newTenant(t)
		revoked := s.newTenant(t)
		live := s.newTenant(t)
		// the writer is stopped, so the exports stay queued on API_METRICS
		w.stop(syscall.SIGINT)
		var queued int
		for _, tn := range []tenant{purged, revoked, live} {
			a := s.newApp(t, tn, appOpts{delta: true})
			a.load(150, routes, 5*time.Millisecond)
			a.shutdown(t)
		}
		info, err := s.admin.Stream(context.Background(), natsx.StreamAPIMetrics)
		if err != nil {
			t.Fatal(err)
		}
		queued = int(info.CachedInfo().State.Msgs)
		ctx := context.Background()
		// the purge as the control plane runs it: removal, then the otlp
		// tokens revoked, then the api rows deleted through api.purge_tenant
		for _, q := range []string{
			`update cp.tenants set status = 'removed', status_changed_at = now() where id = $1`,
			`update cp.otlp_tokens set status = 'revoked', revoked_at = now() where tenant_id = $1 and status = 'active'`,
			`select count(*) from api.purge_tenant($1, false)`,
		} {
			if _, err := s.super.Exec(ctx, q, purged.id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.super.Exec(ctx, `update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = $1`, revoked.tokID); err != nil {
			t.Fatal(err)
		}
		w = s.startWriter(t)
		s.drained(t, 60*time.Second)
		for _, tn := range []tenant{purged, revoked} {
			var n int64
			if err := s.super.QueryRow(ctx, `select count(*) from api.metrics_minute where tenant_id = $1`, tn.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("tenant %s has %d minute rows after its messages were refused", tn.id, n)
			}
		}
		if _, total := s.stored(t, live.id, SourceMetric); total != 150 {
			t.Fatalf("live tenant stored %d, sent 150", total)
		}
		refused := w.metric(t, `metricswriter_messages_total{outcome="refused"}`)
		if refused == 0 {
			t.Fatal("no refused message counted")
		}
		t.Logf("%d messages were queued; %v refused (purged tenant and revoked token), the live tenant's 150 requests stored", queued, refused)
	})

	t.Run("span-derived requests are stored as span rows", func(t *testing.T) {
		tn := s.newTenant(t)
		// a raw otlp/http trace request with server spans, as an sdk sends it
		body := spanPayload(t, 37)
		req, err := http.NewRequest(http.MethodPost, "http://"+s.httpAddr+"/v1/traces", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", "Bearer "+tn.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("traces: %d", resp.StatusCode)
		}
		s.expectExact(t, tn, SourceSpan, map[dimKey]int64{{"/spans", 200}: 37})
		var warn int64
		if err := s.super.QueryRow(context.Background(),
			`select coalesce(sum(count), 0)::bigint from api.ingest_warnings where tenant_id = $1 and kind = 'span_derived'`, tn.id).Scan(&warn); err != nil {
			t.Fatal(err)
		}
		if warn < 1 {
			t.Fatal("no span_derived warning")
		}
	})
}

// commitWithoutAck applies one fetched batch through the writer's own
// transaction code under the metricswriter lock, then hands the messages
// back unacknowledged, as a writer that died between commit and ack would.
func (s *suite) commitWithoutAck(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	env := map[string]string{}
	for _, kv := range s.writerEnv("127.0.0.1:1") {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	cfg := config.FromMap(env)
	set, err := LoadSettings(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pcfg, err := pdb.LoadPoolConfig(cfg, pdb.RoleMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pdb.Open(ctx, pcfg, "metricswriter-it")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	nc := s.conn(t, natsx.ServiceMetricsWriter)
	w, err := New(Config{Settings: set, NC: nc, DB: d, Log: slog.New(slog.DiscardHandler), Registry: obs.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := w.bindLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(natsx.LocksTTL + 10*time.Second)
	for {
		if _, err := lock.Acquire(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("could not take the metricswriter lock")
		}
		time.Sleep(200 * time.Millisecond)
	}
	defer func() { _ = lock.Release(context.Background()) }()
	cons, err := w.js.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(64, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var msgs []jetstream.Msg
	for m := range batch.Messages() {
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return 0
	}
	if _, err := w.apply(ctx, lock.Fence(), msgs); err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if err := m.Nak(); err != nil {
			t.Fatal(err)
		}
	}
	return len(msgs)
}

// bucketOf returns the canonical bucket of a duration.
func bucketOf(v float64) int {
	for i, b := range canonicalBounds {
		if v <= b {
			return i
		}
	}
	return len(canonicalBounds)
}

func letters(i int) string {
	var b []byte
	for {
		b = append([]byte{byte('a' + i%26)}, b...)
		i /= 26
		if i == 0 {
			break
		}
	}
	return "r" + string(b)
}

func newSeed(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	s, err := strkey.Encode(strkey.VersionByteSeed, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// spanPayload is an otlp/http trace request with n server spans of one
// route, as an sdk sends it.
func spanPayload(t *testing.T, n int) []byte {
	t.Helper()
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "traced")
	ss := rs.ScopeSpans().AppendEmpty()
	now := time.Now()
	for i := range n {
		sp := ss.Spans().AppendEmpty()
		var tid [16]byte
		var sid [8]byte
		_, _ = rand.Read(tid[:])
		_, _ = rand.Read(sid[:])
		sp.SetTraceID(tid)
		sp.SetSpanID(sid)
		sp.SetName(fmt.Sprintf("GET /spans %d", i))
		sp.SetKind(ptrace.SpanKindServer)
		sp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-20 * time.Millisecond)))
		sp.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
		sp.Attributes().PutStr("http.request.method", "GET")
		sp.Attributes().PutStr("http.route", "/spans")
		sp.Attributes().PutInt("http.response.status_code", 200)
	}
	b, err := (&ptrace.ProtoMarshaler{}).MarshalTraces(td)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
