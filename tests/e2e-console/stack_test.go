//go:build consolee2e

// Package consolee2e runs the console browser flows against real services in
// containers this test owns: postgresql with the platform migrations, nats with the topology, umami
// (bootstrapped), the control plane built with the oauthfixture tag and
// pointed at the TEST-ONLY github oauth fixture (tests/fixtures/githuboauth),
// the replayer, the gateway image behind the edge caddy and the console's
// static build behind the app caddy; umami stays on the private network, as
// in production. for
// the api analytics flows the stack adds the collector image and
// the metricswriter behind otlp.<base domain> on the same edge caddy, a
// stock collector as the tenants' own backend, and a loopback control api
// that starts the go example app with a token the spec created. the
// browsers reach the edge through a forwarder on a db-ip german range, so the
// country panel has a real value. an alertmanager member behind mutual tls
// feeds the operator alerts page (alertmanager_test.go). the playwright half
// lives in this directory (specs/). run it with `make console-e2e`.
package consolee2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/stellar/go-stellar-sdk/keypair"

	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/gatewayimage"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

const (
	baseDomain   = "sa-test.example"
	gatewayImage = "veristar/gateway:console-e2e"
	// the collector distribution (deploy/images/collector.Dockerfile) and
	// the go example app (examples/otel-go-nethttp), built in TestMain
	collectorImage = "veristar/collector:console-e2e"
	exampleImage   = "veristar/otel-go-nethttp:console-e2e"
	// the stock collector of deploy/compose/examples.yml, standing in for a
	// tenant's own observability backend
	tenantBackendImage = "otel/opentelemetry-collector-contrib:0.165.0@sha256:e40d61f10c9a617cca1ffd1be4d7e2b3a4b0f0001798ff07e58cabe82e618cea"
	staticImage      = "gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931"
	// the oauth fixture's host as the browser and the control plane see it.
	// plain http: the control plane trusts only the system roots, and the
	// fixture needs no certificate to stand in for github's endpoints
	oauthHost = "oauth." + baseDomain
	oauthPort = "8080"
	// a fixed test client; the secret is random per run
	oauthClientID = "ce2e-test-client"
)

var gatewayEnv = map[string]string{
	"NATS_URL":                 "nats://nats:4222",
	"NATS_REPLICAS":            "1",
	"GATEWAY_NODE_ID":          "edge-1",
	"GATEWAY_ADDR":             "0.0.0.0:8080",
	"GATEWAY_OPS_ADDR":         "0.0.0.0:9090",
	"PROOF_BITS":               "12",
	"PROOF_REPLAY_LIMIT":       "50",
	"IPCLUSTER_THRESHOLD":      "20",
	"VISITOR_EVENTS_PER_HOUR":  "600",
	"RATE_SITE_PER_SECOND":     "200",
	"RATE_SITE_BURST":          "400",
	"RATE_PREFIX_PER_SECOND":   "20",
	"RATE_PREFIX_BURST":        "60",
	"GATEWAY_MAX_BODY":         "8192",
	"CLIENT_IP_HEADER":         "X-Edge-Client-Ip",
	"GATEWAY_SECURE_TRANSPORT": "true",
	"TZ":                       "UTC",
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// binaries built once in TestMain
var bins struct {
	controlplane, jobs, replayer, metricswriter, platformctl, fwd, oauth string
	dir                                                                  string
}

func TestMain(m *testing.M) {
	code, err := setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "console-e2e:", err)
		os.Exit(1)
	}
	if code >= 0 {
		os.Exit(code)
	}
	code = m.Run()
	_ = os.RemoveAll(bins.dir)
	os.Exit(code)
}

