//go:build handover

package handover

// the containers of one environment. adapted from tests/e2e-console's
// stack (this stage), with what a migration needs on top: a source database
// that archives wal into a pgbackrest repository, services that can be
// stopped and started again around an import, and env files in the shape
// scripts/migrate.sh and platformctl verify-migration read.

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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

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
		return "", fmt.Errorf("docker %s: %w: %s%s", strings.Join(args, " "), err, errb.String(), out.String())
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

// pgbackrest settings of the source database and of the restore in the
// target: a posix repository on a volume, encrypted like production's
// (roles/backup pgbackrest-repo.conf.j2), standing in for object storage
const pgbackrestConf = `[global]
repo1-path=/var/spool/pgbackrest/repo
repo1-cipher-type=aes-256-cbc
repo1-cipher-pass=%s
log-level-console=info
log-level-file=off
compress-type=zst
start-fast=y

[sa]
pg1-path=/var/lib/postgresql/18/docker
pg1-socket-path=/var/run/postgresql
`

// Stack is every container of one environment.
type Stack struct {
	Label  string
	ID     string
	Net    string
	Subnet netip.Prefix

	PGPort     string
	PostgresPW string
	Postgres   string
	UmamiDBPW  string
	DBEnv      map[string]string
	// pgbackrest repository volume and settings file of the source
	RepoVolume string
	RepoConf   string

	NATSPort string
	NC       *nats.Conn
	JS       jetstream.JetStream

	Umami     string
	UmamiPort string
	UmamiURL  *url.URL
	UmamiPW   string
	AppSecret string
	UserKey   string

	OAuthSecret string
	OAuthCtl    string

	Edge, Jobs, JobsOps string
	CADir               string
	// Routes maps host:port as the browser asks for it to the loopback
	// address of the published container port
	Routes map[string]string

	mu    sync.Mutex
	names []string
	nets  []string
	vols  []string
}

