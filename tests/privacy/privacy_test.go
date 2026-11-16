//go:build privacy

// Package privacy is the system-wide privacy audit. it runs
// the web and api pipelines in containers it owns (postgresql with both
// databases, nats with file storage, umami, the gateway image behind the edge
// caddy, the replayer, the collector image and the metricswriter), sends real
// traffic from containers with marker addresses on their own docker
// networks, so the markers reach caddy as real tcp peers, and then scans
// both databases, every jetstream stream and kv bucket, every service log
// (caddy's included) and every file a container wrote, its volumes included,
// for the markers. any hit fails the test. run it with `make privacy`.
package privacy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/gatewayimage"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

const (
	baseDomain     = "sa-privacy.example"
	gatewayImage   = "veristar/gateway:privacy"
	collectorImage = "veristar/collector:privacy"
	exampleImage   = "veristar/otel-go-nethttp:privacy"
	staticImage    = "gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
	// the stock collector standing in for the tenant's own backend; it is
	// the tenant's system, not this service's, so it is not scanned
	tenantBackendImage = "otel/opentelemetry-collector-contrib:0.165.0@sha256:e40d61f10c9a617cca1ffd1be4d7e2b3a4b0f0001798ff07e58cabe82e618cea"
)

var gatewayEnv = map[string]string{
	"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "GATEWAY_NODE_ID": "edge-1",
	"GATEWAY_ADDR": "0.0.0.0:8080", "GATEWAY_OPS_ADDR": "0.0.0.0:9090", "PROOF_BITS": "12",
	"PROOF_REPLAY_LIMIT": "50", "IPCLUSTER_THRESHOLD": "20", "VISITOR_EVENTS_PER_HOUR": "600", "RATE_SITE_PER_SECOND": "200",
	"RATE_SITE_BURST": "400", "RATE_PREFIX_PER_SECOND": "20", "RATE_PREFIX_BURST": "60",
	"GATEWAY_MAX_BODY": "8192", "CLIENT_IP_HEADER": "X-Edge-Client-Ip", "GATEWAY_SECURE_TRANSPORT": "true",
	"TZ": "UTC",
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

var bins struct{ replayer, metricswriter, platformctl, probe, dir string }

func TestMain(m *testing.M) {
	month := os.Getenv("DBIP_MONTH")
	if month == "" {
		fmt.Fprintln(os.Stderr, "privacy: DBIP_MONTH (YYYY-MM) is required; make privacy sets it")
		os.Exit(2)
	}
	for _, b := range [][]string{
		gatewayimage.BuildArgs(month, gatewayImage),
		{"build", "-q", "-f", "deploy/images/collector.Dockerfile", "-t", collectorImage, "."},
		{"build", "-q", "-t", exampleImage, "examples/otel-go-nethttp"},
	} {
		cmd := exec.Command("docker", b...)
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "privacy: docker %s: %v\n%s", strings.Join(b, " "), err, out)
			os.Exit(1)
		}
	}
	dir, err := os.MkdirTemp("", "sa-privacy-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bins.dir = dir
	for _, b := range []struct {
		out   *string
		pkg   string
		linux bool
	}{
		{&bins.replayer, "./cmd/replayer", true},
		{&bins.metricswriter, "./cmd/metricswriter", true},
		{&bins.probe, "./tests/privacy/probe", true},
		{&bins.platformctl, "./cmd/platformctl", false},
	} {
		*b.out = filepath.Join(dir, filepath.Base(b.pkg))
		cmd := exec.Command("go", "build", "-trimpath", "-o", *b.out, b.pkg)
		cmd.Dir = repoRoot()
		if b.linux {
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "privacy: go build %s: %v\n%s", b.pkg, err, out)
			os.Exit(1)
		}
		// the distroless image runs as nonroot; the mounted file must be readable
		_ = os.Chmod(*b.out, 0o755)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
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

var natsImageLine = regexp.MustCompile(`(?m)^\s*image:\s*(nats:[^\s]+@sha256:[0-9a-f]{64})\s*$`)

func natsImage(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), "deploy", "compose", "dev.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := natsImageLine.FindSubmatch(b)
	if m == nil {
		t.Fatal("dev.yml has no digest-pinned nats image")
	}
	return string(m[1])
}

