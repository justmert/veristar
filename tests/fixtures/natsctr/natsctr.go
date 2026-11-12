// Package natsctr starts real nats servers in docker for tests: a single
// server, a server with an authorization file, and a 3-node cluster. it uses
// the image pinned in deploy/compose/dev.yml, so tests and the dev stack run
// the same server build. only *_test.go files import it.
package natsctr

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	mobynet "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RepoRoot returns the repository root, found from this file's location.
func RepoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("natsctr: cannot locate source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

var imageLine = regexp.MustCompile(`(?m)^\s*image:\s*(nats:[^\s]+@sha256:[0-9a-f]{64})\s*$`)

// Image returns the nats image reference, with digest, from dev.yml.
func Image(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(RepoRoot(t), "deploy", "compose", "dev.yml"))
	if err != nil {
		t.Fatalf("natsctr: read dev.yml: %v", err)
	}
	m := imageLine.FindSubmatch(b)
	if m == nil {
		t.Fatal("natsctr: no digest-pinned nats image in dev.yml")
	}
	return string(m[1])
}

// Server is one running nats server.
type Server struct {
	Name       string
	URL        string // nats://host:port as seen from the test process
	MonitorURL string // http://host:port of the monitoring endpoint
	Container  *testcontainers.DockerContainer
}

// Stop stops the container without removing it, so file storage survives.
func (s *Server) Stop(t testing.TB) {
	t.Helper()
	timeout := 10 * time.Second
	if err := s.Container.Stop(context.Background(), &timeout); err != nil {
		t.Fatalf("natsctr: stop %s: %v", s.Name, err)
	}
}

// Start starts a stopped container again on the same host ports.
func (s *Server) Start(t testing.TB) {
	t.Helper()
	if err := s.Container.Start(context.Background()); err != nil {
		t.Fatalf("natsctr: start %s: %v", s.Name, err)
	}
	WaitHealthy(t, s.MonitorURL, "js-enabled-only=true", time.Minute)
}

// freePort asks the kernel for an unused port. a restarted container must
// come back on the same host port, so ports are bound explicitly instead of
// letting docker pick a new one on every start.
func freePort() (string, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("natsctr: free port: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("natsctr: listener address %v is not tcp", l.Addr())
	}
	return strconv.Itoa(addr.Port), nil
}

type options struct {
	image   string
	config  string
	network *testcontainers.DockerNetwork
	alias   string
	args    []string
}

// Option customises one server.
type Option func(*options)

// WithConfig starts the server with this configuration file content.
func WithConfig(content string) Option { return func(o *options) { o.config = content } }

// start must not call t.Fatal, because StartCluster runs it in goroutines.
func start(t testing.TB, name string, o options) (*Server, error) {
	ctx := context.Background()
	clientPort, err := freePort()
	if err != nil {
		return nil, err
	}
	monitorPort, err := freePort()
	if err != nil {
		return nil, err
	}
	cmd := []string{"--name", name, "--jetstream", "--store_dir", "/data", "--http_port", "8222"}
	if o.config != "" {
		cmd = append(cmd, "--config", "/etc/nats/test.conf")
	}
	cmd = append(cmd, o.args...)
	loopback := netip.MustParseAddr("127.0.0.1")
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmd(cmd...),
		testcontainers.WithExposedPorts("4222/tcp", "8222/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = mobynet.PortMap{
				mobynet.MustParsePort("4222/tcp"): {{HostIP: loopback, HostPort: clientPort}},
				mobynet.MustParsePort("8222/tcp"): {{HostIP: loopback, HostPort: monitorPort}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/healthz?js-enabled-only=true").
			WithPort("8222/tcp").WithStartupTimeout(time.Minute)),
	}
	if o.config != "" {
		opts = append(opts, testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(o.config),
			ContainerFilePath: "/etc/nats/test.conf",
			FileMode:          0o644,
		}))
	}
	if o.network != nil {
		opts = append(opts, tcnetwork.WithNetwork([]string{o.alias}, o.network))
	}
	c, err := testcontainers.Run(ctx, o.image, opts...)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		return nil, fmt.Errorf("natsctr: start %s: %w", name, err)
	}
	return &Server{
		Name:       name,
		URL:        "nats://127.0.0.1:" + clientPort,
		MonitorURL: "http://127.0.0.1:" + monitorPort,
		Container:  c,
	}, nil
}

// StartSingle starts one server with jetstream.
func StartSingle(t testing.TB, opts ...Option) *Server {
	t.Helper()
	o := options{image: Image(t)}
	for _, f := range opts {
		f(&o)
	}
	s, err := start(t, "single", o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Cluster is a 3-node jetstream cluster.
type Cluster struct {
	Servers []*Server
}

// URLs returns every client url, comma separated, for nats.Connect.
func (c *Cluster) URLs() string {
	var u []string
	for _, s := range c.Servers {
		u = append(u, s.URL)
	}
	return strings.Join(u, ",")
}

// StartCluster starts three routed servers and waits until jetstream has a
// meta leader on every node. opts apply to every node, so WithConfig gives
// the whole cluster the same authorization.
func StartCluster(t testing.TB, opts ...Option) *Cluster {
	t.Helper()
	var base options
	for _, f := range opts {
		f(&base)
	}
	ctx := context.Background()
	nw, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("natsctr: network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	names := []string{"n1", "n2", "n3"}
	routes := make([]string, len(names))
	for i, n := range names {
		routes[i] = "nats://" + n + ":6222"
	}
	image := Image(t)
	type result struct {
		i   int
		s   *Server
		err error
	}
	// all three start together: each one waits for jetstream, which needs
	// the routes to its peers
	ch := make(chan result, len(names))
	for i, n := range names {
		go func() {
			s, err := start(t, n, options{
				image:   image,
				config:  base.config,
				network: nw,
				alias:   n,
				args: []string{
					"--cluster_name", "test",
					"--cluster", "nats://0.0.0.0:6222",
					"--routes", strings.Join(routes, ","),
					// the advertised addresses are docker-internal names the
					// test process cannot resolve
					"--no_advertise",
				},
			})
			ch <- result{i, s, err}
		}()
	}
	c := &Cluster{Servers: make([]*Server, len(names))}
	var errs []error
	for range names {
		r := <-ch
		c.Servers[r.i] = r.s
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for _, s := range c.Servers {
		WaitHealthy(t, s.MonitorURL, "", 2*time.Minute)
	}
	return c
}

// Restart stops every node, starts them all again and waits for jetstream.
// memory-backed streams lose their messages; file-backed ones keep them.
func (c *Cluster) Restart(t testing.TB) {
	t.Helper()
	for _, s := range c.Servers {
		s.Stop(t)
	}
	// all nodes start together, because each waits for its peers
	errs := make(chan error, len(c.Servers))
	for _, s := range c.Servers {
		go func() { errs <- s.Container.Start(context.Background()) }()
	}
	for range c.Servers {
		if err := <-errs; err != nil {
			t.Fatalf("natsctr: restart: %v", err)
		}
	}
	for _, s := range c.Servers {
		WaitHealthy(t, s.MonitorURL, "", 2*time.Minute)
	}
}

// WaitHealthy polls /healthz until it answers 200.
func WaitHealthy(t testing.TB, monitorURL, query string, timeout time.Duration) {
	t.Helper()
	u := monitorURL + "/healthz"
	if query != "" {
		u += "?" + query
	}
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		if err != nil {
			t.Fatalf("natsctr: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)

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
	t.Fatalf("natsctr: %s not healthy after %s: %s", u, timeout, last)
}