func newStack(t testing.TB, label string) *Stack {
	t.Helper()
	s := &Stack{Label: label, ID: randHex(4), Routes: map[string]string{}}
	t.Cleanup(func() { s.cleanup(t) })
	s.Net = "sa-ho-" + label + "-" + s.ID
	var err error
	for range 20 {
		n, _ := rand.Int(rand.Reader, big.NewInt(250))
		s.Subnet = netip.MustParsePrefix(fmt.Sprintf("10.217.%d.0/24", n.Int64()+1))
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

func (s *Stack) name(role string) string { return "sa-ho-" + s.Label + "-" + role + "-" + s.ID }

// cleanup removes every container, network and volume of the stack, after
// saving logs on failure. Remove calls it early to free the machine.
func (s *Stack) cleanup(t testing.TB) {
	s.mu.Lock()
	names := append([]string(nil), s.names...)
	nets := append([]string(nil), s.nets...)
	vols := append([]string(nil), s.vols...)
	s.names, s.nets, s.vols = nil, nil, nil
	s.mu.Unlock()
	if t.Failed() || os.Getenv("HANDOVER_LOGS") != "" {
		for _, n := range names {
			out, _ := exec.Command("docker", "logs", n).CombinedOutput()
			_ = os.WriteFile(filepath.Join(outDir(), n+".log"), out, 0o644)
		}
	}
	if os.Getenv("HANDOVER_KEEP") != "" {
		return
	}
	if s.NC != nil {
		s.NC.Close()
	}
	for i := len(names) - 1; i >= 0; i-- {
		_, _ = docker("rm", "-f", "-v", names[i])
	}
	for _, n := range nets {
		_, _ = docker("network", "rm", n)
	}
	for _, v := range vols {
		_, _ = docker("volume", "rm", "-f", v)
	}
}

func (s *Stack) track(name string) {
	s.mu.Lock()
	s.names = append(s.names, name)
	s.mu.Unlock()
}

func (s *Stack) run(t testing.TB, name string, opts []string, image string, args ...string) {
	t.Helper()
	all := append([]string{"run", "-d", "--name", name, "--network", s.Net}, opts...)
	all = append(all, image)
	all = append(all, args...)
	mustDocker(t, all...)
	s.track(name)
}

// mounted runs a static binary from bins on the distroless image.
func (s *Stack) mounted(t testing.TB, name, bin string, opts []string, env map[string]string, args ...string) {
	t.Helper()
	all := append([]string{"-v", bin + ":/bin/service:ro", "--entrypoint", "/bin/service"}, opts...)
	all = append(all, envOpts(env)...)
	s.run(t, name, all, staticImage, args...)
}

// oneShot runs a static binary to completion on the stack's network.
func (s *Stack) oneShot(t testing.TB, bin string, env map[string]string, args ...string) string {
	t.Helper()
	all := append([]string{"run", "--rm", "--network", s.Net, "-v", bin + ":/bin/service:ro", "--entrypoint", "/bin/service"}, envOpts(env)...)
	all = append(all, staticImage)
	all = append(all, args...)
	out, err := docker(all...)
	if err != nil {
		t.Fatalf("%s %v on stack %s: %v", filepath.Base(bin), args, s.Label, err)
	}
	return out
}

func (s *Stack) psql(t testing.TB, db, q string) string {
	t.Helper()
	return mustDocker(t, "exec", s.Postgres, "psql", "-X", "-q", "-h", "127.0.0.1", "-U", "postgres", "-d", db, "-tAc", q)
}

func (s *Stack) waitPostgres(t testing.TB, container string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := docker("exec", container, "psql", "-X", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-tAc",
			"select count(*) from pg_database where datname in ('umami','platform') and not pg_is_in_recovery()")
		if err == nil && out == "2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("postgres %s not ready: %v %q", container, err, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// StartPostgres runs postgresql with the dev init script (databases umami
// and platform), then applies the platform migrations and role passwords
// with platformctl, as site.yml's bootstrap does. with archive set, the
// server is the postgres-ha image archiving wal into a pgbackrest
// repository, as every production database node does.
func (s *Stack) StartPostgres(t testing.TB, archive bool) {
	t.Helper()
	_, pgImage := umamictr.Images(t)
	s.PostgresPW = randHex(24)
	s.UmamiDBPW = randHex(24)
	s.PGPort = freePort(t)
	s.Postgres = s.name("postgres")
	opts := []string{
		"--network-alias", "postgres", "-p", "127.0.0.1:" + s.PGPort + ":5432",
		"-v", filepath.Join(repoRoot(), "deploy", "compose", "postgres-init.sh") + ":/docker-entrypoint-initdb.d/10-init.sh:ro",
	}
	opts = append(opts, envOpts(map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": s.PostgresPW, "UMAMI_DB_PASSWORD": s.UmamiDBPW, "TZ": "UTC",
	})...)
	image := pgImage
	var args []string
	if archive {
		image = postgresHAImage
		dir := t.TempDir()
		_ = os.Chmod(dir, 0o755)
		s.RepoConf = filepath.Join(dir, "pgbackrest.conf")
		// test-only cipher passphrase, readable by the container's postgres user
		if err := os.WriteFile(s.RepoConf, []byte(fmt.Sprintf(pgbackrestConf, randHex(32))), 0o644); err != nil {
			t.Fatal(err)
		}
		s.RepoVolume = s.name("repo")
		mustDocker(t, "volume", "create", s.RepoVolume)
		s.vols = append(s.vols, s.RepoVolume)
		// the image has no gosu, so the official entrypoint runs as postgres
		opts = append(opts, "--user", "postgres",
			"-v", s.RepoVolume+":/var/spool/pgbackrest",
			"-v", s.RepoConf+":/etc/pgbackrest/pgbackrest.conf:ro")
		args = []string{"postgres", "-c", "wal_level=replica", "-c", "archive_mode=on",
			"-c", "archive_command=pgbackrest --stanza=sa archive-push %p"}
	}
	s.run(t, s.Postgres, opts, image, args...)
	s.waitPostgres(t, s.Postgres)
	if archive {
		mustDocker(t, "exec", s.Postgres, "pgbackrest", "--stanza=sa", "stanza-create")
		mustDocker(t, "exec", s.Postgres, "pgbackrest", "--stanza=sa", "check")
	}
	s.DBEnv = map[string]string{"PLATFORM_DB_NAME": "platform", "PLATFORM_DB_SSLMODE": "disable"}
	for _, r := range []string{"CP_SYSTEM", "CP_TENANT", "CP_OPERATOR", "METRICS_WRITER", "JOBS"} {
		s.DBEnv["PLATFORM_DB_"+r+"_PASSWORD"] = randHex(24)
	}
	for _, sub := range [][]string{{"db", "migrate"}, {"db", "roles"}} {
		s.hostPlatformctl(t, nil, sub...)
	}
	s.DBEnv["PLATFORM_DB_HOST"] = "postgres"
	s.DBEnv["PLATFORM_DB_PORT"] = "5432"
}

// hostEnv is what platformctl on this host needs to reach the stack.
func (s *Stack) hostEnv() map[string]string {
	env := map[string]string{
		"PLATFORM_DB_HOST": "127.0.0.1", "PLATFORM_DB_PORT": s.PGPort,
		"PLATFORM_DB_ADMIN_USER": "postgres", "PLATFORM_DB_ADMIN_PASSWORD": s.PostgresPW,
	}
	for k, v := range s.DBEnv {
		if k != "PLATFORM_DB_HOST" && k != "PLATFORM_DB_PORT" {
			env[k] = v
		}
	}
	env["PLATFORM_DB_NAME"] = "platform"
	env["PLATFORM_DB_SSLMODE"] = "disable"
	return env
}

func (s *Stack) hostPlatformctl(t testing.TB, extra map[string]string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bins.platformctl, args...)
	cmd.Env = os.Environ()
	for k, v := range s.hostEnv() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("platformctl %v on stack %s: %v\n%s", args, s.Label, err, out)
	}
	return string(out)
}

