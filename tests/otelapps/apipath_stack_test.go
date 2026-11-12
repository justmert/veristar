//go:build apipath

package otelapps

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/tests/fixtures/githuboauth"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// the stack of the api path: postgresql with the platform migrations, umami,
// nats with the production users, the control plane binary (built with the
// oauthfixture tag, the only difference from production being where sign-in
// goes), the built scf-otelcol binary, and deploy/caddy/edge.Caddyfile in a
// container that the example apps reach as otlp.<baseDomain>.

const (
	baseDomain  = "apipath.test"
	otlpHost    = "otlp." + baseDomain
	appOrigin   = "https://app." + baseDomain
	oauthClient = "apipath-client"
	// github id 1 is the only operator (OPERATOR_GITHUB_IDS)
	operatorGitHubID = 1
	composeProject   = "sa-apipath-test"
)

type apiStack struct {
	id    string
	root  string
	creds map[string]string

	natsURL string
	writer  jetstream.JetStream

	cpURL   string
	fixture *httptest.Server
	cp      *exec.Cmd
	cpLogs  *syncBuffer

	httpAddr, grpcAddr, health string
	collector                  *exec.Cmd
	collectorLogs              *syncBuffer

	network string
	edge    string // container name
	edgeTLS string // host:port of the published https listener
	caDir   string // holds root.crt, the edge's local ca
	ca      *x509.CertPool

	terms string

	mu   sync.Mutex
	seen []queued
}

