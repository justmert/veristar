//go:build gatewayit

// Package gatewayit holds the this stage verification tests of the collect
// gateway. they run the real gateway image (deploy/images/gateway.Dockerfile),
// real nats, the edge caddy (deploy/caddy/edge.Caddyfile) and real browsers
// (tests/e2e in its real-gateway mode), so they sit behind the build tag
// gatewayit and run with `make gateway-it`. tests that need an injected
// clock (expiry, the key overlap, midnight) or direct access to the stats
// publisher run the same gateway.Node in process against the same nats.
package gatewayit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

const (
	gatewayImage = "stellar-analytics/gateway:gatewayit"
	caddyImage   = "caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1"
	staticImage  = "gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
	// the pinned load tool of the design
	k6Image = "grafana/k6:2.3.0@sha256:2dd4756ec6e14e62778916e1eb850108e4b499304c02df56760fa0bdb0fba0b8"

	gatewayHost = "in.sa-test.example"
	clientIPHdr = "X-Edge-Client-Ip"
)

// starting values from docs/reference/gateway-limits.md
var gatewayEnv = map[string]string{
	"NATS_REPLICAS":            "1",
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
	"CLIENT_IP_HEADER":         clientIPHdr,
	"GATEWAY_SECURE_TRANSPORT": "true",
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestMain(m *testing.M) {
	month := os.Getenv("DBIP_MONTH")
	if month == "" {
		fmt.Fprintln(os.Stderr, "gatewayit: DBIP_MONTH (YYYY-MM) is required; make gateway-it sets it")
		os.Exit(2)
	}
	cmd := exec.Command("docker", "build", "-q", "-f", "deploy/images/gateway.Dockerfile",
		"--build-arg", "DBIP_MONTH="+month, "-t", gatewayImage, ".")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "gatewayit: build gateway image: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// docker runs the docker cli and returns trimmed stdout. stdout is returned
// on failure too: a container that exits non-zero (k6 crossing a threshold)
// has printed its own diagnosis there.
func docker(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, errb.String())
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

// logs returns stdout and stderr of a container.
func logs(t testing.TB, name string) string {
	t.Helper()
	out, err := exec.Command("docker", "logs", name).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s: %v", name, err)
	}
	return string(out)
}

func hostPort(t testing.TB, name, port string) string {
	t.Helper()
	out := mustDocker(t, "port", name, port)
	// one line per address family; the loopback ipv4 binding is first
	return strings.TrimSpace(strings.Split(out, "\n")[0])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Stack is one dedicated docker network with nats, gateways and caddy.
type Stack struct {
	t       testing.TB
	ID      string
	Net     string
	Subnet  string // 10.213.N, away from the 172.16/12 pools docker hands out
	NATS    string // container name
	NATSURL string
	NATSMon string
	NC      *nats.Conn
	JS      jetstream.JetStream
	names   []string
}

// Gateway is one running gateway container.
type Gateway struct {
	Name, Node string
	// URL is the public port on loopback, reached without caddy; tests that
	// stand in for caddy set X-Edge-Client-Ip themselves.
	URL    string
	OpsURL string
}

// newStack creates the network and starts nats with the topology applied.
func newStack(t testing.TB) *Stack {
	t.Helper()
	s := &Stack{t: t, ID: randHex(4)}
	s.Net = "sa-gwit-" + s.ID
	var err error
	for range 20 {
		n, _ := rand.Int(rand.Reader, big.NewInt(250))
		s.Subnet = fmt.Sprintf("10.213.%d", n.Int64()+1)
		if _, err = docker("network", "create", "--subnet", s.Subnet+".0/24", s.Net); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.cleanup)
	s.NATS = "sa-gwit-nats-" + s.ID
	s.run(t, s.NATS, []string{"--network-alias", "nats", "-p", "127.0.0.1::4222", "-p", "127.0.0.1::8222"},
		natsctr.Image(t), "--name", "it", "--jetstream", "--store_dir", "/data", "--http_port", "8222")
	s.NATSURL = "nats://" + hostPort(t, s.NATS, "4222")
	s.NATSMon = "http://" + hostPort(t, s.NATS, "8222")
	natsctr.WaitHealthy(t, s.NATSMon, "js-enabled-only=true", time.Minute)
	s.NC, err = nats.Connect(s.NATSURL, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
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
	return s
}

func (s *Stack) run(t testing.TB, name string, opts []string, image string, args ...string) {
	t.Helper()
	all := append([]string{"run", "-d", "--name", name, "--network", s.Net}, opts...)
	all = append(all, image)
	all = append(all, args...)
	mustDocker(t, all...)
	s.names = append(s.names, name)
}

func (s *Stack) cleanup() {
	if os.Getenv("GATEWAYIT_KEEP") != "" {
		return
	}
	for _, n := range s.names {
		_, _ = docker("rm", "-f", "-v", n)
	}
	_, _ = docker("network", "rm", s.Net)
}

// AddGateway starts a gateway container; override changes env values.
func (s *Stack) AddGateway(t testing.TB, node string, override map[string]string) *Gateway {
	t.Helper()
	g := &Gateway{Name: "sa-gwit-" + node + "-" + s.ID, Node: node}
	env := map[string]string{"NATS_URL": "nats://nats:4222", "GATEWAY_NODE_ID": node}
	for k, v := range gatewayEnv {
		env[k] = v
	}
	for k, v := range override {
		env[k] = v
	}
	opts := []string{"--network-alias", node, "-p", "127.0.0.1::8080", "-p", "127.0.0.1::9090"}
	for k, v := range env {
		opts = append(opts, "-e", k+"="+v)
	}
	s.run(t, g.Name, opts, gatewayImage)
	g.URL = "http://" + hostPort(t, g.Name, "8080")
	g.OpsURL = "http://" + hostPort(t, g.Name, "9090")
	waitReady(t, g.OpsURL+"/readyz", time.Minute)
	return g
}

// AddCaddy starts the edge caddy in front of upstream (host:port on the
// network) and returns the loopback https address and the container name.
func (s *Stack) AddCaddy(t testing.TB, upstream string) (addr, name string) {
	t.Helper()
	name = "sa-gwit-caddy-" + s.ID
	caddyfile := filepath.Join(repoRoot(), "deploy", "caddy", "edge.Caddyfile")
	s.run(t, name, []string{
		"--network-alias", gatewayHost, "-p", "127.0.0.1::443",
		"-v", caddyfile + ":/etc/caddy/Caddyfile:ro",
		"-e", "BASE_DOMAIN=sa-test.example", "-e", "GATEWAY_UPSTREAM=" + upstream, "-e", "EDGE_TLS=internal",
		// the edge file requires the otlp upstreams; this suite never sends otlp,
		// so they name a host that does not exist rather than a running collector
		"-e", "COLLECTOR_GRPC_UPSTREAM=no-collector:4317", "-e", "COLLECTOR_HTTP_UPSTREAM=no-collector:4318",
		"-e", "COLLECTOR_HEALTH_UPSTREAM=no-collector:13133",
	}, caddyImage)
	addr = hostPort(t, name, "443")
	// ready when caddy answers a tracker request from the gateway
	c := httpsClient()
	deadline := time.Now().Add(time.Minute)
	for {
		req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/v/1.0.0/sdk.js", nil)
		req.Host = gatewayHost
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr, name
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("caddy not serving: %v\n%s", err, logs(t, name))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func waitReady(t testing.TB, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s not ready after %s: %s", url, timeout, last)
}

// Site is a registered test site.
type Site struct {
	ID, Quarantine, Tenant string
	Origin, Host           string
}

// PutSite registers a site with one origin and the given status.
func (s *Stack) PutSite(t testing.TB, id, origin, status string) Site {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	p, err := registry.NewPublisher(context.Background(), s.JS)
	if err != nil {
		t.Fatal(err)
	}
	site := Site{ID: id, Quarantine: uuid.NewString(), Tenant: uuid.NewString(), Origin: origin}
	site.Host = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	err = p.PutSite(context.Background(), registry.Site{
		ID: site.ID, TenantID: site.Tenant, Status: status, Origins: []string{origin},
		QuarantineWebsiteID: site.Quarantine,
	})
	if err != nil {
		t.Fatal(err)
	}
	return site
}