// EnvFile writes the env file scripts/migrate.sh and verify-migration read.
func (s *Stack) EnvFile(t testing.TB) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), s.Label+".env")
	lines := []string{
		"PLATFORM_DB_HOST=127.0.0.1", "PLATFORM_DB_PORT=" + s.PGPort, "PLATFORM_DB_NAME=platform",
		"PLATFORM_DB_SSLMODE=disable", "PLATFORM_DB_ADMIN_USER=postgres", "PLATFORM_DB_ADMIN_PASSWORD=" + s.PostgresPW,
		"UMAMI_DB_NAME=umami", "UMAMI_URL=" + s.UmamiURL.String(), "UMAMI_ADMIN_PASSWORD=" + s.UmamiPW,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// StartUmami runs umami with the environment of the design and, on a
// fresh database, replaces the seeded admin password.
func (s *Stack) StartUmami(t testing.TB, bootstrap bool) {
	t.Helper()
	umamiImage, _ := umamictr.Images(t)
	s.Umami = s.name("umami")
	if s.UmamiPort == "" {
		s.UmamiPort = freePort(t)
		s.AppSecret = randHex(24)
	}
	s.run(t, s.Umami, append([]string{"--network-alias", "umami", "-p", "127.0.0.1:" + s.UmamiPort + ":3000"},
		envOpts(map[string]string{
			"DATABASE_URL":       "postgresql://umami_app:" + s.UmamiDBPW + "@postgres:5432/umami",
			"APP_SECRET":         s.AppSecret,
			"CLIENT_IP_HEADER":   "x-edge-client-ip",
			"ENABLE_CLIENT_SALT": "1",
			"SALT_ROTATION":      "daily",
			"PRIVATE_MODE":       "1",
			"DISABLE_TELEMETRY":  "1",
			"DISABLE_UPDATES":    "1",
			"DISABLE_LOGIN":      "1",
			"TZ":                 "UTC",
		})...), umamiImage)
	s.UmamiURL = &url.URL{Scheme: "http", Host: "127.0.0.1:" + s.UmamiPort}
	waitHTTP(t, s.UmamiURL.String()+"/api/heartbeat", 5*time.Minute)
	if !bootstrap {
		return
	}
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

// Stop removes one container, keeping the stack's other state, so an
// import finds no open connections; Start* runs it again on the same port.
func (s *Stack) Stop(t testing.TB, names ...string) {
	t.Helper()
	for _, n := range names {
		if n == "" {
			continue
		}
		mustDocker(t, "rm", "-f", "-v", n)
		s.mu.Lock()
		for i, x := range s.names {
			if x == n {
				s.names = append(s.names[:i], s.names[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
}

// StartNATS runs nats with jetstream and applies the topology.
func (s *Stack) StartNATS(t testing.TB) {
	t.Helper()
	s.NATSPort = freePort(t)
	mon := freePort(t)
	s.run(t, s.name("nats"), []string{"--network-alias", "nats", "-p", "127.0.0.1:" + s.NATSPort + ":4222", "-p", "127.0.0.1:" + mon + ":8222"},
		devImage(t, natsImageLine), "--name", "ho-"+s.Label, "--jetstream", "--store_dir", "/data", "--http_port", "8222")
	waitHTTP(t, "http://127.0.0.1:"+mon+"/healthz?js-enabled-only=true", time.Minute)
	var err error
	s.NC, err = nats.Connect("nats://127.0.0.1:"+s.NATSPort, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
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

// StartOAuthFixture runs the TEST-ONLY github oauth fixture.
func (s *Stack) StartOAuthFixture(t testing.TB) {
	t.Helper()
	s.OAuthSecret = randHex(20)
	port := freePort(t)
	s.mounted(t, s.name("oauth"), bins.oauth,
		[]string{"--network-alias", oauthHost, "-p", "127.0.0.1:" + port + ":" + oauthPort}, nil,
		"-listen", ":"+oauthPort, "-client-id", oauthClientID, "-client-secret", s.OAuthSecret,
		"-redirect-url", "https://app."+baseDomain+"/api/auth/github/callback")
	s.OAuthCtl = "http://127.0.0.1:" + port
	waitHTTP(t, s.OAuthCtl+"/_fixture/healthz", 30*time.Second)
	s.Routes[oauthHost+":"+oauthPort] = "127.0.0.1:" + port
}

// common is the environment shared by the control plane and jobs.
func (s *Stack) common() map[string]string {
	env := map[string]string{
		"NATS_URL":                    "nats://nats:4222",
		"NATS_REPLICAS":               "1",
		"UMAMI_URL":                   "http://umami:3000",
		"UMAMI_ADMIN_PASSWORD":        s.UmamiPW,
		"UMAMI_USER_PASSWORD_KEY":     s.UserKey,
		"TRACKER_RELEASE_DIR":         "/releases",
		"API_MINUTE_RETENTION_DAYS":   "30",
		"API_HOURLY_RETENTION_MONTHS": "36",
		"TZ":                          "UTC",
	}
	for k, v := range s.DBEnv {
		env[k] = v
	}
	return env
}

func releasesMount() string {
	return filepath.Join(repoRoot(), "packages", "tracker", "releases") + ":/releases:ro"
}

// Jobs runs one `jobs run` task to completion with the stack's settings.
func (s *Stack) JobsRun(t testing.TB, args ...string) string {
	t.Helper()
	all := append([]string{"run", "--rm", "--network", s.Net, "-v", releasesMount(), "-v", bins.jobs + ":/bin/service:ro", "--entrypoint", "/bin/service"},
		envOpts(s.common())...)
	all = append(all, staticImage, "run")
	all = append(all, args...)
	out, err := docker(all...)
	if err != nil {
		t.Fatalf("jobs run %v on stack %s: %v", args, s.Label, err)
	}
	return out
}

// StartControlPlane runs the control plane (oauthfixture build) and the
// jobs daemon.
func (s *Stack) StartControlPlane(t testing.TB) {
	t.Helper()
	if s.UserKey == "" {
		s.UserKey = key32()
	}
	env := s.common()
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
		"OPERATOR_GITHUB_IDS":   "1",
	} {
		env[k] = v
	}
	ops := freePort(t)
	s.mounted(t, s.name("controlplane"), bins.controlplane,
		[]string{"--network-alias", "controlplane", "-p", "127.0.0.1:" + ops + ":9090", "-v", releasesMount()}, env)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)
	s.StartJobs(t)
}

func (s *Stack) StartJobs(t testing.TB) {
	t.Helper()
	s.JobsOps = freePort(t)
	s.Jobs = s.name("jobs")
	jenv := s.common()
	jenv["JOBS_OPS_ADDR"] = "0.0.0.0:9090"
	s.mounted(t, s.Jobs, bins.jobs, []string{"-p", "127.0.0.1:" + s.JobsOps + ":9090", "-v", releasesMount()}, jenv, "daemon")
	waitHTTP(t, "http://127.0.0.1:"+s.JobsOps+"/healthz", time.Minute)
}

// RestartJobs makes the daemon run every periodic task now: a daemon runs
// each task once as soon as it leads.
func (s *Stack) RestartJobs(t testing.TB) {
	t.Helper()
	mustDocker(t, "restart", "-t", "20", s.Jobs)
	waitHTTP(t, "http://127.0.0.1:"+s.JobsOps+"/healthz", time.Minute)
}

// StartReplayer drains WEB_EVENTS into umami.
func (s *Stack) StartReplayer(t testing.TB) {
	t.Helper()
	ops := freePort(t)
	s.mounted(t, s.name("replayer"), bins.replayer, []string{"-p", "127.0.0.1:" + ops + ":9090"}, map[string]string{
		"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "REPLAYER_NODE_ID": "core-1",
		"REPLAYER_OPS_ADDR": "0.0.0.0:9090", "UMAMI_URL": "http://umami:3000",
		"REPLAYER_WORKERS": "16", "REPLAYER_MAX_IN_FLIGHT": "512", "TZ": "UTC",
	})
	waitHTTP(t, "http://127.0.0.1:"+ops+"/healthz", time.Minute)
}

var gatewayEnv = map[string]string{
	"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "GATEWAY_NODE_ID": "edge-1",
	"GATEWAY_ADDR": "0.0.0.0:8080", "GATEWAY_OPS_ADDR": "0.0.0.0:9090", "PROOF_BITS": "12",
	"PROOF_REPLAY_LIMIT": "50", "IPCLUSTER_THRESHOLD": "20", "RATE_SITE_PER_SECOND": "200",
	"RATE_SITE_BURST": "400", "RATE_PREFIX_PER_SECOND": "20", "RATE_PREFIX_BURST": "60",
	"GATEWAY_MAX_BODY": "8192", "CLIENT_IP_HEADER": "X-Edge-Client-Ip",
	"GATEWAY_SECURE_TRANSPORT": "true", "TZ": "UTC",
}

// StartEdge runs the gateway image, the edge caddy and a forwarder on a
// german db-ip range, as tests/e2e-console does.
func (s *Stack) StartEdge(t testing.TB) {
	t.Helper()
	ops := freePort(t)
	s.run(t, s.name("gateway"), append([]string{"--network-alias", "edge-1", "-p", "127.0.0.1:" + ops + ":9090"}, envOpts(gatewayEnv)...), gatewayImage)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)

	s.Edge = s.name("edge")
	s.run(t, s.Edge, []string{
		"--network-alias", "in." + baseDomain, "--network-alias", "otlp." + baseDomain,
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "GATEWAY_UPSTREAM=edge-1:8080", "-e", "EDGE_TLS=internal",
		"-e", "COLLECTOR_GRPC_UPSTREAM=collector:4317", "-e", "COLLECTOR_HTTP_UPSTREAM=collector:4318",
		"-e", "COLLECTOR_HEALTH_UPSTREAM=collector:13133",
	}, umamictr.CaddyImage)
	s.copyEdgeCA(t)

	name := s.name("de")
	var subnet netip.Prefix
	var err error
	for _, p := range countrySubnets(t, "DE") {
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
	fwd := s.name("fwd")
	tlsPort, ctlPort := freePort(t), freePort(t)
	mustDocker(t, "run", "-d", "--name", fwd, "--network", name, "--ip", clientIP.String(),
		"-p", "127.0.0.1:"+tlsPort+":8443", "-p", "127.0.0.1:"+ctlPort+":8080",
		"-v", bins.fwd+":/fwd:ro", "--entrypoint", "/fwd", staticImage,
		"-listen", ":8443", "-control", ":8080", "-initial", "DE", "-route", "DE="+caddyIP.String()+":443")
	s.track(fwd)
	waitHTTP(t, "http://127.0.0.1:"+ctlPort+"/", 30*time.Second)
	s.Routes["in."+baseDomain+":443"] = "127.0.0.1:" + tlsPort
}

func (s *Stack) copyEdgeCA(t testing.TB) {
	t.Helper()
	s.CADir = t.TempDir()
	if err := os.Chmod(s.CADir, 0o755); err != nil {
		t.Fatal(err)
	}
	crt := filepath.Join(s.CADir, "root.crt")
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := docker("cp", s.Edge+":/data/caddy/pki/authorities/local/root.crt", crt); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("edge ca not created: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := os.Chmod(crt, 0o644); err != nil {
		t.Fatal(err)
	}
}

// StartCollector and StartMetricsWriter carry the api path; the tenant
// backend takes the example app's first export.
func (s *Stack) StartCollector(t testing.TB) {
	t.Helper()
	health := freePort(t)
	s.run(t, s.name("collector"), append([]string{"--network-alias", "collector", "-p", "127.0.0.1:" + health + ":13133"},
		envOpts(map[string]string{
			"NATS_URL": "nats://nats:4222", "NATS_USER": "", "NATS_PASSWORD": "",
			"COLLECTOR_GRPC_ADDR": "0.0.0.0:4317", "COLLECTOR_HTTP_ADDR": "0.0.0.0:4318",
			"COLLECTOR_HEALTH_ADDR": "0.0.0.0:13133", "COLLECTOR_MEMORY_LIMIT_MIB": "512",
			"COLLECTOR_MEMORY_SPIKE_MIB": "128", "COLLECTOR_RATE_REQUESTS_PER_SECOND": "200",
			"COLLECTOR_RATE_REQUESTS_BURST": "400", "COLLECTOR_RATE_ITEMS_PER_SECOND": "100000",
			"COLLECTOR_RATE_ITEMS_BURST": "200000", "TZ": "UTC",
		})...), collectorImage)
	waitHTTP(t, "http://127.0.0.1:"+health+"/", 2*time.Minute)
}

func (s *Stack) StartMetricsWriter(t testing.TB) {
	t.Helper()
	ops := freePort(t)
	env := map[string]string{
		"NATS_URL": "nats://nats:4222", "NATS_REPLICAS": "1", "METRICSWRITER_NODE_ID": "core-1",
		"METRICSWRITER_OPS_ADDR": "0.0.0.0:9090", "METRICSWRITER_BATCH": "256",
		"METRICSWRITER_FIRST_POINT_THRESHOLD": "120s", "METRICSWRITER_ROUTE_CAP": "300",
		"METRICSWRITER_SERVICE_CAP": "50", "TZ": "UTC",
	}
	for k, v := range s.DBEnv {
		env[k] = v
	}
	s.mounted(t, s.name("metricswriter"), bins.metricswriter, []string{"-p", "127.0.0.1:" + ops + ":9090"}, env)
	waitHTTP(t, "http://127.0.0.1:"+ops+"/readyz", time.Minute)
}

func (s *Stack) StartTenantBackend(t testing.TB) {
	t.Helper()
	cfg := filepath.Join(repoRoot(), "deploy", "compose", "examples", "tenant-backend.yaml")
	health := freePort(t)
	s.run(t, s.name("tenant-backend"), []string{
		"--network-alias", "tenant-backend", "-p", "127.0.0.1:" + health + ":13133",
		"-v", cfg + ":/etc/otelcol/tenant-backend.yaml:ro", "--tmpfs", "/out:mode=1777",
	}, tenantBackendImage, "--config=/etc/otelcol/tenant-backend.yaml")
	waitHTTP(t, "http://127.0.0.1:"+health+"/", time.Minute)
}

type appRequest struct {
	Service string `json:"service"`
	Token   string `json:"token"`
	Signals string `json:"signals"`
}

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

func (s *Stack) startApp(t testing.TB, r appRequest) (string, error) {
	if !serviceName.MatchString(r.Service) || !strings.HasPrefix(r.Token, "sa_otlp_") || r.Signals != "metrics,traces" {
		return "", fmt.Errorf("bad app request %+v", r)
	}
	port := freePort(t)
	name := s.name("svc-" + r.Service)
	args := append([]string{"run", "-d", "--name", name, "--network", s.Net, "-p", "127.0.0.1:" + port + ":8080",
		"-v", s.CADir + ":/edge-ca:ro"}, envOpts(map[string]string{
		"TENANT_OTLP_ENDPOINT": "http://tenant-backend:4318", "PLATFORM_OTLP_ENDPOINT": "https://otlp." + baseDomain,
		"PLATFORM_OTLP_TOKEN": r.Token, "EXPORT_SIGNALS": r.Signals, "METRICS_TEMPORALITY": "cumulative",
		"METRICS_HISTOGRAM": "explicit", "METRICS_EXPORT_INTERVAL_MS": "3000", "OTEL_SERVICE_NAME": r.Service,
		"SSL_CERT_FILE": "/edge-ca/root.crt", "PORT": "8080", "TZ": "UTC",
	})...)
	args = append(args, exampleImage)
	if _, err := docker(args...); err != nil {
		return "", err
	}
	s.track(name)
	base := "http://127.0.0.1:" + port
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return base, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("app %s not healthy: %v", name, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// StopApps removes the example apps, so their last export lands before the
// copy and none follows it.
func (s *Stack) StopApps(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	var apps []string
	for _, n := range s.names {
		if strings.Contains(n, "-svc-") {
			apps = append(apps, n)
		}
	}
	s.mu.Unlock()
	for _, n := range apps {
		// sigterm lets the sdk flush its last export before exit
		_, _ = docker("stop", "-t", "15", n)
	}
	s.Stop(t, apps...)
}

var siteID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// AppsControl serves POST /apps (start an example app with a token a spec
// created) and POST /sites/verify (the domain check result, as the console
// suite records it) on loopback for the specs.
func (s *Stack) AppsControl(t testing.TB) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /apps", func(w http.ResponseWriter, req *http.Request) {
		var r appRequest
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		base, err := s.startApp(t, r)
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

// StartWeb runs the web caddy in front of umami; StartApp the app caddy with
// the console build and the control plane behind /api.
func (s *Stack) StartWeb(t testing.TB) {
	t.Helper()
	port := freePort(t)
	s.run(t, s.name("web"), []string{
		"-p", "127.0.0.1:" + port + ":443",
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "web.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=" + baseDomain, "-e", "UMAMI_UPSTREAM=" + umamictr.UpstreamAddr, "-e", "WEB_TLS=internal",
	}, caddyPluginImage)
	s.Routes["web."+baseDomain+":443"] = "127.0.0.1:" + port
	waitTLS(t, "127.0.0.1:"+port, "web."+baseDomain, "/api/heartbeat")
}

func (s *Stack) StartApp(t testing.TB) {
	t.Helper()
	port := freePort(t)
	s.run(t, s.name("app"), append([]string{
		"-p", "127.0.0.1:" + port + ":443",
		"-v", filepath.Join(repoRoot(), "deploy", "caddy", "app.Caddyfile") + ":/etc/caddy/Caddyfile:ro",
		"-v", filepath.Join(repoRoot(), "apps", "console", "dist") + ":/srv/console:ro",
	}, envOpts(map[string]string{
		"BASE_DOMAIN": baseDomain, "APP_TLS": "internal", "CONTROLPLANE_UPSTREAM": "controlplane:8081", "CONSOLE_ROOT": "/srv/console",
	})...), umamictr.CaddyImage)
	s.Routes["app."+baseDomain+":443"] = "127.0.0.1:" + port
	waitTLS(t, "127.0.0.1:"+port, "app."+baseDomain, "/")
}

func waitTLS(t testing.TB, addr, host, path string) {
	t.Helper()
	c := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true}, //nolint:gosec // caddy's internal ca of a test container
		},
	}
	deadline := time.Now().Add(time.Minute)
	last := ""
	for time.Now().Before(deadline) {
		resp, err := c.Get("https://" + host + path)
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

var natsImageLine = regexp.MustCompile(`(?m)^\s*image:\s*(nats:[^\s]+@sha256:[0-9a-f]{64})\s*$`)

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

// countrySubnets picks /24 subnets the gateway image's db-ip file assigns
// to a country (the method of tests/webpath).
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

// switchboard is the name service of the run: every hostname the browsers
// use is a loopback listener here that forwards to one environment's
// published port. pointing a name at the other environment is what a dns
// change does after a domain transfer (docs/handover/plan.md section 5).
type switchboard struct {
	mu      sync.Mutex
	targets map[string]string
	addrs   map[string]string
}

func newSwitchboard(t testing.TB, hosts []string) *switchboard {
	t.Helper()
	sb := &switchboard{targets: map[string]string{}, addrs: map[string]string{}}
	for _, h := range hosts {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		sb.addrs[h] = l.Addr().String()
		go func(h string) {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go sb.pipe(h, c)
			}
		}(h)
	}
	return sb
}

func (sb *switchboard) pipe(host string, c net.Conn) {
	defer c.Close()
	sb.mu.Lock()
	to := sb.targets[host]
	sb.mu.Unlock()
	if to == "" {
		return
	}
	u, err := net.Dial("tcp", to)
	if err != nil {
		return
	}
	defer u.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
	<-done
}

// Point sends every name to the stack's ports; connections already open
// stay where they are, as with a dns change.
func (sb *switchboard) Point(t testing.TB, s *Stack) {
	t.Helper()
	sb.mu.Lock()
	defer sb.mu.Unlock()
	for h := range sb.addrs {
		to, ok := s.Routes[h]
		if !ok {
			t.Fatalf("stack %s has no route for %s", s.Label, h)
		}
		sb.targets[h] = to
	}
}

// Routes is the browser proxy's table: every name to its switchboard port.
func (sb *switchboard) Routes() map[string]string {
	out := map[string]string{}
	for h, a := range sb.addrs {
		out[h] = a
	}
	return out
}