// queued is one message read from API_METRICS.
type queued struct {
	subject string
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

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func key32(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// freeAddrs returns n distinct free ports on every interface, so the edge
// container reaches them through the docker host gateway
func freeAddrs(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for range n {
		l, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		out = append(out, strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
	}
	return out
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func goBuild(t *testing.T, root, out, pkg string, tags ...string) {
	t.Helper()
	args := []string{"build", "-trimpath", "-o", out}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	cmd := exec.Command("go", append(args, pkg)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
}

func waitOK(t *testing.T, u string, timeout time.Duration, logs fmt.Stringer) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		res, err := http.Get(u)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not ready after %s:\n%s", u, timeout, logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startAPIStack(t *testing.T) *apiStack {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(root, "dist", "scf-otelcol", "scf-otelcol")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("collector binary missing (%v); run make collector-build", err)
	}
	s := &apiStack{id: randHex(t, 4), root: root, creds: map[string]string{}, cpLogs: &syncBuffer{}, collectorLogs: &syncBuffer{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// postgresql with the platform migrations and role passwords, as make
	// db-migrate and db-roles do
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

	um := umamictr.Start(t)

	// nats with every service user and its production permissions
	for _, u := range natsx.Users() {
		s.creds[u.Name] = "pw-" + u.Name + "-" + uuid.NewString()
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), s.creds)
	if err != nil {
		t.Fatal(err)
	}
	ns := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	s.natsURL = ns.URL
	connect := func(user string) jetstream.JetStream {
		nc, err := nats.Connect(ns.URL, nats.UserInfo(user, s.creds[user]), nats.CustomInboxPrefix(natsx.InboxPrefix(user)))
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
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(ctx, connect(natsx.ServicePlatformctl), spec); err != nil {
		t.Fatal(err)
	}
	s.writer = connect(natsx.ServiceMetricsWriter)

	// the TEST-ONLY github oauth fixture, registered with the callback url
	// of the console origin
	callback := appOrigin + "/api/auth/github/callback"
	oauthSecret := randHex(t, 20)
	s.fixture = httptest.NewServer(githuboauth.New(githuboauth.Config{
		ClientID: oauthClient, ClientSecret: oauthSecret, RedirectURL: callback,
	}).Handler())
	t.Cleanup(s.fixture.Close)

	// the control plane binary
	binDir := t.TempDir()
	cpBin := filepath.Join(binDir, "controlplane")
	goBuild(t, root, cpBin, "./cmd/controlplane", "oauthfixture")
	ports := freeAddrs(t, 5)
	s.cpURL = "http://127.0.0.1:" + ports[0]
	env := []string{
		"CONTROLPLANE_ADDR=127.0.0.1:" + ports[0],
		"CONTROLPLANE_OPS_ADDR=127.0.0.1:" + ports[1],
		"CONSOLE_ORIGIN=" + appOrigin,
		"INGEST_ORIGIN=https://in." + baseDomain,
		"WEB_ORIGIN=https://web." + baseDomain,
		"GITHUB_CLIENT_ID=" + oauthClient,
		"GITHUB_CLIENT_SECRET=" + oauthSecret,
		"GITHUB_REDIRECT_URL=" + callback,
		"OAUTH_STATE_KEY=" + key32(t),
		"OAUTH_FIXTURE_URL=" + s.fixture.URL,
		"OPERATOR_GITHUB_IDS=" + strconv.Itoa(operatorGitHubID),
		"UMAMI_URL=" + um.URL.String(),
		"UMAMI_ADMIN_PASSWORD=" + um.AdminPassword,
		"UMAMI_USER_PASSWORD_KEY=" + key32(t),
		"TRACKER_RELEASE_DIR=" + filepath.Join(root, "packages", "tracker", "releases"),
		"NATS_URL=" + ns.URL,
		"NATS_REPLICAS=1",
		"NATS_USER=" + natsx.ServiceControlPlane,
		"NATS_PASSWORD=" + s.creds[natsx.ServiceControlPlane],
		"TZ=UTC",
	}
	for k, v := range pg.EnvFor("platform") {
		env = append(env, k+"="+v)
	}
	s.cp = exec.Command(cpBin)
	s.cp.Env = env
	s.cp.Stdout, s.cp.Stderr = s.cpLogs, s.cpLogs
	if err := s.cp.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop(s.cp)
		if t.Failed() {
			t.Logf("control plane log:\n%s", s.cpLogs)
		}
	})
	waitOK(t, "http://127.0.0.1:"+ports[1]+"/readyz", time.Minute, s.cpLogs)

	// the collector binary
	s.httpAddr, s.grpcAddr, s.health = ports[2], ports[3], ports[4]
	s.collector = exec.Command(bin, "--config", filepath.Join(root, "collector", "otelcol.yaml"))
	s.collector.Env = []string{
		"NATS_URL=" + ns.URL,
		"NATS_USER=" + natsx.ServiceCollector,
		"NATS_PASSWORD=" + s.creds[natsx.ServiceCollector],
		"COLLECTOR_GRPC_ADDR=0.0.0.0:" + s.grpcAddr,
		"COLLECTOR_HTTP_ADDR=0.0.0.0:" + s.httpAddr,
		"COLLECTOR_HEALTH_ADDR=0.0.0.0:" + s.health,
		"COLLECTOR_MEMORY_LIMIT_MIB=512",
		"COLLECTOR_MEMORY_SPIKE_MIB=128",
		"COLLECTOR_RATE_REQUESTS_PER_SECOND=200",
		"COLLECTOR_RATE_REQUESTS_BURST=400",
		"COLLECTOR_RATE_ITEMS_PER_SECOND=100000",
		"COLLECTOR_RATE_ITEMS_BURST=200000",
	}
	s.collector.Stdout, s.collector.Stderr = s.collectorLogs, s.collectorLogs
	if err := s.collector.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop(s.collector)
		if t.Failed() {
			t.Logf("collector log:\n%s", s.collectorLogs)
		}
	})
	waitOK(t, "http://127.0.0.1:"+s.health+"/", time.Minute, s.collectorLogs)

	s.startEdge(t)

	terms := struct {
		Version string `json:"version"`
	}{}
	res, err := http.Get(s.cpURL + "/api/terms")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&terms); err != nil || terms.Version == "" {
		t.Fatalf("terms: %v %q", err, terms.Version)
	}
	s.terms = terms.Version
	return s
}

