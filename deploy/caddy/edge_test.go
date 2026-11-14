// Package caddy_test checks deploy/caddy/edge.Caddyfile with the pinned
// caddy image: that it validates, and that a request through a real caddy
// leaves no client address or user agent in the access log.
package caddy_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// caddyImage is caddy 2.11.9 by the digest pinned in the design
const caddyImage = "caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1"

func caddyfile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	p := filepath.Join(filepath.Dir(file), "edge.Caddyfile")
	_, err := os.Stat(p)
	require.NoError(t, err)
	return p
}

func baseEnv() map[string]string {
	return map[string]string{
		"BASE_DOMAIN":      "edge.test",
		"GATEWAY_UPSTREAM": "gateway:8080",
		"EDGE_TLS":         "internal",
		// otlp.<BASE_DOMAIN> (this stage)
		"COLLECTOR_GRPC_UPSTREAM":   "collector:4317",
		"COLLECTOR_HTTP_UPSTREAM":   "collector:4318",
		"COLLECTOR_HEALTH_UPSTREAM": "collector:13133",
	}
}

// caddyRun runs one caddy command to completion and returns its exit code
// and combined output.
func caddyRun(t *testing.T, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: caddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
		}),
		testcontainers.WithCmd(cmd...),
		testcontainers.WithWaitStrategy(wait.ForExit().WithExitTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, c)
	require.NoError(t, err)
	state, err := c.State(ctx)
	require.NoError(t, err)
	logs, err := c.Logs(ctx)
	require.NoError(t, err)
	b, err := io.ReadAll(logs)
	require.NoError(t, err)
	return state.ExitCode, string(b)
}

func TestEdgeCaddyfileValidates(t *testing.T) {
	code, out := caddyRun(t, baseEnv(), "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "Valid configuration")

	// every variable is required: an unset one fails validation
	for _, k := range []string{"BASE_DOMAIN", "GATEWAY_UPSTREAM", "EDGE_TLS", "COLLECTOR_GRPC_UPSTREAM", "COLLECTOR_HTTP_UPSTREAM", "COLLECTOR_HEALTH_UPSTREAM"} {
		env := baseEnv()
		delete(env, k)
		code, out := caddyRun(t, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		require.NotEqual(t, 0, code, "validation passed without %s: %s", k, out)
	}

	// the adapted json routes the right host to the gateway and filters the log
	code, out = caddyRun(t, baseEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	var adapted string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `{"apps"`) || strings.HasPrefix(line, `{"logging"`) {
			adapted = line
		}
	}
	require.NotEmpty(t, adapted, "no adapted config in output: %s", out)
	for _, want := range []string{
		`"host":["in.edge.test"]`,
		`"dial":"gateway:8080"`,
		`"X-Edge-Client-Ip":["{http.vars.client_ip}"]`,
		`"delete":["X-Forwarded-For","X-Real-Ip","Forwarded"]`,
	} {
		require.Contains(t, adapted, want)
	}
	var cfg struct {
		Logging struct {
			Logs map[string]struct {
				Encoder struct {
					Format string `json:"format"`
					Fields map[string]struct {
						Filter string `json:"filter"`
					} `json:"fields"`
				} `json:"encoder"`
				Include []string `json:"include"`
			} `json:"logs"`
		} `json:"logging"`
	}
	require.NoError(t, json.Unmarshal([]byte(adapted), &cfg))
	// the in and otlp site logs and caddy's default logger, which writes the
	// access lines of requests for a host the file does not serve
	require.Contains(t, cfg.Logging.Logs, "default")
	var filtered int
	for name, l := range cfg.Logging.Logs {
		filtered++
		require.Equal(t, "filter", l.Encoder.Format, name)
		for _, f := range []string{
			"request>remote_ip", "request>remote_port", "request>client_ip",
			"request>headers", "resp_headers",
		} {
			require.Equal(t, "delete", l.Encoder.Fields[f].Filter, "%s must delete %s", name, f)
		}
	}
	require.Equal(t, 3, filtered, "every logger is filtered")
}

// upstream stands in for the gateway and records the headers caddy sends.
type upstream struct {
	mu      sync.Mutex
	headers []http.Header
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.headers = append(u.headers, r.Header.Clone())
	u.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (u *upstream) last() http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.headers) == 0 {
		return nil
	}
	return u.headers[len(u.headers)-1]
}

