//go:build collectorit

// Package collector_test runs the built scf-otelcol binary against a real
// nats server with the production user permissions, and drives it with the
// pinned opentelemetry go sdk exporters. run it
// with `make collector-it`, which builds the binary first.
package collector_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stellar/go-stellar-sdk/strkey"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/otelcolcfg"
)

// limits the collector runs with in this suite. the request burst is small
// enough for the rate limit check to exhaust it quickly and large enough
// for every other scenario of one tenant.
const (
	rateRequestsPerSecond = 2
	rateRequestsBurst     = 30
)

type stack struct {
	nats     *natsctr.Server
	creds    map[string]string
	pub      *registry.Publisher
	writer   jetstream.JetStream // metricswriter user, drains API_METRICS as the writer will
	httpAddr string
	grpcAddr string
	health   string
	logs     *syncBuffer
	cmd      *exec.Cmd

	mu   sync.Mutex
	seen []received // every message read from API_METRICS
}

type received struct {
	subject string
	msgID   string
	data    []byte
	md      pmetric.Metrics
}

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

// freeAddrs returns n distinct free loopback addresses. the listeners stay
// open until all are chosen, so the kernel cannot hand out one port twice.
func freeAddrs(t *testing.T, n int) []string {
	t.Helper()
	var lc net.ListenConfig
	var out []string
	for range n {
		l, err := lc.Listen(context.Background(), "tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		out = append(out, "127.0.0.1:"+port(l.Addr().String()))
	}
	return out
}

func repoRoot(t *testing.T) string { return natsctr.RepoRoot(t) }

func startStack(t *testing.T) *stack {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoRoot(t), "dist", "scf-otelcol", "scf-otelcol")); err != nil {
		t.Fatalf("collector binary missing (%v); run make collector-build", err)
	}
	s := &stack{creds: map[string]string{}, logs: &syncBuffer{}}
	for _, u := range natsx.Users() {
		s.creds[u.Name] = "pw-" + u.Name + "-" + uuid.NewString()
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), s.creds)
	if err != nil {
		t.Fatal(err)
	}
	s.nats = natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	connect := func(user string) jetstream.JetStream {
		nc, err := nats.Connect(s.nats.URL, nats.UserInfo(user, s.creds[user]), nats.CustomInboxPrefix(natsx.InboxPrefix(user)),
			nats.MaxReconnects(-1), nats.ReconnectWait(200*time.Millisecond))
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
	admin := connect(natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := natsx.Apply(ctx, admin, spec); err != nil {
		t.Fatal(err)
	}
	s.writer = connect(natsx.ServiceMetricsWriter)
	s.pub, err = registry.NewPublisher(ctx, connect(natsx.ServiceControlPlane))
	if err != nil {
		t.Fatal(err)
	}

	// the listeners bind every interface so the edge caddy container can
	// reach them through the docker host gateway; the test uses loopback
	addrs := freeAddrs(t, 3)
	s.httpAddr, s.grpcAddr, s.health = addrs[0], addrs[1], addrs[2]
	s.startCollector(t)
	t.Cleanup(func() {
		s.stopCollector(t)
		if t.Failed() {
			t.Logf("collector log:\n%s", s.logs.String())
		}
	})
	return s
}

func port(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

// startCollector runs the binary and waits until health_check reports
// ready, which happens only after the token snapshot is loaded.
func (s *stack) startCollector(t *testing.T) {
	t.Helper()
	bin := filepath.Join(repoRoot(t), "dist", "scf-otelcol", "scf-otelcol")
	// the suite never reads the collector's own metrics; without them it
	// does not need the fixed port 8888, which another collector on the
	// host may hold
	cmd := exec.Command(bin, "--config", filepath.Join(repoRoot(t), "collector", "otelcol.yaml"),
		"--config", otelcolcfg.NoSelfMetrics(t))
	cmd.Env = []string{
		"NATS_URL=" + s.nats.URL,
		"NATS_USER=collector",
		"NATS_PASSWORD=" + s.creds["collector"],
		"COLLECTOR_GRPC_ADDR=0.0.0.0:" + port(s.grpcAddr),
		"COLLECTOR_HTTP_ADDR=0.0.0.0:" + port(s.httpAddr),
		"COLLECTOR_HEALTH_ADDR=0.0.0.0:" + port(s.health),
		"COLLECTOR_MEMORY_LIMIT_MIB=512",
		"COLLECTOR_MEMORY_SPIKE_MIB=128",
		"COLLECTOR_RATE_REQUESTS_PER_SECOND=" + strconv.Itoa(rateRequestsPerSecond),
		"COLLECTOR_RATE_REQUESTS_BURST=" + strconv.Itoa(rateRequestsBurst),
		"COLLECTOR_RATE_ITEMS_PER_SECOND=100000",
		"COLLECTOR_RATE_ITEMS_BURST=200000",
	}
	cmd.Stdout, cmd.Stderr = s.logs, s.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := http.Get("http://" + s.health + "/")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector not healthy:\n%s", s.logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopCollector sends an interrupt and waits for a clean exit.
func (s *stack) stopCollector(t *testing.T) {
	t.Helper()
	if s.cmd == nil {
		return
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	s.cmd = nil
}

// tenant is one registered tenant with one active token.
type tenant struct {
	ID    string
	Token string
	entry registry.OTLPToken
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

// newTenant writes a token entry straight into registry_otlp, as the
// control plane will, and waits until the collector accepts it.
func (s *stack) newTenant(t *testing.T) tenant {
	t.Helper()
	tok := randomToken(t)
	tn := tenant{ID: uuid.NewString(), Token: tok}
	tn.entry = registry.OTLPToken{Hash: registry.HashOTLPToken(tok), TenantID: tn.ID, TokenID: uuid.NewString(), Status: registry.StatusActive}
	if err := s.pub.PutOTLPToken(context.Background(), tn.entry); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, _ := s.postMetrics(t, tok, emptyMetrics(), "")
		if code == http.StatusOK {
			return tn
		}
		if time.Now().After(deadline) {
			t.Fatalf("token not accepted, last status %d", code)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func emptyMetrics() []byte {
	b, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(pmetric.NewMetrics())
	return b
}

// postMetrics sends a raw otlp/http protobuf request.
func (s *stack) postMetrics(t *testing.T, token string, body []byte, encoding string) (int, string) {
	t.Helper()
	return s.post(t, "/v1/metrics", token, body, encoding)
}

func (s *stack) post(t *testing.T, path, token string, body []byte, encoding string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+s.httpAddr+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// drain pulls everything currently on API_METRICS through the
// metricswriter consumer, acks it and records it.
func (s *stack) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := s.writer.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	for {
		batch, err := c.Fetch(100, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for m := range batch.Messages() {
			n++
			md, err := (&pmetric.ProtoUnmarshaler{}).UnmarshalMetrics(m.Data())
			if err != nil {
				t.Fatalf("message on %s does not decode: %v", m.Subject(), err)
			}
			s.mu.Lock()
			s.seen = append(s.seen, received{subject: m.Subject(), msgID: m.Headers().Get(jetstream.MsgIDHeader), data: append([]byte(nil), m.Data()...), md: md})
			s.mu.Unlock()
			// double ack: the server confirms, so a stopped server cannot
			// lose the ack and redeliver the message later
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if batch.Error() != nil {
			t.Fatal(batch.Error())
		}
		if n == 0 {
			return
		}
	}
}

// point is one histogram data point with its resource and metric.
type point struct {
	resource pcommon.Map
	metric   pmetric.Metric
	dp       pmetric.HistogramDataPoint
}

// points returns the data points of one metric for one tenant from every
// message read so far.
func (s *stack) points(tenantID, metric string) []point {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []point
	for _, r := range s.seen {
		if r.subject != natsx.Subject(natsx.SubjectAPIMetrics, tenantID) {
			continue
		}
		rms := r.md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					m := ms.At(k)
					if m.Name() != metric || m.Type() != pmetric.MetricTypeHistogram {
						continue
					}
					dps := m.Histogram().DataPoints()
					for l := 0; l < dps.Len(); l++ {
						out = append(out, point{resource: rm.Resource().Attributes(), metric: m, dp: dps.At(l)})
					}
				}
			}
		}
	}
	return out
}

// waitPoints drains until want reports success or the timeout passes.
func (s *stack) waitPoints(t *testing.T, tenantID, metric string, timeout time.Duration, want func([]point) bool) []point {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s.drain(t)
		p := s.points(tenantID, metric)
		if want(p) {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition on %s for tenant %s not met; %d points: %s", metric, tenantID, len(p), describe(p))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func describe(ps []point) string {
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "\n  %s count=%d attrs=%v res=%v", p.metric.Name(), p.dp.Count(), p.dp.Attributes().AsRaw(), p.resource.AsRaw())
	}
	return b.String()
}

func totalCount(ps []point) uint64 {
	var n uint64
	for _, p := range ps {
		n += p.dp.Count()
	}
	return n
}

// newSeed returns a fresh, valid secret seed, so the scan below cannot be
// satisfied by a fixed string that happens to be filtered elsewhere.
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

func newAccount(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func freeListener(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}