// stack is every container of the run. scanned lists the containers whose
// logs and files the audit reads: every service of the platform.
type stack struct {
	id      string
	net     string
	names   []string
	scanned []string
	nets    []string

	pgPort, pgPW string
	dbEnv        map[string]string
	nc           *nats.Conn
	js           jetstream.JetStream
	umamiURL     *url.URL
	admin        *umami.Admin
	edge         string
	caDir        string
}

func newNet(t testing.TB, s *stack, prefix string) (string, netip.Prefix) {
	t.Helper()
	name := prefix + "-" + s.id
	var err error
	for range 20 {
		n, _ := rand.Int(rand.Reader, big.NewInt(250))
		p := netip.MustParsePrefix(fmt.Sprintf("10.217.%d.0/24", n.Int64()+1))
		if _, err = docker("network", "create", "--subnet", p.String(), name); err == nil {
			s.nets = append(s.nets, name)
			return name, p
		}
	}
	t.Fatal(err)
	return "", netip.Prefix{}
}

func (s *stack) run(t testing.TB, name string, scan bool, opts []string, image string, args ...string) {
	t.Helper()
	all := append([]string{"run", "-d", "--name", name, "--network", s.net}, opts...)
	all = append(all, image)
	all = append(all, args...)
	mustDocker(t, all...)
	s.names = append(s.names, name)
	if scan {
		s.scanned = append(s.scanned, name)
	}
}

func (s *stack) mounted(t testing.TB, name, bin string, opts []string, env map[string]string) {
	t.Helper()
	all := append([]string{"-v", bin + ":/bin/service:ro", "--entrypoint", "/bin/service"}, opts...)
	s.run(t, name, true, append(all, envOpts(env)...), staticImage)
}

func newStack(t *testing.T) *stack {
	t.Helper()
	s := &stack{id: randHex(4)}
	t.Cleanup(func() {
		if os.Getenv("PRIVACY_KEEP") != "" {
			return
		}
		for i := len(s.names) - 1; i >= 0; i-- {
			_, _ = docker("rm", "-f", "-v", s.names[i])
		}
		for _, n := range s.nets {
			_, _ = docker("network", "rm", n)
		}
	})
	s.net, _ = newNet(t, s, "sa-pv")
	return s
}