func TestEdgeAccessLogHasNoClientIdentity(t *testing.T) {
	ctx := context.Background()
	// the upstream listens on every interface so the container can reach it
	// through the docker host gateway
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	up := &upstream{}
	srv := &http.Server{Handler: up, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	env := baseEnv()
	env["GATEWAY_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(port)
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

	const ua = "Mozilla/5.0 (X11; Linux x86_64) EdgeLogProbe/7.1"
	const spoofed = "198.51.100.77"
	// W-7: a page with Referrer-Policy: unsafe-url sends its full url
	const referer = "https://shop.example/reset?token=EdgeRefererProbe&email=maria%40example.com"
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		// caddy's local ca signs the test certificate; the test checks the
		// log, not the chain
		TLSClientConfig: &tls.Config{ServerName: "in.edge.test", InsecureSkipVerify: true},
	}}
	var status int
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+"/v1/c?w=probe", nil)
		require.NoError(t, err)
		req.Host = "in.edge.test"
		req.Header.Set("User-Agent", ua)
		// a client cannot choose the address the gateway sees
		req.Header.Set("X-Edge-Client-Ip", spoofed)
		req.Header.Set("X-Forwarded-For", spoofed)
		req.Header.Set("X-Real-Ip", spoofed)
		req.Header.Set("Referer", referer)
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		status = resp.StatusCode
		return true
	}, 30*time.Second, 250*time.Millisecond, "caddy did not answer over tls")
	require.Equal(t, http.StatusNoContent, status)

	h := up.last()
	require.NotNil(t, h, "the request did not reach the upstream")
	got := h.Values("X-Edge-Client-Ip")
	require.Len(t, got, 1, "exactly one client address header")
	remote, err := netip.ParseAddr(got[0])
	require.NoError(t, err, "the header must hold one address: %q", got[0])
	require.NotEqual(t, spoofed, remote.String(), "the client-sent value must be replaced")
	require.Empty(t, h.Values("X-Forwarded-For"))
	require.Empty(t, h.Values("X-Real-Ip"))
	require.Empty(t, h.Values("Forwarded"))
	require.Equal(t, ua, h.Get("User-Agent"), "the gateway still needs the user agent")
	t.Logf("caddy saw the client as %s", remote)

	// the event route (POST /v1/e) is not access-logged at all (log_skip in
	// edge.Caddyfile): it still reaches the gateway with the same headers
	event, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+endpoint+"/v1/e?probe=EdgeEventProbe",
		strings.NewReader(`{"v":1}`))
	require.NoError(t, err)
	event.Host = "in.edge.test"
	event.Header.Set("User-Agent", ua)
	event.Header.Set("X-Forwarded-For", spoofed)
	resp, err := client.Do(event)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	ev := up.last()
	require.Equal(t, remote.String(), ev.Get("X-Edge-Client-Ip"), "the event route sets the client address like the others")
	require.Empty(t, ev.Values("X-Forwarded-For"))

	// a request for a host this file does not serve goes to caddy's default
	// logger; it must be filtered the same way
	unmapped, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+"/unmapped-probe", nil)
	require.NoError(t, err)
	unmapped.Host = "other.edge.test"
	unmapped.Header.Set("User-Agent", ua)
	unmapped.Header.Set("X-Forwarded-For", spoofed)
	unmapped.Header.Set("Referer", referer)
	resp, err = client.Do(unmapped)
	require.NoError(t, err)
	_ = resp.Body.Close()

	// the access log line is written after the response; wait for it
	var all, access, other string
	require.Eventually(t, func() bool {
		logs, err := c.Logs(ctx)
		require.NoError(t, err)
		b, err := io.ReadAll(logs)
		require.NoError(t, err)
		all = string(b)
		for _, line := range strings.Split(all, "\n") {
			if strings.Contains(line, `"logger":"http.log.access`) && strings.Contains(line, "/v1/c?w=probe") {
				access = line
			}
			if strings.Contains(line, `"logger":"http.log.access`) && strings.Contains(line, "/unmapped-probe") {
				other = line
			}
		}
		return access != "" && other != ""
	}, 10*time.Second, 100*time.Millisecond, "no access log line: %s", all)
	require.NotContains(t, all, "EdgeEventProbe", "the event route must not be access-logged")
	for _, line := range []string{access, other} {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		req, ok := entry["request"].(map[string]any)
		require.True(t, ok, line)
		require.NotContains(t, req, "remote_ip", line)
		require.NotContains(t, req, "remote_port", line)
		require.NotContains(t, req, "client_ip", line)
		// no request header at all: user agent, forwarding headers, Referer
		require.NotContains(t, req, "headers", line)
		require.NotContains(t, entry, "resp_headers", line)
	}

	// and nowhere in anything caddy wrote: neither the address nor the agent
	require.NotContains(t, all, remote.String())
	require.NotContains(t, all, ua)
	require.NotContains(t, all, "EdgeLogProbe")
	require.NotContains(t, all, "EdgeRefererProbe")
	require.NotContains(t, all, "maria")
}
