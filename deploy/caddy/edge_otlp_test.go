package caddy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
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

// the otlp site (this stage) adapts to two upstreams read at request time and
// adds no logger of its own: the existing check of exactly two filtered
// loggers in TestEdgeCaddyfileValidates still holds.
func TestEdgeOTLPSiteAdapts(t *testing.T) {
	code, out := caddyRun(t, baseEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	for _, want := range []string{
		`"host":["otlp.edge.test"]`,
		`"dial":"{env.COLLECTOR_GRPC_UPSTREAM}"`,
		`"dial":"{env.COLLECTOR_HTTP_UPSTREAM}"`,
		`"versions":["h2c"]`,
		`"path":["/v1/metrics","/v1/traces","/v1/logs"]`,
		`"max_size":4000000`,
	} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, `"otlp.edge.test":["log`, "otlp must use the filtered default logger")
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

	env := baseEnv()
	env["COLLECTOR_GRPC_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(gport)
	env["COLLECTOR_HTTP_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(hport)
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

	// nothing caddy wrote carries the client's agent or forwarded address
	logs, err := c.Logs(ctx)
	require.NoError(t, err)
	b, err := io.ReadAll(logs)
	require.NoError(t, err)
	all := string(b)
	require.NotContains(t, all, ua)
	require.NotContains(t, all, "198.51.100.7")
}