func (s *stack) startPostgres(t *testing.T) string {
	t.Helper()
	umamiImage, pgImage := umamictr.Images(t)
	s.pgPW = randHex(24)
	umamiPW := randHex(24)
	s.pgPort = freePort(t)
	name := "sa-pv-postgres-" + s.id
	s.run(t, name, true, append([]string{
		"--network-alias", "postgres", "-p", "127.0.0.1:" + s.pgPort + ":5432",
		"-v", filepath.Join(repoRoot(), "deploy", "compose", "postgres-init.sh") + ":/docker-entrypoint-initdb.d/10-init.sh:ro",
	}, envOpts(map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": s.pgPW, "UMAMI_DB_PASSWORD": umamiPW, "TZ": "UTC"})...), pgImage)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := docker("exec", name, "psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-tAc",
			"select count(*) from pg_database where datname in ('umami','platform')")
		if err == nil && out == "2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("postgres not ready: %v %q", err, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.dbEnv = map[string]string{"PLATFORM_DB_NAME": "platform", "PLATFORM_DB_SSLMODE": "disable"}
	for _, r := range []string{"CP_SYSTEM", "CP_TENANT", "CP_OPERATOR", "METRICS_WRITER", "JOBS"} {
		s.dbEnv["PLATFORM_DB_"+r+"_PASSWORD"] = randHex(24)
	}
	for _, sub := range [][]string{{"db", "migrate"}, {"db", "roles"}} {
		cmd := exec.Command(bins.platformctl, sub...)
		cmd.Env = os.Environ()
		for k, v := range s.dbEnv {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Env = append(cmd.Env, "PLATFORM_DB_HOST=127.0.0.1", "PLATFORM_DB_PORT="+s.pgPort,
			"PLATFORM_DB_ADMIN_USER=postgres", "PLATFORM_DB_ADMIN_PASSWORD="+s.pgPW)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("platformctl %v: %v\n%s", sub, err, out)
		}
	}
	s.dbEnv["PLATFORM_DB_HOST"] = "postgres"
	s.dbEnv["PLATFORM_DB_PORT"] = "5432"

	port := freePort(t)
	s.run(t, "sa-pv-umami-"+s.id, true, append([]string{"--network-alias", "umami", "-p", "127.0.0.1:" + port + ":3000"},
		envOpts(map[string]string{
			"DATABASE_URL": "postgresql://umami_app:" + umamiPW + "@postgres:5432/umami", "APP_SECRET": randHex(24),
			"CLIENT_IP_HEADER": "x-edge-client-ip", "ENABLE_CLIENT_SALT": "1", "SALT_ROTATION": "daily",
			"PRIVATE_MODE": "1", "DISABLE_TELEMETRY": "1", "DISABLE_UPDATES": "1", "DISABLE_LOGIN": "1", "TZ": "UTC",
		})...), umamiImage)
	s.umamiURL = &url.URL{Scheme: "http", Host: "127.0.0.1:" + port}
	waitHTTP(t, s.umamiURL.String()+"/api/heartbeat", 5*time.Minute)
	c, err := umami.New(s.umamiURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	pw := randHex(24)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := umami.Bootstrap(ctx, c, pw); err != nil {
		t.Fatal(err)
	}
	if s.admin, err = umami.NewAdmin(c, umami.SeededUsername, pw); err != nil {
		t.Fatal(err)
	}
	return name
}

func (s *stack) startNATS(t *testing.T) {
	t.Helper()
	port, mon := freePort(t), freePort(t)
	s.run(t, "sa-pv-nats-"+s.id, true, []string{"--network-alias", "nats", "-p", "127.0.0.1:" + port + ":4222", "-p", "127.0.0.1:" + mon + ":8222"},
		natsImage(t), "--name", "pv", "--jetstream", "--store_dir", "/data", "--http_port", "8222")
	waitHTTP(t, "http://127.0.0.1:"+mon+"/healthz?js-enabled-only=true", time.Minute)
	var err error
	if s.nc, err = nats.Connect("nats://127.0.0.1:"+port, nats.MaxReconnects(-1)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.nc.Close)
	if s.js, err = jetstream.New(s.nc); err != nil {
		t.Fatal(err)
	}
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(context.Background(), s.js, spec); err != nil {
		t.Fatal(err)
	}
}

func (s *stack) startServices(t *testing.T) {
	t.Helper()
	ops := freePort(t)
	s.run(t, "sa-pv-gateway-"+s.id, true, append([]string{"--network-alias", "edge-1", "-p", "127.0.0.1:" + ops + ":9090"}, envOpts(gatewayEnv)...), gatewayImage)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)

	health := freePort(t)
	s.run(t, "sa-pv-collector-"+s.id, true, append([]string{"--network-alias", "collector", "-p", "127.0.0.1:" + health + ":13133"},
		envOpts(map[string]string{
			"NATS_URL": "nats://nats:4222", "NATS_USER": "", "NATS_PASSWORD": "",
			"COLLECTOR_GRPC_ADDR": "0.0.0.0:4317", "COLLECTOR_HTTP_ADDR": "0.0.0.0:4318", "COLLECTOR_HEALTH_ADDR": "0.0.0.0:13133",
			"COLLECTOR_MEMORY_LIMIT_MIB": "512", "COLLECTOR_MEMORY_SPIKE_MIB": "128",
			"COLLECTOR_RATE_REQUESTS_PER_SECOND": "200", "COLLECTOR_RATE_REQUESTS_BURST": "400",
			"COLLECTOR_RATE_ITEMS_PER_SECOND": "100000", "COLLECTOR_RATE_ITEMS_BURST": "200000", "TZ": "UTC",
		})...), collectorImage)
	waitHTTP(t, "http://127.0.0.1:"+health+"/", 2*time.Minute)

	s.edge = "sa-pv-edge-" + s.id
	s.run(t, s.edge, true, []string{
		"--network-alias", "in." + baseDomain, "--network-alias", "otlp." + baseDomain,
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "GATEWAY_UPSTREAM=edge-1:8080", "-e", "EDGE_TLS=internal",
		"-e", "COLLECTOR_GRPC_UPSTREAM=collector:4317", "-e", "COLLECTOR_HTTP_UPSTREAM=collector:4318",
		"-e", "COLLECTOR_HEALTH_UPSTREAM=collector:13133",
	}, umamictr.CaddyImage)
	s.caDir = t.TempDir()
	_ = os.Chmod(s.caDir, 0o755)
	crt := filepath.Join(s.caDir, "root.crt")
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := docker("cp", s.edge+":/data/caddy/pki/authorities/local/root.crt", crt); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("edge ca not created")
		}
		time.Sleep(300 * time.Millisecond)
	}
	_ = os.Chmod(crt, 0o644)

	rops := freePort(t)
	s.mounted(t, "sa-pv-replayer-"+s.id, bins.replayer, []string{"-p", "127.0.0.1:" + rops + ":9090"}, map[string]string{
		"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "REPLAYER_NODE_ID": "core-1", "REPLAYER_OPS_ADDR": "0.0.0.0:9090",
		"UMAMI_URL": "http://umami:3000", "REPLAYER_WORKERS": "8", "REPLAYER_MAX_IN_FLIGHT": "128", "TZ": "UTC",
	})
	waitHTTP(t, "http://127.0.0.1:"+rops+"/healthz", time.Minute)

	mops := freePort(t)
	env := map[string]string{
		"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "METRICSWRITER_NODE_ID": "core-1",
		"METRICSWRITER_OPS_ADDR": "0.0.0.0:9090", "METRICSWRITER_BATCH": "256", "METRICSWRITER_FIRST_POINT_THRESHOLD": "120s",
		"METRICSWRITER_ROUTE_CAP": "300", "METRICSWRITER_SERVICE_CAP": "50", "TZ": "UTC",
	}
	for k, v := range s.dbEnv {
		env[k] = v
	}
	s.mounted(t, "sa-pv-metricswriter-"+s.id, bins.metricswriter, []string{"-p", "127.0.0.1:" + mops + ":9090"}, env)
	waitHTTP(t, "http://127.0.0.1:"+mops+"/readyz", time.Minute)
}

