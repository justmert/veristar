package caddy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// the otlp site (this stage) proxies to the two collector listeners, health
// checks the collector, and logs through the filtered site logger
// (TestEdgeCaddyfileValidates checks the filter and that each collector
// variable is required).
func TestEdgeOTLPSiteAdapts(t *testing.T) {
	code, out := caddyRun(t, baseEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	for _, want := range []string{
		`"host":["otlp.edge.test"]`,
		`"dial":"collector:4317"`,
		`"dial":"collector:4318"`,
		`"upstream":"collector:13133"`,
		`"uri":"/"`,
		`"versions":["h2c"]`,
		`"path":["/v1/metrics","/v1/traces","/v1/logs"]`,
		`"max_size":4000000`,
	} {
		require.Contains(t, out, want)
	}
	require.Contains(t, out, `"otlp.edge.test":["log`, "otlp has its own filtered site log")
}

// otlpUpstream stands in for the collector: a real otlp grpc metrics
// service and an http handler, both recording what caddy forwarded.
type otlpUpstream struct {
	pmetricotlp.UnimplementedGRPCServer
	mu      sync.Mutex
	grpcMD  []metadata.MD
	httpHdr []http.Header
	httpLen []int
}

func (u *otlpUpstream) Export(ctx context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	u.mu.Lock()
	u.grpcMD = append(u.grpcMD, md.Copy())
	u.mu.Unlock()
	return pmetricotlp.NewExportResponse(), nil
}

func (u *otlpUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.httpHdr = append(u.httpHdr, r.Header.Clone())
	u.httpLen = append(u.httpLen, len(b))
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func listenAll(t *testing.T) (net.Listener, int) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func TestEdgeOTLPProxiesGRPCAndHTTP(t *testing.T) {
	ctx := context.Background()
	up := &otlpUpstream{}
	gln, gport := listenAll(t)
	gs := grpc.NewServer()
	pmetricotlp.RegisterGRPCServer(gs, up)
	go func() { _ = gs.Serve(gln) }()
	t.Cleanup(gs.Stop)
	hln, hport := listenAll(t)
	hs := &http.Server{Handler: up, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(hln) }()
	t.Cleanup(func() { _ = hs.Close() })
	// the collector's health_check endpoint: 200 when ready, 503 before
	var ready atomic.Bool
	ready.Store(true)
	cln, cport := listenAll(t)
	cs := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	go func() { _ = cs.Serve(cln) }()
	t.Cleanup(func() { _ = cs.Close() })

	env := baseEnv()
	env["COLLECTOR_GRPC_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(gport)
	env["COLLECTOR_HTTP_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(hport)
	env["COLLECTOR_HEALTH_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(cport)
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: caddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
		}),
		testcontainers.WithExposedPorts("443/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
		}),
		testcontainers.WithWaitStrategy(wait.ForLog("serving initial configuration").WithStartupTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, c)
	require.NoError(t, err)
	endpoint, err := c.PortEndpoint(ctx, "443/tcp", "")
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			if r, err := c.Logs(context.Background()); err == nil {
				b, _ := io.ReadAll(r)
				t.Logf("caddy log:\n%s", b)
			}
		}
	})

	const ua = "OTLPEdgeProbe/3.2"
	tlsCfg := &tls.Config{ServerName: "otlp.edge.test", InsecureSkipVerify: true}

	// grpc over tls to caddy, h2c from caddy to the upstream
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithAuthority("otlp.edge.test"), grpc.WithUserAgent(ua))
	require.NoError(t, err)
	defer conn.Close()
	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetName("http.server.request.duration")
	octx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer sa_otlp_probe", "x-forwarded-for", "198.51.100.7")
	require.Eventually(t, func() bool {
		cctx, cancel := context.WithTimeout(octx, 5*time.Second)
		defer cancel()
		_, err := pmetricotlp.NewGRPCClient(conn).Export(cctx, pmetricotlp.NewExportRequestFromMetrics(md))
		return err == nil
	}, 30*time.Second, 250*time.Millisecond, "grpc export through caddy failed")
	up.mu.Lock()
	gmd := up.grpcMD[len(up.grpcMD)-1]
	up.mu.Unlock()
	require.Equal(t, []string{"Bearer sa_otlp_probe"}, gmd.Get("authorization"))
	require.Empty(t, gmd.Get("x-forwarded-for"))

	// otlp http
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	body, err := pmetricotlp.NewExportRequestFromMetrics(md).MarshalProto()
	require.NoError(t, err)
	do := func(method, path string, b []byte) int {
		req, err := http.NewRequestWithContext(ctx, method, "https://"+endpoint+path, bytes.NewReader(b))
		require.NoError(t, err)
		req.Host = "otlp.edge.test"
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Authorization", "Bearer sa_otlp_probe")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for _, p := range []string{"/v1/metrics", "/v1/traces", "/v1/logs"} {
		require.Equal(t, http.StatusOK, do(http.MethodPost, p, body), p)
	}
	up.mu.Lock()
	h := up.httpHdr[len(up.httpHdr)-1]
	up.mu.Unlock()
	require.Equal(t, "Bearer sa_otlp_probe", h.Get("Authorization"))
	require.Empty(t, h.Values("X-Forwarded-For"))
	require.Empty(t, h.Values("Forwarded"))

	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/v1/metrics", nil))
	require.Equal(t, http.StatusNotFound, do(http.MethodPost, "/v1/other", body))
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusRequestEntityTooLarge, do(http.MethodPost, "/v1/metrics", make([]byte, 5<<20)))

	// a collector that is not ready (snapshot not loaded) receives nothing:
	// caddy answers 503, which sdks retry, and forwards again once ready
	up.mu.Lock()
	before := len(up.httpHdr)
	up.mu.Unlock()
	ready.Store(false)
	require.Eventually(t, func() bool { return do(http.MethodPost, "/v1/metrics", body) == http.StatusServiceUnavailable },
		10*time.Second, 200*time.Millisecond, "caddy kept forwarding to an unready collector")
	up.mu.Lock()
	forwarded := len(up.httpHdr) - before
	up.mu.Unlock()
	ready.Store(true)
	require.Eventually(t, func() bool { return do(http.MethodPost, "/v1/metrics", body) == http.StatusOK },
		10*time.Second, 200*time.Millisecond, "caddy did not resume after the collector became ready")
	t.Logf("requests forwarded while the health check caught up: %d", forwarded)

	// otlp access lines exist and carry no client identity
	var all string
	var lines []string
	require.Eventually(t, func() bool {
		logs, err := c.Logs(ctx)
		require.NoError(t, err)
		b, err := io.ReadAll(logs)
		require.NoError(t, err)
		all = string(b)
		lines = lines[:0]
		for _, line := range strings.Split(all, "\n") {
			if strings.Contains(line, `"logger":"http.log.access`) && strings.Contains(line, "otlp.edge.test") {
				lines = append(lines, line)
			}
		}
		return len(lines) >= 5
	}, 10*time.Second, 100*time.Millisecond)
	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		req, ok := entry["request"].(map[string]any)
		require.True(t, ok, line)
		require.NotContains(t, req, "remote_ip", line)
		require.NotContains(t, req, "client_ip", line)
		// no request header at all, so neither the user agent nor the
		// bearer token
		require.NotContains(t, req, "headers", line)
	}
	require.NotContains(t, all, ua)
	require.NotContains(t, all, "198.51.100.7")
	require.NotContains(t, all, "sa_otlp_probe")
}
