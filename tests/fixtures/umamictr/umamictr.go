// Package umamictr starts a private umami with its own postgresql in docker
// for tests: the images pinned in deploy/compose/dev.yml, the environment of
// the design and the dev init script, on a dedicated docker network, so
// a test never touches the shared dev stack. the seeded admin password is
// replaced with a random one through umami.Bootstrap. only *_test.go files
// import it.
package umamictr

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	mobynet "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/justmert/veristar/internal/umami"
)

// UpstreamAddr is where umami listens for containers on Stack.Network.
const UpstreamAddr = "umami:3000"

// Stack is one running umami with its database.
type Stack struct {
	// Network is the docker network umami and postgres share. other
	// containers join it to reach umami at UpstreamAddr.
	Network *testcontainers.DockerNetwork
	// URL is umami as seen from the test process, the private-network view.
	URL           *url.URL
	Client        *umami.Client
	Admin         *umami.Admin
	AdminPassword string
	Umami         *testcontainers.DockerContainer
	// Postgres runs umami's database; PostgresHost and PostgresPort reach it
	// from the test process as the superuser postgres with
	// PostgresPassword, for tests that check umami's tables directly.
	Postgres         *testcontainers.DockerContainer
	PostgresHost     string
	PostgresPort     int
	PostgresPassword string
}