func setup() (int, error) {
	month := os.Getenv("DBIP_MONTH")
	if month == "" {
		fmt.Fprintln(os.Stderr, "console-e2e: DBIP_MONTH (YYYY-MM) is required; make console-e2e sets it")
		return 2, nil
	}
	if out, err := exec.Command("node", "--version").Output(); err != nil || !strings.HasPrefix(string(out), "v26.") {
		return 0, fmt.Errorf("node 26 is required on PATH (source ~/.nvm/nvm.sh && nvm use 26.13.1): %q %w", out, err)
	}
	builds := [][]string{
		gatewayimage.BuildArgs(month, gatewayImage),
		{"build", "-q", "-f", "deploy/images/collector.Dockerfile", "-t", collectorImage, "."},
		{"build", "-q", "-t", exampleImage, "examples/otel-go-nethttp"},
	}
	for _, b := range builds {
		cmd := exec.Command("docker", b...)
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("docker %s: %w\n%s", strings.Join(b, " "), err, out)
		}
	}
	dir, err := os.MkdirTemp("", "sa-ce2e-bin-")
	if err != nil {
		return 0, err
	}
	bins.dir = dir
	// linux static binaries, mounted into the distroless image; the control
	// plane carries the oauthfixture tag, the only build that reads
	// OAUTH_FIXTURE_URL
	goBuilds := []struct {
		out  *string
		pkg  string
		tags string
	}{
		{&bins.controlplane, "./cmd/controlplane", "oauthfixture"},
		{&bins.jobs, "./cmd/jobs", ""},
		{&bins.replayer, "./cmd/replayer", ""},
		{&bins.metricswriter, "./cmd/metricswriter", ""},
		{&bins.fwd, "./tests/webpath/fwd", ""},
		{&bins.oauth, "./tests/fixtures/githuboauth/cmd/githuboauth-fixture", ""},
	}
	for _, b := range goBuilds {
		*b.out = filepath.Join(dir, filepath.Base(b.pkg))
		args := []string{"build", "-trimpath", "-o", *b.out}
		if b.tags != "" {
			args = append(args, "-tags", b.tags)
		}
		args = append(args, b.pkg)
		cmd := exec.Command("go", args...)
		cmd.Dir = repoRoot()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		if out, err := cmd.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("go build %s: %w\n%s", b.pkg, err, out)
		}
		// the distroless image runs as nonroot; the mounted file must be readable
		if err := os.Chmod(*b.out, 0o755); err != nil {
			return 0, err
		}
	}
	// platformctl runs on this host against the published postgres port
	bins.platformctl = filepath.Join(dir, "platformctl-host")
	cmd := exec.Command("go", "build", "-trimpath", "-o", bins.platformctl, "./cmd/platformctl")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("go build platformctl: %w\n%s", err, out)
	}
	// the production build first: it must hold no trace of the
	// deterministic wallet test signer
	cmd = exec.Command("pnpm", "--filter", "@veristar/console", "build")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("console build: %w\n%s", err, out)
	}
	if err := noTestSigner(filepath.Join(repoRoot(), "apps", "console", "dist", "assets")); err != nil {
		return 0, err
	}
	// the console's static build served by the app caddy: the same build
	// with the test signer, so the wallet specs sign in without an extension
	cmd = exec.Command("pnpm", "--filter", "@veristar/console", "build")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "VITE_STELLAR_TEST_SIGNER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("console build with the test signer: %w\n%s", err, out)
	}
	return -1, nil
}

// testSignerID is the product id of apps/console/src/lib/stellar-wallet/test-signer.ts
const testSignerID = "veristar-test-signer"

// noTestSigner fails when a production asset holds the test signer.
func noTestSigner(assets string) error {
	files, err := os.ReadDir(assets)
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(assets, f.Name()))
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(testSignerID)) {
			return fmt.Errorf("the production console build holds the test signer in %s", f.Name())
		}
	}
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// key32 is a random 32-byte key in standard base64, as the control plane's
// key settings expect
func key32() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func docker(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String()), nil
}

