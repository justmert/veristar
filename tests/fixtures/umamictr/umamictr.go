// Package umamictr starts a private umami with its own postgresql in docker
// for tests: the images pinned in deploy/compose/dev.yml, the umami
// environment of that file and the dev init script, on a dedicated docker network, so
// a test never touches the shared dev stack. the seeded admin password is
// replaced with a random one through umami.Bootstrap. only *_test.go files
// import it.
package umamictr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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

	// the umami environment of deploy/compose/dev.yml.
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

// CaddyImage is caddy 2.11.9 by its pinned digest: the
// final stage of deploy/images/caddy.Dockerfile. the deploy/caddy tests check
// that the dockerfile builds on it.
const CaddyImage = "caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1"

// PlatformCaddyTag names the image built from deploy/images/caddy.Dockerfile:
// caddy with the cloudflare dns plugin, which the assembled node configs
// name. stock caddy refuses them.
const PlatformCaddyTag = "veristar/caddy:2.11.9-test"

var (
	caddyImageOnce sync.Once
	caddyImageErr  error
	caddyImageOut  string
)

// PlatformCaddyImage builds deploy/images/caddy.Dockerfile once per test process
// and returns its tag. docker's layer cache makes later builds take seconds;
// the first one compiles caddy.
func PlatformCaddyImage(t testing.TB) string {
	t.Helper()
	root := repoRoot(t)
	caddyImageOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		//nolint:gosec // fixed arguments: the repository's own dockerfile and tag
		cmd := exec.CommandContext(ctx, "docker", "build", "--quiet",
			"-f", filepath.Join(root, "deploy", "images", "caddy.Dockerfile"),
			"-t", PlatformCaddyTag, filepath.Join(root, "deploy", "images"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			caddyImageErr = err
			caddyImageOut = string(out)
		}
	})
	if caddyImageErr != nil {
		t.Fatalf("umamictr: build %s: %v\n%s", PlatformCaddyTag, caddyImageErr, caddyImageOut)
	}
	return PlatformCaddyTag
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
