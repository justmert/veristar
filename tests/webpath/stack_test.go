//go:build webpath

// Package webpath holds the end-to-end suite of the web path: real
// browsers (tests/e2e in its real-gateway mode) on the example sites, the
// edge caddy, the gateway image, nats, the replayer image, umami and its
// postgresql, all in containers this suite owns, so services can be stopped
// and killed without touching the shared dev stack. results are read from
// umami's api as the admin service account. run it with `make webpath`.
package webpath

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/gatewayimage"
)

const (
	gatewayImage  = "veristar/gateway:webpath"
	replayerImage = "veristar/replayer:webpath"
	// pinned by digest, the same digests the gateway tests use
	caddyImage  = "caddy:2.11.9@sha256:4851e54a46b206517c574821bb5cc67f170d2156041e9fbc4c9193b45e84a75b"
	staticImage = "gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931"

	gatewayHost = "in.sa-test.example"
	edgeDomain  = "sa-test.example"
)

// starting values from docs/reference/gateway-limits.md, as in tests/gateway
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

func TestMain(m *testing.M) {
	month := os.Getenv("DBIP_MONTH")
	if month == "" {
		fmt.Fprintln(os.Stderr, "webpath: DBIP_MONTH (YYYY-MM) is required; make webpath sets it")
		os.Exit(2)
	}
	builds := [][]string{
		gatewayimage.BuildArgs(month, gatewayImage),
		{"build", "-q", "-f", "deploy/images/go-service.Dockerfile", "--build-arg", "CMD=replayer", "-t", replayerImage, "."},
	}
	for _, b := range builds {
		cmd := exec.Command("docker", b...)
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "webpath: docker %s: %v\n%s", strings.Join(b, " "), err, out)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// docker runs the docker cli and returns trimmed stdout.
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

func containerLogs(t testing.TB, name string) string {
	t.Helper()
	out, err := exec.Command("docker", "logs", name).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s: %v", name, err)
	}
	return string(out)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func secret() string { return randHex(24) }

// freePort returns a loopback port nothing listens on right now. fixed host
// ports keep umami's and the replayers' addresses stable across docker stop
// and start, which a random published port would not.
func freePort(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// Stack is every container of one run: a private network for nats, gateway,
// caddy, replayers, umami and postgresql, plus two networks on db-ip country
// ranges that carry the browsers' traffic into caddy.
type Stack struct {
	t      testing.TB
	ID     string
	Net    string
	Subnet netip.Prefix

	NATS string
	NC   *nats.Conn
	JS   jetstream.JetStream

	Postgres string
	Umami    string
	UmamiURL *url.URL
	Admin    *umami.Admin
	Client   *umami.Client
	AdminPW  string
	TeamID   string

	Gateway   string
	GatewayOp string
	Caddy     string

	// Countries maps a route name (DE, JP) to its network.
	Countries map[string]*CountryNet
	Fwd       string
	FwdAddr   string // loopback host:port the browsers' tls goes to
	FwdCtl    string // http base of the route switch

	Replayers map[string]*Replayer

	names []string
	nets  []string
}

// CountryNet is a docker network on a /24 db-ip assigns to one country.
type CountryNet struct {
	Code   string
	Name   string
	Subnet netip.Prefix
	// Client is the forwarder's address on this network: the address caddy
	// sees as the browser's.
	Client netip.Addr
	Caddy  netip.Addr
}

// Replayer is one replayer container.
type Replayer struct {
	Name, Node string
	OpsURL     string
	opsPort    string
}

func newStack(t testing.TB) *Stack {
	t.Helper()
	s := &Stack{t: t, ID: randHex(4), Countries: map[string]*CountryNet{}, Replayers: map[string]*Replayer{}}
	t.Cleanup(s.cleanup)
	s.Net = "sa-wp-" + s.ID
	var err error
	for range 20 {
		n, _ := rand.Int(rand.Reader, big.NewInt(250))
		s.Subnet = netip.MustParsePrefix(fmt.Sprintf("10.214.%d.0/24", n.Int64()+1))
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

func (s *Stack) cleanup() {
	if os.Getenv("WEBPATH_KEEP") != "" {
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

func envOpts(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, "-e", k+"="+v)
	}
	return out
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

var (
	natsImageLine     = regexp.MustCompile(`(?m)^\s*image:\s*(nats:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
	umamiImageLine    = regexp.MustCompile(`(?m)^\s*image:\s*(ghcr\.io/umami-software/umami:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
	postgresImageLine = regexp.MustCompile(`(?m)^\s*image:\s*(postgres:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
)

// devImage reads a digest-pinned image from deploy/compose/dev.yml, so the
// suite runs exactly the builds the dev stack runs.
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

// StartNATS runs nats with jetstream and applies the topology.
func (s *Stack) StartNATS(t testing.TB) {
	t.Helper()
	s.NATS = "sa-wp-nats-" + s.ID
	port, mon := freePort(t), freePort(t)
	s.run(t, s.NATS, []string{"--network-alias", "nats", "-p", "127.0.0.1:" + port + ":4222", "-p", "127.0.0.1:" + mon + ":8222"},
		devImage(t, natsImageLine), "--name", "wp", "--jetstream", "--store_dir", "/data", "--http_port", "8222")
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

// StartUmami runs postgresql with the dev init script and umami with the
// environment of deploy/compose/dev.yml, then rotates
// the seeded admin password.
func (s *Stack) StartUmami(t testing.TB) {
	t.Helper()
	dbPW := secret()
	s.Postgres = "sa-wp-postgres-" + s.ID
	s.run(t, s.Postgres, append([]string{
		"--network-alias", "postgres",
		"-v", filepath.Join(repoRoot(), "deploy", "compose", "postgres-init.sh") + ":/docker-entrypoint-initdb.d/10-init.sh:ro",
	}, envOpts(map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": secret(), "UMAMI_DB_PASSWORD": dbPW, "TZ": "UTC",
	})...), devImage(t, postgresImageLine))
	s.waitPostgres(t, 2*time.Minute)

	s.Umami = "sa-wp-umami-" + s.ID
	port := freePort(t)
	// REDIS_URL, CLICKHOUSE_URL, KAFKA_URL, CLOUD_MODE, MCP_ENABLED,
	// DISABLE_BOT_CHECK and SKIP_LOCATION_HEADERS stay unset on purpose
	s.run(t, s.Umami, append([]string{"--network-alias", "umami", "-p", "127.0.0.1:" + port + ":3000"},
		envOpts(map[string]string{
			"DATABASE_URL":       "postgresql://umami_app:" + dbPW + "@postgres:5432/umami",
			"APP_SECRET":         secret(),
			"CLIENT_IP_HEADER":   "x-edge-client-ip",
			"ENABLE_CLIENT_SALT": "1",
			"SALT_ROTATION":      "daily",
			"PRIVATE_MODE":       "1",
			"DISABLE_TELEMETRY":  "1",
			"DISABLE_UPDATES":    "1",
			"DISABLE_LOGIN":      "1",
			"TZ":                 "UTC",
		})...), devImage(t, umamiImageLine))
	s.UmamiURL = &url.URL{Scheme: "http", Host: "127.0.0.1:" + port}
	waitHTTP(t, s.UmamiURL.String()+"/api/heartbeat", 5*time.Minute)
	var err error
	if s.Client, err = umami.New(s.UmamiURL, nil); err != nil {
		t.Fatal(err)
	}
	s.AdminPW = secret()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := umami.Bootstrap(ctx, s.Client, s.AdminPW); err != nil {
		t.Fatal(err)
	}
	if s.Admin, err = umami.NewAdmin(s.Client, umami.SeededUsername, s.AdminPW); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Client.Login(ctx, umami.SeededUsername, s.AdminPW)
	if err != nil {
		t.Fatal(err)
	}
	team, _, err := s.Admin.CreateTeam(ctx, "webpath-"+s.ID, sess.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.TeamID = team.ID
}

// waitPostgres waits until the server outside the entrypoint's init phase
// accepts tcp connections and both databases exist.
func (s *Stack) waitPostgres(t testing.TB, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, err := docker("exec", s.Postgres, "psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-tAc",
			"select count(*) from pg_database where datname in ('umami','platform')")
		if err == nil && out == "2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("postgres not ready: %v %q", err, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// AddWebsite creates an umami website with a caller-chosen id in the team.
func (s *Stack) AddWebsite(t testing.TB, id, name, domain string) {
	t.Helper()
	if _, err := s.Admin.CreateWebsite(context.Background(), umami.NewWebsite{ID: id, Name: name, Domain: domain, TeamID: s.TeamID}); err != nil {
		t.Fatal(err)
	}
}

// PutSite registers a site in registry_sites.
func (s *Stack) PutSite(t testing.TB, site registry.Site) {
	t.Helper()
	p, err := registry.NewPublisher(context.Background(), s.JS)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.PutSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
}

// StartEdge runs the gateway and the edge caddy (deploy/caddy/edge.Caddyfile)
// on the private network.
func (s *Stack) StartEdge(t testing.TB) {
	t.Helper()
	s.Gateway = "sa-wp-gateway-" + s.ID
	ops := freePort(t)
	s.run(t, s.Gateway, append([]string{"--network-alias", "edge-1", "-p", "127.0.0.1:" + ops + ":9090"}, envOpts(gatewayEnv)...), gatewayImage)
	s.GatewayOp = "http://127.0.0.1:" + ops
	waitHTTP(t, s.GatewayOp+"/readyz", time.Minute)

	s.Caddy = "sa-wp-caddy-" + s.ID
	s.run(t, s.Caddy, []string{
		"--network-alias", gatewayHost,
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + edgeDomain, "-e", "GATEWAY_UPSTREAM=edge-1:8080", "-e", "EDGE_TLS=internal",
		// the otlp site of edge.Caddyfile needs its upstreams set or
		// caddy refuses the config. the web path has no collector, so they
		// name a host that does not exist; nothing here calls otlp.<domain>.
		"-e", "COLLECTOR_GRPC_UPSTREAM=no-collector:4317", "-e", "COLLECTOR_HTTP_UPSTREAM=no-collector:4318",
		"-e", "COLLECTOR_HEALTH_UPSTREAM=no-collector:13133",
	}, caddyImage)
}

// AddCountry creates a network on the first of subnets docker accepts (one
// another network holds is refused) and attaches caddy at .3 of it.
func (s *Stack) AddCountry(t testing.TB, code string, subnets []netip.Prefix) *CountryNet {
	t.Helper()
	name := "sa-wp-" + strings.ToLower(code) + "-" + s.ID
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
	base := subnet.Addr().As4()
	at := func(last byte) netip.Addr { return netip.AddrFrom4([4]byte{base[0], base[1], base[2], last}) }
	c := &CountryNet{Code: code, Name: name, Subnet: subnet, Client: at(10), Caddy: at(3)}
	mustDocker(t, "network", "connect", "--ip", c.Caddy.String(), name, s.Caddy)
	s.Countries[code] = c
	return c
}

// StartForwarder runs tests/webpath/fwd attached to every country network,
// with initial as the first route, and waits until a tls request through it
// reaches caddy.
func (s *Stack) StartForwarder(t testing.TB, bin, initial string) {
	t.Helper()
	s.Fwd = "sa-wp-fwd-" + s.ID
	first := s.Countries[initial]
	args := []string{"-listen", ":8443", "-control", ":8080", "-initial", initial}
	for code, c := range s.Countries {
		args = append(args, "-route", code+"="+c.Caddy.String()+":443")
	}
	tlsPort, ctlPort := freePort(t), freePort(t)
	all := append([]string{"run", "-d", "--name", s.Fwd, "--network", first.Name, "--ip", first.Client.String(),
		"-p", "127.0.0.1:" + tlsPort + ":8443", "-p", "127.0.0.1:" + ctlPort + ":8080",
		"-v", bin + ":/fwd:ro", "--entrypoint", "/fwd", staticImage}, args...)
	mustDocker(t, all...)
	s.names = append(s.names, s.Fwd)
	for code, c := range s.Countries {
		if code != initial {
			mustDocker(t, "network", "connect", "--ip", c.Client.String(), c.Name, s.Fwd)
		}
	}
	s.FwdAddr = "127.0.0.1:" + tlsPort
	s.FwdCtl = "http://127.0.0.1:" + ctlPort
	waitHTTP(t, s.FwdCtl+"/", 30*time.Second)
}

// Route switches the forwarder, which closes its open connections.
func (s *Stack) Route(t testing.TB, code string) {
	t.Helper()
	resp, err := http.Post(s.FwdCtl+"/route?to="+code, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("route %s: %s", code, resp.Status)
	}
	// the fixture's pooled connections see the close before the next request
	time.Sleep(time.Second)
}

// AddReplayer starts a replayer container; it waits until /healthz answers.
func (s *Stack) AddReplayer(t testing.TB, node string) *Replayer {
	t.Helper()
	r := &Replayer{Name: "sa-wp-replayer-" + node + "-" + s.ID, Node: node, opsPort: freePort(t)}
	s.run(t, r.Name, append([]string{"-p", "127.0.0.1:" + r.opsPort + ":9090"}, envOpts(map[string]string{
		"NATS_URL":               "nats://nats:4222",
		"NATS_REPLICAS":          "1",
		"REPLAYER_NODE_ID":       node,
		"REPLAYER_OPS_ADDR":      "0.0.0.0:9090",
		"UMAMI_URL":              "http://umami:3000",
		"REPLAYER_WORKERS":       "16",
		"REPLAYER_MAX_IN_FLIGHT": "512",
		"TZ":                     "UTC",
	})...), replayerImage)
	r.OpsURL = "http://127.0.0.1:" + r.opsPort
	waitHTTP(t, r.OpsURL+"/healthz", time.Minute)
	s.Replayers[node] = r
	return r
}

// stop and start keep the container, its address and its published ports.
func (s *Stack) stop(t testing.TB, name string) {
	t.Helper()
	mustDocker(t, "stop", "-t", "10", name)
}

func (s *Stack) start(t testing.TB, name string) {
	t.Helper()
	mustDocker(t, "start", name)
}

// psql runs a query in the umami database as the superuser over the
// container's local socket.
func (s *Stack) psql(t testing.TB, query string) string {
	t.Helper()
	return mustDocker(t, "exec", s.Postgres, "psql", "-U", "postgres", "-d", "umami", "-tAc", query)
}

// buildTool compiles one of the test tools under tests/webpath for linux.
func buildTool(t testing.TB, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-trimpath", "-o", bin, "./"+pkg)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, out)
	}
	// the distroless image runs as nonroot; the mounted file must be readable
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