// tenant creates what enrollment and the reconciler would leave: a tenant,
// one active site with both umami websites and its registry entry, and an
// active otlp token in cp.otlp_tokens and registry_otlp.
func (s *stack) tenant(t *testing.T) (site string, token string) {
	t.Helper()
	ctx := context.Background()
	team, _, err := s.admin.CreateTeam(ctx, "privacy", "")
	if err != nil {
		t.Fatal(err)
	}
	tenant, siteID, quarantine := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	domain := "privacy.sa-tenant.example"
	for _, id := range []uuid.UUID{siteID, quarantine} {
		if _, err := s.admin.CreateWebsite(ctx, umami.NewWebsite{ID: id.String(), Name: domain, Domain: domain, TeamID: team.ID}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := pgx.Connect(ctx, fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/platform?sslmode=disable", s.pgPW, s.pgPort))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	user := uuid.Must(uuid.NewV7())
	g, err := otlptokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	tokenID := uuid.Must(uuid.NewV7())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into cp.users (id, github_id, github_login) values ($1, 4242, 'privacy-owner')`, []any{user}},
		{`insert into cp.tenants (id, slug, name, status, umami_team_id) values ($1, 'privacy', 'Privacy', 'active', $2)`, []any{tenant, team.ID}},
		{`insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
		  values ($1, $2, $3, $3, $4, $5, 'active', 'token')`, []any{siteID, tenant, domain, []string{"https://" + domain}, quarantine}},
		{`insert into cp.otlp_tokens (id, tenant_id, name, prefix, hash, created_by) values ($1, $2, 'privacy', $3, $4, $5)`,
			[]any{tokenID, tenant, g.Display, g.Hash[:], user}},
	} {
		if _, err := c.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	pub, err := registry.NewPublisher(ctx, s.js)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.PutSite(ctx, registry.Site{ID: siteID.String(), TenantID: tenant.String(), Status: registry.StatusActive,
		Origins: []string{"https://" + domain}, QuarantineWebsiteID: quarantine.String()}); err != nil {
		t.Fatal(err)
	}
	e, err := otlptokens.Entry(tenant, tokenID, g.Hash[:], "active", "active")
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.PutOTLPToken(ctx, e); err != nil {
		t.Fatal(err)
	}
	return siteID.String(), g.Plain
}

func TestNoMarkerAddressAnywhere(t *testing.T) {
	s := newStack(t)
	pg := s.startPostgres(t)
	s.startNATS(t)
	s.startServices(t)
	site, token := s.tenant(t)

	// two networks of their own, one per pipeline; caddy joins both under the
	// public host names, so a client there reaches it only as its tcp peer
	webNet, webSubnet := newNet(t, s, "sa-pv-web")
	apiNet, apiSubnet := newNet(t, s, "sa-pv-api")
	at := func(p netip.Prefix, last byte) string {
		b := p.Addr().As4()
		return netip.AddrFrom4([4]byte{b[0], b[1], b[2], last}).String()
	}
	webMarker, apiMarker := at(webSubnet, 77), at(apiSubnet, 78)
	mustDocker(t, "network", "connect", "--alias", "in."+baseDomain, webNet, s.edge)
	mustDocker(t, "network", "connect", "--alias", "otlp."+baseDomain, apiNet, s.edge)
	markers := []string{webMarker, apiMarker}

	// web: the probe solves the proof and posts pageviews from webMarker
	// a plain browser user agent: umami's bot check drops anything with a
	// tool-like token, and the audit needs the events stored
	agent := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	origin := "https://privacy.sa-tenant.example"
	out := mustDocker(t, "run", "--rm", "--network", webNet, "--ip", webMarker,
		"-v", bins.probe+":/probe:ro", "--entrypoint", "/probe", staticImage,
		"https://in."+baseDomain, site, origin, agent, "20")
	t.Logf("web probe from %s: %s", webMarker, out)

	// api: the go example app exports metrics and traces from apiMarker
	mustDocker(t, "run", "-d", "--name", "sa-pv-tenant-backend-"+s.id, "--network", apiNet, "--network-alias", "tenant-backend",
		"-v", filepath.Join(repoRoot(), "deploy", "compose", "examples", "tenant-backend.yaml")+":/etc/otelcol/tenant-backend.yaml:ro",
		"--tmpfs", "/out:mode=1777", tenantBackendImage, "--config=/etc/otelcol/tenant-backend.yaml")
	s.names = append(s.names, "sa-pv-tenant-backend-"+s.id)
	appPort := freePort(t)
	app := "sa-pv-app-" + s.id
	mustDocker(t, append(append([]string{"run", "-d", "--name", app, "--network", apiNet, "--ip", apiMarker,
		"-p", "127.0.0.1:" + appPort + ":8080", "-v", s.caDir + ":/edge-ca:ro"}, envOpts(map[string]string{
		"TENANT_OTLP_ENDPOINT": "http://tenant-backend:4318", "PLATFORM_OTLP_ENDPOINT": "https://otlp." + baseDomain,
		"PLATFORM_OTLP_TOKEN": token, "EXPORT_SIGNALS": "metrics,traces", "METRICS_TEMPORALITY": "cumulative",
		"METRICS_HISTOGRAM": "explicit", "METRICS_EXPORT_INTERVAL_MS": "2000", "OTEL_SERVICE_NAME": "privacy-api",
		"SSL_CERT_FILE": "/edge-ca/root.crt", "PORT": "8080", "TZ": "UTC",
	})...), exampleImage)...)
	s.names = append(s.names, app)
	base := "http://127.0.0.1:" + appPort
	waitHTTP(t, base+"/healthz", time.Minute)
	for i := range 30 {
		resp, err := http.Post(base+"/accounts", "application/json", strings.NewReader(`{"name":"a`+strconv.Itoa(i)+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	// both pipelines reached their stores
	ctx := context.Background()
	c, err := pgx.Connect(ctx, fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/platform?sslmode=disable", s.pgPW, s.pgPort))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	waitFor(t, 3*time.Minute, "api metric rows", func() bool {
		var n int64
		_ = c.QueryRow(ctx, `select count(*) from api.metrics_minute`).Scan(&n)
		return n > 0
	}, func() string { return "" })
	stored := ""
	waitFor(t, 2*time.Minute, "web events in umami", func() bool {
		out, err := docker("exec", pg, "psql", "-U", "postgres", "-d", "umami", "-tAc", "select count(*) from website_event")
		stored = out
		if err != nil {
			stored = err.Error()
		}
		return err == nil && out == "20"
	}, func() string {
		logs, _ := exec.Command("docker", "logs", "--tail", "40", "sa-pv-replayer-"+s.id).CombinedOutput()
		return "umami holds " + stored + " events; replayer log tail:\n" + string(logs)
	})
	// the probe's two loads of the tracker file are on EDGE_STATS as the
	// site's coverage counters once the gateway flushed (every 10 s), so the
	// jetstream scan below covers them
	waitFor(t, time.Minute, "coverage counters on EDGE_STATS", func() bool {
		d := s.natsDump(t)
		return strings.Contains(d, `"outcome":"script_requests"`) && strings.Contains(d, `"outcome":"script_gpc"`)
	}, func() string { return "" })
	// postgres writes everything to its files before the file scan
	mustDocker(t, "exec", pg, "psql", "-U", "postgres", "-tAc", "checkpoint")

	type source struct{ name, text string }
	var sources []source
	for _, db := range []string{"platform", "umami"} {
		dump := mustDocker(t, "exec", pg, "pg_dump", "-U", "postgres", "--data-only", db)
		if len(dump) < 1000 {
			t.Fatalf("dump of %s is suspiciously small", db)
		}
		sources = append(sources, source{"database " + db, dump})
	}
	sources = append(sources, source{"jetstream", s.natsDump(t)})
	for _, n := range s.scanned {
		logs, err := exec.Command("docker", "logs", n).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source{"logs of " + n, string(logs)})
	}
	files := 0
	for _, n := range s.scanned {
		for _, p := range writtenPaths(t, n) {
			txt, k := copyOut(t, n, p)
			files += k
			sources = append(sources, source{"files of " + n + ":" + p, txt})
		}
	}
	// the scan reads what the traffic left: the probe's paths in umami, the
	// otlp service name in the platform database, the registry in nats
	for i, want := range []string{"privacy-api", "/audit/0", "registry_sites"} {
		if !strings.Contains(sources[i].text, want) {
			t.Fatalf("%s does not contain %q; the scan would not see a marker either", sources[i].name, want)
		}
	}
	t.Logf("scanned %d sources, %d files", len(sources), files)
	if files == 0 {
		t.Fatal("no container file was scanned")
	}
	for _, src := range sources {
		for _, m := range markers {
			if i := strings.Index(src.text, m); i >= 0 {
				lo, hi := max(0, i-120), min(len(src.text), i+120)
				t.Errorf("marker %s found in %s: ...%q...", m, src.name, src.text[lo:hi])
			}
		}
	}
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool, detail func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s not seen after %s: %s", what, d, detail())
		}
		time.Sleep(time.Second)
	}
}

