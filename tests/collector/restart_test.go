//go:build collectorit

package collector_test

import (
	"context"
	"crypto/tls"
	"maps"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// caddyImage is caddy 2.11.9 by its pinned digest, the
// same image deploy/caddy's tests run edge.Caddyfile with.
const caddyImage = "caddy:2.11.9@sha256:4851e54a46b206517c574821bb5cc67f170d2156041e9fbc4c9193b45e84a75b"

// startEdge runs deploy/caddy/edge.Caddyfile in front of the collector and
// returns the host:port of its https listener.
func (s *stack) startEdge(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	host := "host.docker.internal:"
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(map[string]string{
			"BASE_DOMAIN":               "edge.test",
			"EDGE_TLS":                  "internal",
			"GATEWAY_UPSTREAM":          "gateway.invalid:8080",
			"COLLECTOR_GRPC_UPSTREAM":   host + port(s.grpcAddr),
			"COLLECTOR_HTTP_UPSTREAM":   host + port(s.httpAddr),
			"COLLECTOR_HEALTH_UPSTREAM": host + port(s.health),
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: filepath.Join(repoRoot(t), "deploy", "caddy", "edge.Caddyfile"), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
		}),
		testcontainers.WithExposedPorts("443/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
		}),
		testcontainers.WithWaitStrategy(wait.ForLog("serving initial configuration").WithStartupTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := c.PortEndpoint(ctx, "443/tcp", "")
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

// statusCounter records every status code the sdk received.
type statusCounter struct {
	next http.RoundTripper
	mu   sync.Mutex
	seen map[int]int
}

func (c *statusCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(r)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.seen[0]++
		return nil, err
	}
	c.seen[resp.StatusCode]++
	return resp, nil
}

// the collector is restarted while an sdk exports through the edge caddy.
// during the restart caddy answers 502 or 503 (collector down, then not yet
// ready), never the collector's 401, so the sdk retries and every recorded
// request arrives.
func testRestartUnderLoad(t *testing.T, s *stack) {
	tn := s.newTenant(t)
	edge := s.startEdge(t)

	counter := &statusCounter{seen: map[int]int{}}
	var d net.Dialer
	client := &http.Client{Transport: counter}
	counter.next = &http.Transport{
		// every connection goes to caddy; the url keeps otlp.edge.test as
		// host and tls server name, as a tenant's would. caddy's local test
		// ca signs the certificate, so the chain is not verified.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, edge)
		},
		TLSClientConfig:   &tls.Config{ServerName: "otlp.edge.test", InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}
	exp, err := otlpmetrichttp.New(context.Background(),
		otlpmetrichttp.WithEndpointURL("https://otlp.edge.test/v1/metrics"),
		otlpmetrichttp.WithHTTPClient(client),
		otlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer " + tn.Token}),
		otlpmetrichttp.WithTemporalitySelector(deltaSelector),
		otlpmetrichttp.WithRetry(retry()))
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(appResource("api-restart")),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(200*time.Millisecond))))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	h, _ := mp.Meter("restart").Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(canonical...))

	var recorded atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				h.Record(context.Background(), 0.02, metric.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/restart")))
				recorded.Add(1)
			}
		}
	}()

	time.Sleep(2 * time.Second)
	s.stopCollector(t)
	time.Sleep(time.Second) // exports keep failing while it is down
	s.startCollector(t)
	time.Sleep(3 * time.Second)
	close(stop)
	<-done
	flush(t, mp)

	want := uint64(recorded.Load())
	ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 60*time.Second, func(p []point) bool { return totalCount(p) >= want })
	// a copy, not the map: the periodic reader keeps exporting through the
	// counter while the counts below are read
	counter.mu.Lock()
	seen := maps.Clone(counter.seen)
	counter.mu.Unlock()
	t.Logf("recorded %d, queued %d, responses %v", want, totalCount(ps), seen)
	if totalCount(ps) != want {
		t.Fatalf("queued %d of %d recorded requests", totalCount(ps), want)
	}
	if seen[http.StatusUnauthorized] != 0 {
		t.Fatalf("the sdk got %d responses 401", seen[http.StatusUnauthorized])
	}
	if seen[http.StatusBadGateway]+seen[http.StatusServiceUnavailable] == 0 {
		t.Fatal("no 502 or 503 seen: the restart window was not exercised")
	}
}
