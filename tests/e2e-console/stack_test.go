//go:build consolee2e

// Package consolee2e runs the this stage browser flows (the design
// verification) against real services in containers this test owns:
// postgresql with the platform migrations, nats with the topology, umami
// (bootstrapped), the control plane built with the oauthfixture tag and
// pointed at the TEST-ONLY github oauth fixture (tests/fixtures/githuboauth),
// the replayer, the gateway image behind the edge caddy, the web caddy in
// front of umami and the console's static build behind the app caddy. the
// browsers reach the edge through a forwarder on a db-ip german range, so the
// country panel has a real value. the playwright half lives in this
// directory (specs/). run it with `make console-e2e`.
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
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

const (
	baseDomain   = "sa-test.example"
	gatewayImage = "stellar-analytics/gateway:console-e2e"
	// caddy with the replace-response plugin (deploy/images/caddy.Dockerfile),
	// which the web caddy needs for the db-ip attribution
	caddyPluginImage = "stellar-analytics/caddy:2.11.9"
	staticImage      = "gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
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
	controlplane, jobs, replayer, platformctl, fwd, oauth string
	dir                                                   string
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
		{"build", "-q", "-f", "deploy/images/gateway.Dockerfile", "--build-arg", "DBIP_MONTH=" + month, "-t", gatewayImage, "."},
		{"build", "-q", "-f", "deploy/images/caddy.Dockerfile", "-t", caddyPluginImage, "deploy/images"},
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
	// the console's static build, served by the app caddy
	cmd = exec.Command("pnpm", "--filter", "@stellar-analytics/console", "build")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("console build: %w\n%s", err, out)
	}
	return -1, nil
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
	// Routes maps host:port as the browser asks for it to the loopback
	// address of the published container port
	Routes map[string]string

	names []string
	nets  []string
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
		for _, n := range s.names {
			out, _ := exec.Command("docker", "logs", n).CombinedOutput()
			_ = os.WriteFile(filepath.Join(dir, n+".log"), out, 0o644)
		}
	}
	if os.Getenv("CONSOLE_E2E_KEEP") != "" {
		return
	}
	for i := len(s.names) - 1; i >= 0; i-- {
		_, _ = docker("rm", "-f", "-v", s.names[i])
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
	s.names = append(s.names, name)
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
	// environment of the design, as deploy/compose/dev.yml
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
		"TZ":                      "UTC",
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
	for k, v := range map[string]string{
		"CONTROLPLANE_ADDR":     "0.0.0.0:8081",
		"CONTROLPLANE_OPS_ADDR": "0.0.0.0:9090",
		"CONSOLE_ORIGIN":        "https://app." + baseDomain,
		"INGEST_ORIGIN":         "https://in." + baseDomain,
		"WEB_ORIGIN":            "https://web." + baseDomain,
		"GITHUB_CLIENT_ID":      oauthClientID,
		"GITHUB_CLIENT_SECRET":  s.OAuthSecret,
		"GITHUB_REDIRECT_URL":   "https://app." + baseDomain + "/api/auth/github/callback",
		"OAUTH_STATE_KEY":       key32(),
		"OAUTH_FIXTURE_URL":     "http://" + oauthHost + ":" + oauthPort,
		// an id no test user takes; this stage flows run as tenants
		"OPERATOR_GITHUB_IDS": "1",
	} {
		env[k] = v
	}
	releases := filepath.Join(repoRoot(), "packages", "tracker", "releases") + ":/releases:ro"
	ops := freePort(t)
	s.mounted(t, "sa-ce-controlplane-"+s.ID, bins.controlplane,
		[]string{"--network-alias", "controlplane", "-p", "127.0.0.1:" + ops + ":9090", "-v", releases}, env)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)

	jobsOps := freePort(t)
	jenv := s.common(userKey)
	jenv["JOBS_OPS_ADDR"] = "0.0.0.0:9090"
	s.mounted(t, "sa-ce-jobs-"+s.ID, bins.jobs,
		[]string{"-p", "127.0.0.1:" + jobsOps + ":9090", "-v", releases}, jenv, "daemon")
	waitHTTP(t, "http://127.0.0.1:"+jobsOps+"/healthz", time.Minute)
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
		"--network-alias", "in." + baseDomain,
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "GATEWAY_UPSTREAM=edge-1:8080", "-e", "EDGE_TLS=internal",
	}, umamictr.CaddyImage)

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

// StartWeb runs deploy/caddy/web.Caddyfile in front of umami, on the caddy
// build with the attribution plugin.
func (s *Stack) StartWeb(t testing.TB) {
	t.Helper()
	port := freePort(t)
	s.run(t, "sa-ce-web-"+s.ID, []string{
		"-p", "127.0.0.1:" + port + ":443",
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "web.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "UMAMI_UPSTREAM=" + umamictr.UpstreamAddr, "-e", "WEB_TLS=internal",
	}, caddyPluginImage)
	s.Routes["web."+baseDomain+":443"] = "127.0.0.1:" + port
	waitTLS(t, "127.0.0.1:"+port, "web."+baseDomain, "/api/heartbeat")
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
	dir := filepath.Join(repoRoot(), "resources", "scratch", "console-e2e")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// countrySubnets picks /24 subnets inside networks the gateway image's db-ip
// file assigns to each country (the same method as tests/webpath).
func countrySubnets(t testing.TB, cc string) []netip.Prefix {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbip.mmdb")
	id := mustDocker(t, "create", gatewayImage)
	defer func() { _, _ = docker("rm", id) }()
	mustDocker(t, "cp", id+":/geo/dbip-country-lite.mmdb", path)
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
	for range 5 {
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
	s.StartControlPlane(t)
	s.StartReplayer(t)
	s.StartEdge(t)
	s.StartWeb(t)
	s.StartApp(t)

	routes, err := json.Marshal(s.Routes)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--filter", "@stellar-analytics/e2e-console", "exec", "playwright", "test"}
	if extra := os.Getenv("CONSOLE_E2E_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	realFailures := filepath.Join(outDir(), "real-failures.jsonl")
	if err := os.Remove(realFailures); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
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