// natsDump reads every message of every stream, kv buckets included.
func (s *stack) natsDump(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	names := s.js.StreamNames(ctx)
	for name := range names.Name() {
		st, err := s.js.Stream(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := st.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
			m, err := st.GetMsg(ctx, seq)
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(m.Subject)
			b.Write(m.Data)
			hb, _ := json.Marshal(m.Header)
			b.Write(hb)
		}
	}
	if err := names.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// writtenPaths are the container's mount destinations and every file its
// writable layer added or changed.
func writtenPaths(t *testing.T, name string) []string {
	t.Helper()
	var mounts []struct {
		Destination string
	}
	if err := json.Unmarshal([]byte(mustDocker(t, "inspect", "-f", "{{json .Mounts}}", name)), &mounts); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range mounts {
		out = append(out, m.Destination)
	}
	diff := mustDocker(t, "diff", name)
	for _, line := range strings.Split(diff, "\n") {
		kind, p, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || kind == "D" {
			continue
		}
		covered := false
		for _, m := range out {
			if p == m || strings.HasPrefix(p, m+"/") {
				covered = true
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// copyOut streams a path out of a container as tar and returns the
// contents of its regular files, concatenated, and how many there were.
func copyOut(t *testing.T, name, p string) (string, int) {
	t.Helper()
	cmd := exec.Command("docker", "cp", name+":"+p, "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	n := 0
	tr := tar.NewReader(stdout)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read %s:%s: %v", name, p, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		n++
	}
	if err := cmd.Wait(); err != nil {
		// a file that vanished between diff and copy (a temporary file) is
		// not an error; anything else is
		if !strings.Contains(errb.String(), "No such container:path") && !strings.Contains(errb.String(), "Could not find the file") {
			t.Fatalf("docker cp %s:%s: %v: %s", name, p, err, errb.String())
		}
	}
	return b.String(), n
}