func mustDocker(t testing.TB, args ...string) string {
	t.Helper()
	out, err := docker(args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func freePort(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func waitHTTP(t testing.TB, u string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		resp, err := http.Get(u)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s not ready after %s: %s", u, timeout, last)
}

func envOpts(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, "-e", k+"="+v)
	}
	return out
}

// Stack is every container of one run.
type Stack struct {
	ID     string
	Net    string
	Subnet netip.Prefix

	PGPort     string
	PostgresPW string
	Postgres   string
	DBEnv      map[string]string

	NC *nats.Conn
	JS jetstream.JetStream

	UmamiURL *url.URL
	UmamiPW  string

	OAuthSecret string
	OAuthCtl    string

	Edge string
	// Jobs is the jobs daemon's container and JobsOps its published ops port
	Jobs    string
	JobsOps string
	// CADir holds root.crt, the edge caddy's local ca, for the example apps
	CADir string
	// Routes maps host:port as the browser asks for it to the loopback
	// address of the published container port
	Routes map[string]string

	// AM is the alertmanager of the operator alerts page
	AM *amStack

	names []string
	nets  []string
	// names of containers the apps control api started, from its goroutines
	mu sync.Mutex
}

func newStack(t testing.TB) *Stack {
	t.Helper()
	s := &Stack{ID: randHex(4), Routes: map[string]string{}}
	t.Cleanup(func() { s.cleanup(t) })
	s.Net = "sa-ce-" + s.ID
	var err error
	for range 20 {
		n, _ := rand.Int(rand.Reader, big.NewInt(250))
		s.Subnet = netip.MustParsePrefix(fmt.Sprintf("10.216.%d.0/24", n.Int64()+1))
		if _, err = docker("network", "create", "--subnet", s.Subnet.String(), s.Net); err == nil {
			s.nets = append(s.nets, s.Net)
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *Stack) cleanup(t testing.TB) {
	// logs of every container, for diagnosis after a failure
	if t.Failed() || os.Getenv("CONSOLE_E2E_LOGS") != "" {
		dir := outDir()
		s.mu.Lock()
		all := append([]string(nil), s.names...)
		s.mu.Unlock()
		for _, n := range all {
			out, _ := exec.Command("docker", "logs", n).CombinedOutput()
			_ = os.WriteFile(filepath.Join(dir, n+".log"), out, 0o644)
		}
	}
	if os.Getenv("CONSOLE_E2E_KEEP") != "" {
		return
	}
	s.mu.Lock()
	names := append([]string(nil), s.names...)
	s.mu.Unlock()
	for i := len(names) - 1; i >= 0; i-- {
		_, _ = docker("rm", "-f", "-v", names[i])
	}
	for _, n := range s.nets {
		_, _ = docker("network", "rm", n)
	}
}

func (s *Stack) run(t testing.TB, name string, opts []string, image string, args ...string) {
	t.Helper()
	all := append([]string{"run", "-d", "--name", name, "--network", s.Net}, opts...)
	all = append(all, image)
	all = append(all, args...)
	mustDocker(t, all...)
	s.mu.Lock()
	s.names = append(s.names, name)
	s.mu.Unlock()
}

// mounted runs a static binary from bins on the distroless image.
func (s *Stack) mounted(t testing.TB, name, bin string, opts []string, env map[string]string, args ...string) {
	t.Helper()
	all := append([]string{"-v", bin + ":/bin/service:ro", "--entrypoint", "/bin/service"}, opts...)
	all = append(all, envOpts(env)...)
	s.run(t, name, all, staticImage, args...)
}

var (
	natsImageLine = regexp.MustCompile(`(?m)^\s*image:\s*(nats:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
)

func devImage(t testing.TB, re *regexp.Regexp) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), "deploy", "compose", "dev.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := re.FindSubmatch(b)
	if m == nil {
		t.Fatalf("dev.yml has no image matching %s", re)
	}
	return string(m[1])
}

// StartPostgres runs postgresql with the dev init script (databases umami
// and platform), then applies the platform migrations and role passwords
// with platformctl, as make db-migrate and db-roles do.
func (s *Stack) StartPostgres(t testing.TB) {
	t.Helper()
	_, pgImage := umamictr.Images(t)
	s.PostgresPW = randHex(24)
	umamiPW := randHex(24)
	s.PGPort = freePort(t)
	s.Postgres = "sa-ce-postgres-" + s.ID
	s.run(t, s.Postgres, append([]string{
		"--network-alias", "postgres", "-p", "127.0.0.1:" + s.PGPort + ":5432",
		"-v", filepath.Join(repoRoot(), "deploy", "compose", "postgres-init.sh") + ":/docker-entrypoint-initdb.d/10-init.sh:ro",
	}, envOpts(map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": s.PostgresPW, "UMAMI_DB_PASSWORD": umamiPW, "TZ": "UTC",
	})...), pgImage)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := docker("exec", s.Postgres, "psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-tAc",
			"select count(*) from pg_database where datname in ('umami','platform')")
		if err == nil && out == "2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("postgres not ready: %v %q", err, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.DBEnv = map[string]string{
		"PLATFORM_DB_NAME":    "platform",
		"PLATFORM_DB_SSLMODE": "disable",
	}
	for _, r := range []string{"CP_SYSTEM", "CP_TENANT", "CP_OPERATOR", "METRICS_WRITER", "JOBS"} {
		s.DBEnv["PLATFORM_DB_"+r+"_PASSWORD"] = randHex(24)
	}
	host := map[string]string{
		"PLATFORM_DB_HOST": "127.0.0.1", "PLATFORM_DB_PORT": s.PGPort,
		"PLATFORM_DB_ADMIN_USER": "postgres", "PLATFORM_DB_ADMIN_PASSWORD": s.PostgresPW,
	}
	for _, sub := range [][]string{{"db", "migrate"}, {"db", "roles"}} {
		cmd := exec.Command(bins.platformctl, sub...)
		cmd.Env = os.Environ()
		for k, v := range s.DBEnv {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		for k, v := range host {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("platformctl %v: %v\n%s", sub, err, out)
		}
	}
	// containers reach postgres on the private network
	s.DBEnv["PLATFORM_DB_HOST"] = "postgres"
	s.DBEnv["PLATFORM_DB_PORT"] = "5432"
	s.startUmami(t, umamiPW)
}

func (s *Stack) startUmami(t testing.TB, dbPW string) {
	t.Helper()
	umamiImage, _ := umamictr.Images(t)
	name := "sa-ce-umami-" + s.ID
	port := freePort(t)
	// the umami environment of deploy/compose/dev.yml
	s.run(t, name, append([]string{"--network-alias", "umami", "-p", "127.0.0.1:" + port + ":3000"},
		envOpts(map[string]string{
			"DATABASE_URL":       "postgresql://umami_app:" + dbPW + "@postgres:5432/umami",
			"APP_SECRET":         randHex(24),
			"CLIENT_IP_HEADER":   "x-edge-client-ip",
			"ENABLE_CLIENT_SALT": "1",
			"SALT_ROTATION":      "daily",
			"PRIVATE_MODE":       "1",
			"DISABLE_TELEMETRY":  "1",
			"DISABLE_UPDATES":    "1",
			"DISABLE_LOGIN":      "1",
			"TZ":                 "UTC",
		})...), umamiImage)
	s.UmamiURL = &url.URL{Scheme: "http", Host: "127.0.0.1:" + port}
	waitHTTP(t, s.UmamiURL.String()+"/api/heartbeat", 5*time.Minute)
	c, err := umami.New(s.UmamiURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.UmamiPW = randHex(24)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := umami.Bootstrap(ctx, c, s.UmamiPW); err != nil {
		t.Fatal(err)
	}
}

// StartNATS runs nats with jetstream and applies the topology.
func (s *Stack) StartNATS(t testing.TB) {
	t.Helper()
	name := "sa-ce-nats-" + s.ID
	port, mon := freePort(t), freePort(t)
	s.run(t, name, []string{"--network-alias", "nats", "-p", "127.0.0.1:" + port + ":4222", "-p", "127.0.0.1:" + mon + ":8222"},
		devImage(t, natsImageLine), "--name", "ce", "--jetstream", "--store_dir", "/data", "--http_port", "8222")
	waitHTTP(t, "http://127.0.0.1:"+mon+"/healthz?js-enabled-only=true", time.Minute)
	var err error
	s.NC, err = nats.Connect("nats://127.0.0.1:"+port, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.NC.Close)
	if s.JS, err = jetstream.New(s.NC); err != nil {
		t.Fatal(err)
	}
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(context.Background(), s.JS, spec); err != nil {
		t.Fatal(err)
	}
}

// StartOAuthFixture runs the TEST-ONLY github oauth fixture under its test
// host name, which the control plane resolves on the private network and
// the browser through the proxy.
func (s *Stack) StartOAuthFixture(t testing.TB) {
	t.Helper()
	s.OAuthSecret = randHex(20)
	port := freePort(t)
	s.mounted(t, "sa-ce-oauth-"+s.ID, bins.oauth,
		[]string{"--network-alias", oauthHost, "-p", "127.0.0.1:" + port + ":" + oauthPort}, nil,
		"-listen", ":"+oauthPort, "-client-id", oauthClientID, "-client-secret", s.OAuthSecret,
		"-redirect-url", "https://app."+baseDomain+"/api/auth/github/callback")
	s.OAuthCtl = "http://127.0.0.1:" + port
	waitHTTP(t, s.OAuthCtl+"/_fixture/healthz", 30*time.Second)
	s.Routes[oauthHost+":"+oauthPort] = "127.0.0.1:" + port
}

// common is the environment shared by the control plane and jobs.
func (s *Stack) common(userPWKey string) map[string]string {
	env := map[string]string{
		"NATS_URL":                "nats://nats:4222",
		"NATS_REPLICAS":           "1",
		"UMAMI_URL":               "http://umami:3000",
		"UMAMI_ADMIN_PASSWORD":    s.UmamiPW,
		"UMAMI_USER_PASSWORD_KEY": userPWKey,
		"TRACKER_RELEASE_DIR":     "/releases",
		// the control plane switches api metric tables at the horizons jobs
		// keeps (control-plane-api 7.11), so both take the same values
		"API_MINUTE_RETENTION_DAYS":   "30",
		"API_HOURLY_RETENTION_MONTHS": "36",
		"TZ":                          "UTC",
	}
	for k, v := range s.DBEnv {
		env[k] = v
	}
	return env
}

// StartControlPlane runs cmd/controlplane built with the oauthfixture tag,
// and jobs with the same umami user password key.
func (s *Stack) StartControlPlane(t testing.TB) {
	t.Helper()
	userKey := key32()
	env := s.common(userKey)
	reportKey := keypair.MustRandom()
	for k, v := range map[string]string{
		"CONTROLPLANE_ADDR":     "0.0.0.0:8081",
		"CONTROLPLANE_OPS_ADDR": "0.0.0.0:9090",
		"CONSOLE_ORIGIN":        "https://app." + baseDomain,
		"INGEST_ORIGIN":         "https://in." + baseDomain,
		"GITHUB_CLIENT_ID":      oauthClientID,
		"GITHUB_CLIENT_SECRET":  s.OAuthSecret,
		"GITHUB_REDIRECT_URL":   "https://app." + baseDomain + "/api/auth/github/callback",
		"OAUTH_STATE_KEY":       key32(),
		// the usage report key of this stack (contract 20.2)
		"REPORT_SIGNING_SEED": reportKey.Seed(),
		"REPORT_SIGNING_KEY":  reportKey.Address(),
		"OAUTH_FIXTURE_URL":   "http://" + oauthHost + ":" + oauthPort,
		// an id no test user takes; the tenant flows run as tenants
		"OPERATOR_GITHUB_IDS": "1",
	} {
		env[k] = v
	}
	for k, v := range s.alertmanagerEnv() {
		env[k] = v
	}
	releases := filepath.Join(repoRoot(), "packages", "tracker", "releases") + ":/releases:ro"
	ops := freePort(t)
	s.mounted(t, "sa-ce-controlplane-"+s.ID, bins.controlplane,
		[]string{"--network-alias", "controlplane", "-p", "127.0.0.1:" + ops + ":9090", "-v", releases,
			"-v", s.AM.clientDir + ":" + amClientDir + ":ro"}, env)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)

	s.JobsOps = freePort(t)
	s.Jobs = "sa-ce-jobs-" + s.ID
	jenv := s.common(userKey)
	jenv["JOBS_OPS_ADDR"] = "0.0.0.0:9090"
	s.mounted(t, s.Jobs, bins.jobs,
		[]string{"-p", "127.0.0.1:" + s.JobsOps + ":9090", "-v", releases}, jenv, "daemon")
	waitHTTP(t, "http://127.0.0.1:"+s.JobsOps+"/healthz", time.Minute)
}

// StartReplayer drains WEB_EVENTS into umami.
func (s *Stack) StartReplayer(t testing.TB) {
	t.Helper()
	ops := freePort(t)
	s.mounted(t, "sa-ce-replayer-"+s.ID, bins.replayer, []string{"-p", "127.0.0.1:" + ops + ":9090"}, map[string]string{
		"NATS_URL":               "nats://nats:4222",
		"NATS_REPLICAS":          "1",
		"REPLAYER_NODE_ID":       "core-1",
		"REPLAYER_OPS_ADDR":      "0.0.0.0:9090",
		"UMAMI_URL":              "http://umami:3000",
		"REPLAYER_WORKERS":       "16",
		"REPLAYER_MAX_IN_FLIGHT": "512",
		"TZ":                     "UTC",
	})
	waitHTTP(t, "http://127.0.0.1:"+ops+"/healthz", time.Minute)
}

// StartEdge runs the gateway image and the edge caddy, and a forwarder on a
// german db-ip range that carries the browsers' tls to caddy, so caddy's tcp
// peer, and with it the gateway's country lookup, is a german address.
func (s *Stack) StartEdge(t testing.TB) {
	t.Helper()
	gw := "sa-ce-gateway-" + s.ID
	ops := freePort(t)
	s.run(t, gw, append([]string{"--network-alias", "edge-1", "-p", "127.0.0.1:" + ops + ":9090"}, envOpts(gatewayEnv)...), gatewayImage)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)

	s.Edge = "sa-ce-edge-" + s.ID
	s.run(t, s.Edge, []string{
		"--network-alias", "in." + baseDomain, "--network-alias", "otlp." + baseDomain,
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "GATEWAY_UPSTREAM=edge-1:8080", "-e", "EDGE_TLS=internal",
		"-e", "COLLECTOR_GRPC_UPSTREAM=collector:4317", "-e", "COLLECTOR_HTTP_UPSTREAM=collector:4318",
		"-e", "COLLECTOR_HEALTH_UPSTREAM=collector:13133",
	}, umamictr.CaddyImage)
	s.copyEdgeCA(t)

	subnets := countrySubnets(t, "DE")
	name := "sa-ce-de-" + s.ID
	var subnet netip.Prefix
	var err error
	for _, p := range subnets {
		if _, err = docker("network", "create", "--subnet", p.String(), name); err == nil {
			subnet = p
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	s.nets = append(s.nets, name)
	b := subnet.Addr().As4()
	caddyIP := netip.AddrFrom4([4]byte{b[0], b[1], b[2], 3})
	clientIP := netip.AddrFrom4([4]byte{b[0], b[1], b[2], 10})
	mustDocker(t, "network", "connect", "--ip", caddyIP.String(), name, s.Edge)

	fwd := "sa-ce-fwd-" + s.ID
	tlsPort, ctlPort := freePort(t), freePort(t)
	mustDocker(t, "run", "-d", "--name", fwd, "--network", name, "--ip", clientIP.String(),
		"-p", "127.0.0.1:"+tlsPort+":8443", "-p", "127.0.0.1:"+ctlPort+":8080",
		"-v", bins.fwd+":/fwd:ro", "--entrypoint", "/fwd", staticImage,
		"-listen", ":8443", "-control", ":8080", "-initial", "DE", "-route", "DE="+caddyIP.String()+":443")
	s.names = append(s.names, fwd)
	waitHTTP(t, "http://127.0.0.1:"+ctlPort+"/", 30*time.Second)
	s.Routes["in."+baseDomain+":443"] = "127.0.0.1:" + tlsPort
}

// copyEdgeCA copies the edge caddy's local root certificate out, so the
// example apps can trust otlp.<base domain> as tenant apps trust a public ca.
func (s *Stack) copyEdgeCA(t testing.TB) {
	t.Helper()
	s.CADir = t.TempDir()
	// the app runs as nonroot; the directory and file must be readable
	if err := os.Chmod(s.CADir, 0o755); err != nil {
		t.Fatal(err)
	}
	crt := filepath.Join(s.CADir, "root.crt")
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := docker("cp", s.Edge+":/data/caddy/pki/authorities/local/root.crt", crt)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge ca not created: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := os.Chmod(crt, 0o644); err != nil {
		t.Fatal(err)
	}
}

// StartCollector runs the collector image as the edge's otlp upstream. the
// dev nats server has no auth, so user and password stay empty
// (collector/otelcol.yaml).
func (s *Stack) StartCollector(t testing.TB) {
	t.Helper()
	health := freePort(t)
	s.run(t, "sa-ce-collector-"+s.ID, append([]string{"--network-alias", "collector", "-p", "127.0.0.1:" + health + ":13133"},
		envOpts(map[string]string{
			"NATS_URL":                           "nats://nats:4222",
			"NATS_USER":                          "",
			"NATS_PASSWORD":                      "",
			"COLLECTOR_GRPC_ADDR":                "0.0.0.0:4317",
			"COLLECTOR_HTTP_ADDR":                "0.0.0.0:4318",
			"COLLECTOR_HEALTH_ADDR":              "0.0.0.0:13133",
			"COLLECTOR_MEMORY_LIMIT_MIB":         "512",
			"COLLECTOR_MEMORY_SPIKE_MIB":         "128",
			"COLLECTOR_RATE_REQUESTS_PER_SECOND": "200",
			"COLLECTOR_RATE_REQUESTS_BURST":      "400",
			"COLLECTOR_RATE_ITEMS_PER_SECOND":    "100000",
			"COLLECTOR_RATE_ITEMS_BURST":         "200000",
			"TZ":                                 "UTC",
		})...), collectorImage)
	waitHTTP(t, "http://127.0.0.1:"+health+"/", 2*time.Minute)
}

// StartMetricsWriter drains API_METRICS into the api schema; jobs (started
// with the control plane) rolls the minutes up to hours and days.
func (s *Stack) StartMetricsWriter(t testing.TB) {
	t.Helper()
	ops := freePort(t)
	env := map[string]string{
		"NATS_URL":                            "nats://nats:4222",
		"NATS_REPLICAS":                       "1",
		"METRICSWRITER_NODE_ID":               "core-1",
		"METRICSWRITER_OPS_ADDR":              "0.0.0.0:9090",
		"METRICSWRITER_BATCH":                 "256",
		"METRICSWRITER_FIRST_POINT_THRESHOLD": "120s",
		"METRICSWRITER_ROUTE_CAP":             "300",
		"METRICSWRITER_SERVICE_CAP":           "50",
		"TZ":                                  "UTC",
	}
	for k, v := range s.DBEnv {
		env[k] = v
	}
	s.mounted(t, "sa-ce-metricswriter-"+s.ID, bins.metricswriter, []string{"-p", "127.0.0.1:" + ops + ":9090"}, env)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)
}

// StartTenantBackend runs the stock collector the example apps export their
// first copy to, as a tenant's own backend (deploy/compose/examples).
func (s *Stack) StartTenantBackend(t testing.TB) {
	t.Helper()
	cfg := filepath.Join(repoRoot(), "deploy", "compose", "examples", "tenant-backend.yaml")
	health := freePort(t)
	s.run(t, "sa-ce-tenant-backend-"+s.ID, []string{
		"--network-alias", "tenant-backend", "-p", "127.0.0.1:" + health + ":13133",
		// nothing reads what the tenant backend writes here, and files of
		// its uid 10001 in a host directory could not be removed afterwards
		"-v", cfg + ":/etc/otelcol/tenant-backend.yaml:ro", "--tmpfs", "/out:mode=1777",
	}, tenantBackendImage, "--config=/etc/otelcol/tenant-backend.yaml")
	waitHTTP(t, "http://127.0.0.1:"+health+"/", time.Minute)
}

// appRequest asks the control api for one example app process.
type appRequest struct {
	Service string `json:"service"`
	Token   string `json:"token"`
	// "metrics,traces" or "traces", the example's EXPORT_SIGNALS
	Signals string `json:"signals"`
}

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// startApp runs the go example app with a tenant's token: its first
// exporter goes to the tenant backend, the second to otlp.<base domain>
// through the edge caddy, the collector, nats and the metricswriter. it
// returns the loopback address the spec sends requests to.
func (s *Stack) startApp(r appRequest) (string, error) {
	if !serviceName.MatchString(r.Service) || !strings.HasPrefix(r.Token, "sa_otlp_") ||
		(r.Signals != "metrics,traces" && r.Signals != "traces") {
		return "", fmt.Errorf("bad app request %+v", r)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	name := "sa-ce-app-" + r.Service + "-" + s.ID
	args := append([]string{"run", "-d", "--name", name, "--network", s.Net, "-p", "127.0.0.1:" + port + ":8080",
		"-v", s.CADir + ":/edge-ca:ro"}, envOpts(map[string]string{
		"TENANT_OTLP_ENDPOINT":   "http://tenant-backend:4318",
		"PLATFORM_OTLP_ENDPOINT": "https://otlp." + baseDomain,
		"PLATFORM_OTLP_TOKEN":    r.Token,
		"EXPORT_SIGNALS":         r.Signals,
		"METRICS_TEMPORALITY":    "cumulative",
		"METRICS_HISTOGRAM":      "explicit",
		// short, so the spec does not wait long for the first export
		"METRICS_EXPORT_INTERVAL_MS": "3000",
		"OTEL_SERVICE_NAME":          r.Service,
		"SSL_CERT_FILE":              "/edge-ca/root.crt",
		"PORT":                       "8080",
		"TZ":                         "UTC",
	})...)
	args = append(args, exampleImage)
	if _, err := docker(args...); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.names = append(s.names, name)
	s.mu.Unlock()
	base := "http://127.0.0.1:" + port
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return base, nil
			}
			// a non-204 answer has no error of its own; name the status so
			// the timeout below still says what the app answered
			err = fmt.Errorf("healthz answered %d", resp.StatusCode)
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			return "", fmt.Errorf("app %s not healthy: %w\n%s", name, err, logs)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

var siteID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// verifySite records a successful file verification of a site, as the
// control plane's domain check would. test fixture only: the real check
// fetches https://<domain>/.well-known/veristar.txt through
// internal/netguard, which refuses the private and loopback addresses every
// host of this offline stack resolves to, and no dns server here can hold
// the txt record. the global views only read the result.
func (s *Stack) verifySite(id string) error {
	if !siteID.MatchString(id) {
		return fmt.Errorf("bad site id %q", id)
	}
	out, err := docker("exec", s.Postgres, "psql", "-q", "-h", "127.0.0.1", "-U", "postgres", "-d", "platform", "-tAc",
		"update cp.sites set verify_method = 'file', verified_at = now(), last_verify_at = now(), "+
			"last_verify_error = null where id = '"+id+"' and status <> 'removed' returning id")
	if err != nil {
		return err
	}
	if out != id {
		return fmt.Errorf("site %s not updated: %q", id, out)
	}
	return nil
}

// restartJobs restarts the jobs daemon. a daemon runs every periodic task
// once as soon as it leads, so this is how a spec gets the hourly
// aggregate-web task (and the 5 minute api rollup) to read new data now
// instead of within the hour; `jobs run` refuses while a daemon leads.
func (s *Stack) restartJobs() error {
	if _, err := docker("restart", "-t", "20", s.Jobs); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := http.Get("http://127.0.0.1:" + s.JobsOps + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("healthz answered %d", resp.StatusCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("jobs not healthy after restart: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// AppsControl serves POST /apps on loopback for the specs: the token comes
// from the console, so the app can only start once a spec has created it.
// POST /sites/verify and POST /jobs/restart are the two fixtures above;
// /alertmanager/* serves the alerts spec (alertmanager_test.go).
func (s *Stack) AppsControl(t testing.TB) string {
	t.Helper()
	mux := http.NewServeMux()
	s.alertmanagerRoutes(mux)
	mux.HandleFunc("POST /sites/verify", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SiteID string `json:"site_id"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.verifySite(body.SiteID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /jobs/restart", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.restartJobs(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /apps", func(w http.ResponseWriter, req *http.Request) {
		var r appRequest
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		base, err := s.startApp(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": base})
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + l.Addr().String()
}

// StartApp runs deploy/caddy/app.Caddyfile with the console's static build
// and the control plane behind /api.
func (s *Stack) StartApp(t testing.TB) {
	t.Helper()
	port := freePort(t)
	s.run(t, "sa-ce-app-"+s.ID, append([]string{
		"-p", "127.0.0.1:" + port + ":443",
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "app.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-v", filepath.Join(repoRoot(), "apps", "console", "dist") + ":/srv/console:ro",
	}, envOpts(appCaddyEnv())...), umamictr.CaddyImage)
	s.Routes["app."+baseDomain+":443"] = "127.0.0.1:" + port
	waitTLS(t, "127.0.0.1:"+port, "app."+baseDomain, "/")
}

// waitTLS waits until a caddy answers 200 on path for host over tls.
func waitTLS(t testing.TB, addr, host, path string) {
	t.Helper()
	c := tlsClient(addr, host)
	deadline := time.Now().Add(time.Minute)
	last := ""
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("https://%s%s via %s not ready: %s", host, path, addr, last)
}

func outDir() string {
	dir := filepath.Join(repoRoot(), ".scratch", "console-e2e")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// countrySubnets picks /24 subnets inside networks the gateway image's db-ip
// file assigns to each country (the same method as tests/webpath), and that
// the image's network data classifies as access networks, so the browsers
// look like residential visitors and the integrity grade of their traffic
// does not depend on which network the draw lands in.
func countrySubnets(t testing.TB, cc string) []netip.Prefix {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dbip.mmdb")
	id := mustDocker(t, "create", gatewayImage)
	defer func() { _, _ = docker("rm", id) }()
	mustDocker(t, "cp", id+":/geo/dbip-country-lite.mmdb", path)
	for _, f := range []string{gateway.NetworkASNFile, gateway.NetworkClassesFile, gateway.NetworkTorFile} {
		mustDocker(t, "cp", id+":/geo/"+f, filepath.Join(dir, f))
	}
	nw, err := gateway.OpenNetwork(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer nw.Close()
	r, err := maxminddb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	type rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	var cands []netip.Prefix
	for res := range r.Networks() {
		var v rec
		if err := res.Decode(&v); err != nil {
			t.Fatal(err)
		}
		p := res.Prefix()
		if !p.Addr().Is4() || p.Bits() > 24 || p.Bits() < 12 || v.Country.ISOCode != cc || p.Addr().IsPrivate() {
			continue
		}
		cands = append(cands, p)
	}
	if len(cands) == 0 {
		t.Fatalf("db-ip file has no /24 or wider ipv4 network for %s", cc)
	}
	var out []netip.Prefix
	for range 200 {
		if len(out) == 5 {
			break
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(cands))))
		p := cands[n.Int64()]
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(1)<<(24-p.Bits())))
		a := p.Addr().As4()
		x := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 + uint32(k.Int64())<<8
		client := netip.AddrFrom4([4]byte{byte(x >> 24), byte(x >> 16), byte(x >> 8), 10})
		var v rec
		if err := r.Lookup(client).Decode(&v); err != nil || v.Country.ISOCode != cc {
			continue
		}
		ip, err := gateway.ParseClientIP(client.String())
		if err != nil {
			t.Fatal(err)
		}
		if class, _, err := nw.Classify(ip); err != nil || class != integrity.NetAccess {
			continue
		}
		out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(x >> 24), byte(x >> 16), byte(x >> 8), 0}), 24))
	}
	if len(out) == 0 {
		t.Fatalf("no usable /24 for %s", cc)
	}
	return out
}

func TestConsoleFlows(t *testing.T) {
	s := newStack(t)
	s.StartPostgres(t)
	s.StartNATS(t)
	s.StartOAuthFixture(t)
	s.StartAlertmanager(t)
	s.StartControlPlane(t)
	s.StartReplayer(t)
	s.StartCollector(t)
	s.StartMetricsWriter(t)
	s.StartTenantBackend(t)
	s.StartEdge(t)
	s.StartApp(t)
	appsCtl := s.AppsControl(t)

	routes, err := json.Marshal(s.Routes)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--filter", "@veristar/e2e-console", "exec", "playwright", "test"}
	if extra := os.Getenv("CONSOLE_E2E_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	realFailures := filepath.Join(outDir(), "real-failures.jsonl")
	if err := os.Remove(realFailures); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// the virtual passkeys of an earlier run belong to that run's database;
	// kept, they would be offered to this run's control plane, which never
	// registered them
	stale, err := filepath.Glob(filepath.Join(outDir(), "passkey-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("pnpm", args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(),
		"CE2E_BASE_DOMAIN="+baseDomain,
		"CE2E_OAUTH_URL=http://"+oauthHost+":"+oauthPort,
		"CE2E_OAUTH_CTL="+s.OAuthCtl,
		"CE2E_PROXY_PORT="+freePort(t),
		"CE2E_SITE_CTL_PORT="+freePort(t),
		"CE2E_ROUTES="+string(routes),
		"CE2E_OUT="+outDir(),
		"CE2E_APPS_CTL="+appsCtl,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runErr := cmd.Run()
	// a retried test that passed hides nothing only if every failed attempt
	// coincided with a network change (specs/test.ts)
	if b, err := os.ReadFile(realFailures); err == nil && len(b) > 0 {
		t.Errorf("failed attempts not caused by a network change:\n%s", b)
	}
	if runErr != nil {
		t.Fatalf("playwright: %v", runErr)
	}
}

// tlsClient talks to a caddy container's published port with the given sni,
// without following redirects; caddy's local ca signs the certificate.
func tlsClient(addr, host string) *http.Client {
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true}, // caddy's internal ca signs it
		},
	}
}

// appCaddyEnv is the environment deploy/caddy/app.Caddyfile reads.
func appCaddyEnv() map[string]string {
	return map[string]string{
		"BASE_DOMAIN":           baseDomain,
		"APP_TLS":               "internal",
		"CONTROLPLANE_UPSTREAM": "controlplane:8081",
		"CONSOLE_ROOT":          "/srv/console",
	}
}