func stop(c *exec.Cmd) {
	if c == nil || c.Process == nil {
		return
	}
	_ = c.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = c.Process.Kill()
		<-done
	}
}

// startEdge runs the edge caddy on a docker network the example apps join
// (examples.yml with apipath.compose.yml), where it answers as otlp.<base
// domain>, and copies out its local ca so the apps verify its certificate.
func (s *apiStack) startEdge(t *testing.T) {
	t.Helper()
	s.network = "sa-apipath-" + s.id
	docker(t, "network", "create", s.network)
	t.Cleanup(func() { _, _ = exec.Command("docker", "network", "rm", s.network).CombinedOutput() })
	s.edge = "sa-apipath-edge-" + s.id
	docker(t, "run", "-d", "--name", s.edge, "--network", s.network, "--network-alias", otlpHost,
		"--add-host", "host.docker.internal:host-gateway", "-p", "127.0.0.1::443",
		"-v", filepath.Join(s.root, "deploy", "caddy", "edge.Caddyfile")+":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN="+baseDomain, "-e", "EDGE_TLS=internal",
		"-e", "GATEWAY_UPSTREAM=gateway.invalid:8080",
		"-e", "COLLECTOR_GRPC_UPSTREAM=host.docker.internal:"+s.grpcAddr,
		"-e", "COLLECTOR_HTTP_UPSTREAM=host.docker.internal:"+s.httpAddr,
		"-e", "COLLECTOR_HEALTH_UPSTREAM=host.docker.internal:"+s.health,
		umamictr.CaddyImage)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", s.edge).CombinedOutput()
			t.Logf("edge caddy log:\n%s", out)
		}
		_, _ = exec.Command("docker", "rm", "-f", "-v", s.edge).CombinedOutput()
	})
	s.edgeTLS = strings.TrimSpace(strings.Split(docker(t, "port", s.edge, "443/tcp"), "\n")[0])

	// the apps run as unprivileged users; the directory and file must be
	// readable by them
	s.caDir = t.TempDir()
	if err := os.Chmod(s.caDir, 0o755); err != nil {
		t.Fatal(err)
	}
	crt := filepath.Join(s.caDir, "root.crt")
	deadline := time.Now().Add(time.Minute)
	for {
		out, err := exec.Command("docker", "cp", s.edge+":/data/caddy/pki/authorities/local/root.crt", crt).CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge ca not created: %s", out)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := os.Chmod(crt, 0o644); err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(crt)
	if err != nil {
		t.Fatal(err)
	}
	s.ca = x509.NewCertPool()
	if !s.ca.AppendCertsFromPEM(pem) {
		t.Fatal("edge root.crt holds no certificate")
	}
	// ready once caddy proxies to a healthy collector: an otlp request
	// without a token reaches the collector, which answers 401
	deadline = time.Now().Add(time.Minute)
	for {
		code, _ := s.otlpPost(t, "/v1/metrics", "", emptyMetrics(t))
		if code == http.StatusUnauthorized {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge not proxying to the collector, last status %d", code)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func emptyMetrics(t *testing.T) []byte {
	t.Helper()
	b, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(pmetric.NewMetrics())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// otlpPost sends one otlp/http protobuf request to https://otlp.<base
// domain> through the edge caddy, verifying caddy's certificate against its
// local ca, as a tenant sdk does against a public one
func (s *apiStack) otlpPost(t *testing.T, path, token string, body []byte) (int, string) {
	t.Helper()
	var d net.Dialer
	c := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.DialContext(ctx, network, s.edgeTLS)
			},
			TLSClientConfig:   &tls.Config{ServerName: otlpHost, RootCAs: s.ca, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: true,
		},
	}
	defer c.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "https://"+otlpHost+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// cookieJar keeps every cookie by name. the session cookie is Secure and the
// control plane is reached over plain http on loopback here, which the
// standard jar would refuse to send back.
type cookieJar struct {
	mu sync.Mutex
	c  map[string]*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cs {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(j.c, c.Name)
			continue
		}
		j.c[c.Name] = c
	}
}

func (j *cookieJar) Cookies(*url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*http.Cookie
	for _, c := range j.c {
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}

// user is one signed-in browser session against the control plane.
type user struct {
	s  *apiStack
	hc *http.Client
}

// signIn runs the real sign-in: start, the fixture's consent, the callback.
func (s *apiStack) signIn(t *testing.T, githubID int64, login string) *user {
	t.Helper()
	u := &user{s: s, hc: &http.Client{
		Jar:           &cookieJar{c: map[string]*http.Cookie{}},
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	res, err := u.hc.Get(s.cpURL + "/api/auth/github/start?return_to=/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("sign-in start: %d", res.StatusCode)
	}
	authz, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", strconv.FormatInt(githubID, 10))
	form.Set("fixture_login", login)
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = plain.PostForm(s.fixture.URL+"/login/oauth/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("fixture consent: %d", res.StatusCode)
	}
	// the fixture redirects to the registered https callback; the same path
	// and query go to the control plane's loopback address
	cb, err := url.Parse(res.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(cb.String(), appOrigin+"/api/auth/github/callback?") {
		t.Fatalf("fixture redirected to %q", res.Header.Get("Location"))
	}
	res, err = u.hc.Get(s.cpURL + cb.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback: %d", res.StatusCode)
	}
	if code, body := u.call(t, http.MethodGet, "/api/me", nil, nil); code != http.StatusOK {
		t.Fatalf("/api/me after sign-in: %d %s", code, body)
	}
	return u
}

// call sends a request the way the console does: json, the csrf header and
// the console origin on state changes. out, when set, receives the json body.
func (u *user) call(t *testing.T, method, path string, body, out any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.s.cpURL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", appOrigin)
	}
	res, err := u.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return res.StatusCode, string(b)
}

// enroll creates a project in the seeded open enrollment mode.
func (u *user) enroll(t *testing.T, name string) (id, status string) {
	t.Helper()
	var tn struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	code, body := u.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name":                   name,
		"enrollment":             map[string]any{"website": "https://example.org", "stellar_usage": "soroban api on testnet"},
		"accepted_terms_version": u.s.terms,
	}, &tn)
	if code != http.StatusCreated {
		t.Fatalf("enroll: %d %s", code, body)
	}
	return tn.ID, tn.Status
}

type createdToken struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Prefix   string `json:"prefix"`
	Status   string `json:"status"`
	Token    string `json:"token"`
}

// newToken creates an otlp token through the control plane api (contract 6.2)
func (u *user) newToken(t *testing.T, tenantID, name string) createdToken {
	t.Helper()
	var tok createdToken
	code, body := u.call(t, http.MethodPost, "/api/tenants/"+tenantID+"/otlp-tokens",
		map[string]any{"name": name, "environment": "apipath"}, &tok)
	if code != http.StatusCreated || tok.TenantID != tenantID || tok.Status != "active" || !strings.HasPrefix(tok.Token, tok.Prefix) {
		t.Fatalf("create token: %d %s", code, body)
	}
	return tok
}

// drain pulls everything on API_METRICS through the metricswriter consumer,
// as the writer will, acks it and records it
func (s *apiStack) drain(t *testing.T) {
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
			s.seen = append(s.seen, queued{subject: m.Subject(), data: append([]byte(nil), m.Data()...), md: md})
			s.mu.Unlock()
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

// messages returns every message read so far whose resource carries the
// given token id.
func (s *apiStack) messages(tokenID string) []queued {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []queued
	for _, q := range s.seen {
		rms := q.md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			if v, ok := rms.At(i).Resource().Attributes().Get("scf.token.id"); ok && v.Str() == tokenID {
				out = append(out, q)
				break
			}
		}
	}
	return out
}