func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("umamictr: cannot locate source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

var (
	umamiLine    = regexp.MustCompile(`(?m)^\s*image:\s*(ghcr\.io/umami-software/umami:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
	postgresLine = regexp.MustCompile(`(?m)^\s*image:\s*(postgres:[^\s]+@sha256:[0-9a-f]{64})\s*$`)
)

// Images returns the digest-pinned umami and postgres references from dev.yml,
// so tests and the dev stack run the same builds.
func Images(t testing.TB) (umamiImage, postgresImage string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "compose", "dev.yml"))
	if err != nil {
		t.Fatalf("umamictr: read dev.yml: %v", err)
	}
	u := umamiLine.FindSubmatch(b)
	p := postgresLine.FindSubmatch(b)
	if u == nil || p == nil {
		t.Fatal("umamictr: dev.yml lacks a digest-pinned umami or postgres image")
	}
	return string(u[1]), string(p[1])
}

// newNetwork creates the stack's network on an explicit /24, as the gateway
// tests do: docker's default address pools run out on a host with many
// compose projects, and an explicit subnet does not draw from them. a subnet
// another network already holds is refused by docker, so a few are tried.
func newNetwork(ctx context.Context) (*testcontainers.DockerNetwork, error) {
	var err error
	for range 20 {
		var b [1]byte
		if _, err = rand.Read(b[:]); err != nil {
			return nil, err
		}
		subnet := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 213, b[0], 0}), 24)
		var nw *testcontainers.DockerNetwork
		nw, err = tcnetwork.New(ctx, tcnetwork.WithIPAM(&mobynet.IPAM{
			Driver: "default",
			Config: []mobynet.IPAMConfig{{Subnet: subnet}},
		}))
		if err == nil {
			return nw, nil
		}
	}
	return nil, err
}

func secret(t testing.TB) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("umamictr: random: %v", err)
	}
	return hex.EncodeToString(b)
}

// Start runs postgres and umami and returns once umami answers its heartbeat
// and the admin password is rotated. containers and the network are removed
// when the test ends.
func Start(t testing.TB) *Stack {
	t.Helper()
	ctx := context.Background()
	umamiImage, pgImage := Images(t)

	nw, err := newNetwork(ctx)
	if err != nil {
		t.Fatalf("umamictr: network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	dbPassword := secret(t)
	superPassword := secret(t)
	pg, err := testcontainers.Run(ctx, pgImage,
		testcontainers.WithExposedPorts("5432/tcp"),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": superPassword,
			"UMAMI_DB_PASSWORD": dbPassword,
			"TZ":                "UTC",
		}),
		// the same init script as the dev stack: database umami owned by the
		// non-superuser login role umami_app
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      filepath.Join(repoRoot(t), "deploy", "compose", "postgres-init.sh"),
			ContainerFilePath: "/docker-entrypoint-initdb.d/10-init.sh",
			FileMode:          0o755,
		}),
		tcnetwork.WithNetwork([]string{"postgres"}, nw),
		// the entrypoint runs a temporary server for init and logs "ready"
		// once for it and once for the real server
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	testcontainers.CleanupContainer(t, pg)
	if err != nil {
		t.Fatalf("umamictr: postgres: %v", err)
	}

	// environment of the design, as in deploy/compose/dev.yml.
	// REDIS_URL, CLICKHOUSE_URL, KAFKA_URL, CLOUD_MODE, MCP_ENABLED,
	// DISABLE_BOT_CHECK and SKIP_LOCATION_HEADERS stay unset on purpose.
	um, err := testcontainers.Run(ctx, umamiImage,
		testcontainers.WithEnv(map[string]string{
			"DATABASE_URL":       "postgresql://umami_app:" + dbPassword + "@postgres:5432/umami",
			"APP_SECRET":         secret(t),
			"CLIENT_IP_HEADER":   "x-edge-client-ip",
			"ENABLE_CLIENT_SALT": "1",
			"SALT_ROTATION":      "daily",
			"PRIVATE_MODE":       "1",
			"DISABLE_TELEMETRY":  "1",
			"DISABLE_UPDATES":    "1",
			"DISABLE_LOGIN":      "1",
			"TZ":                 "UTC",
		}),
		testcontainers.WithExposedPorts("3000/tcp"),
		tcnetwork.WithNetwork([]string{"umami"}, nw),
		// the first boot runs every prisma migration before the server listens
		testcontainers.WithWaitStrategy(wait.ForHTTP("/api/heartbeat").WithPort("3000/tcp").
			WithStartupTimeout(4*time.Minute)),
	)
	testcontainers.CleanupContainer(t, um)
	if err != nil {
		t.Fatalf("umamictr: umami: %v", err)
	}
	endpoint, err := um.PortEndpoint(ctx, "3000/tcp", "http")
	if err != nil {
		t.Fatalf("umamictr: umami endpoint: %v", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("umamictr: %v", err)
	}
	c, err := umami.New(u, nil)
	if err != nil {
		t.Fatalf("umamictr: %v", err)
	}
	pw := secret(t)
	bctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := umami.Bootstrap(bctx, c, pw); err != nil {
		t.Fatalf("umamictr: bootstrap admin: %v", err)
	}
	a, err := umami.NewAdmin(c, umami.SeededUsername, pw)
	if err != nil {
		t.Fatalf("umamictr: %v", err)
	}
	pgHost, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("umamictr: postgres host: %v", err)
	}
	pgPort, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("umamictr: postgres port: %v", err)
	}
	return &Stack{Network: nw, URL: u, Client: c, Admin: a, AdminPassword: pw, Umami: um,
		Postgres: pg, PostgresHost: pgHost, PostgresPort: int(pgPort.Num()), PostgresPassword: superPassword}
}

// CaddyImage is caddy 2.11.9 by the digest pinned in the design: the
// final stage of deploy/images/caddy.Dockerfile. the deploy/caddy tests check
// that the dockerfile builds on it.
const CaddyImage = "caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1"

// WebCaddyTag names the image built from deploy/images/caddy.Dockerfile: caddy
// with the replace-response plugin, which web.Caddyfile needs for the db-ip
// attribution. stock caddy refuses that file.
const WebCaddyTag = "veristar/caddy:2.11.9-test"

var (
	webImageOnce sync.Once
	webImageErr  error
	webImageOut  string
)

// WebCaddyImage builds deploy/images/caddy.Dockerfile once per test process
// and returns its tag. docker's layer cache makes later builds take seconds;
// the first one compiles caddy.
func WebCaddyImage(t testing.TB) string {
	t.Helper()
	root := repoRoot(t)
	webImageOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		//nolint:gosec // fixed arguments: the repository's own dockerfile and tag
		cmd := exec.CommandContext(ctx, "docker", "build", "--quiet",
			"-f", filepath.Join(root, "deploy", "images", "caddy.Dockerfile"),
			"-t", WebCaddyTag, filepath.Join(root, "deploy", "images"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			webImageErr = err
			webImageOut = string(out)
		}
	})
	if webImageErr != nil {
		t.Fatalf("umamictr: build %s: %v\n%s", WebCaddyTag, webImageErr, webImageOut)
	}
	return WebCaddyTag
}

// WebDomain is BASE_DOMAIN for the web caddy; the site is web.WebDomain.
const WebDomain = "umami.test"

// Web is a caddy running deploy/caddy/web.Caddyfile in front of a Stack: the
// public view of web.<BASE_DOMAIN>.
type Web struct {
	// Base is https://127.0.0.1:<port>; every request needs Host set to Host.
	Base      string
	Host      string
	HTTP      *http.Client
	Container *testcontainers.DockerContainer
}

// StartWeb runs the web caddy on the stack's network with tls from caddy's
// local ca, and returns once it answers over tls.
func StartWeb(t testing.TB, s *Stack) *Web {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, WebCaddyImage(t),
		testcontainers.WithEnv(map[string]string{
			"BASE_DOMAIN":    WebDomain,
			"UMAMI_UPSTREAM": UpstreamAddr,
			"WEB_TLS":        "internal",
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      filepath.Join(repoRoot(t), "deploy", "caddy", "web.Caddyfile"),
			ContainerFilePath: "/etc/caddy/Caddyfile",
			FileMode:          0o644,
		}),
		testcontainers.WithExposedPorts("443/tcp"),
		tcnetwork.WithNetwork([]string{"web"}, s.Network),
		testcontainers.WithWaitStrategy(wait.ForLog("serving initial configuration").WithStartupTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("umamictr: web caddy: %v", err)
	}
	endpoint, err := c.PortEndpoint(ctx, "443/tcp", "")
	if err != nil {
		t.Fatalf("umamictr: web caddy endpoint: %v", err)
	}
	w := &Web{
		Base: "https://" + endpoint,
		Host: "web." + WebDomain,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			// redirects are part of what the tests check
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				// caddy's local ca signs the certificate; the tests check
				// routing and headers, not the chain
				TLSClientConfig: &tls.Config{ServerName: "web." + WebDomain, InsecureSkipVerify: true}, //nolint:gosec // test-only client for caddy's internal ca
			},
		},
		Container: c,
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.Base+"/api/heartbeat", nil)
		if err != nil {
			t.Fatalf("umamictr: %v", err)
		}
		req.Host = w.Host
		resp, err := w.HTTP.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return w
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("umamictr: web caddy does not proxy /api/heartbeat: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Logs returns everything the container wrote so far.
func Logs(t testing.TB, c *testcontainers.DockerContainer) string {
	t.Helper()
	r, err := c.Logs(context.Background())
	if err != nil {
		t.Fatalf("umamictr: logs: %v", err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("umamictr: logs: %v", err)
	}
	return string(b)
}

// canonical is the route umami serves for path: lower case, without the
// query and without leading /teams/<id> segments, which umami's
// next.config.ts rewrites away.
func canonical(path string) string {
	p := strings.ToLower(path)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	for {
		rest, ok := strings.CutPrefix(p, "/teams/")
		if !ok {
			break
		}
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			break
		}
		p = rest[i:]
	}
	return p
}

// userReads are the two /api/users routes the dashboard reads for its team
// and website switchers. umami answers them only about the caller.
var userReads = regexp.MustCompile(`^/api/users/[^/]+/(teams|websites)/?$`)

// DeniedPath reports whether web.<BASE_DOMAIN> must refuse path for every
// method: the deny list of the design, written out a second time in go,
// independently of the caddyfile's regular expression. /compat, the target of
// umami's legacy report rewrites, and /api/auth/subscription, which hangs
// without redis, are covered as well. /api/users* is denied except the two
// reads in userReads, which RefusedAtEdge allows for GET and HEAD only.
func DeniedPath(path string) bool {
	p := canonical(path)
	if strings.HasPrefix(p, "/api/users") && !userReads.MatchString(p) {
		return true
	}
	exact := []string{
		"/api/send", "/api/batch", "/api/record", "/api/auth/login", "/api/auth/sso", "/api/auth/subscription",
		"/api/me/password", "/api/teams/join", "/api/scripts/telemetry", "/telemetry.js",
		"/openapi.json", "/script.js", "/recorder.js",
	}
	for _, e := range exact {
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
	}
	for _, pre := range []string{"/api/admin", "/api/me/api-keys", "/api/share", "/api/links", "/api/pixels", "/api/boards", "/mcp"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	for _, dir := range []string{"/share", "/q", "/p", "/compat"} {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return strings.HasPrefix(p, "/api/2fa") && p != "/api/2fa/status"
}

// RefusedAtEdge reports whether web.<BASE_DOMAIN> must answer 403 to method
// on path: a denied path, or a method outside the allowlist on /api/* (plan
// 4.9: GET, HEAD and OPTIONS, plus POST to /api/auth/verify and
// /api/auth/logout).
func RefusedAtEdge(method, path string) bool {
	if DeniedPath(path) {
		return true
	}
	p := canonical(path)
	// these two allow only reads, and caddy refuses OPTIONS on them as well
	if userReads.MatchString(p) || p == "/api/2fa/status" || p == "/api/2fa/status/" {
		return method != http.MethodGet && method != http.MethodHead
	}
	if p != "/api" && !strings.HasPrefix(p, "/api/") {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	case http.MethodPost:
		return p != "/api/auth/verify" && p != "/api/auth/logout"
	}
	return true
}
