package caddy_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func redirectCaddyfile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	p := filepath.Join(filepath.Dir(file), "redirect.Caddyfile")
	_, err := os.Stat(p)
	require.NoError(t, err)
	return p
}

func redirectEnv() map[string]string {
	return map[string]string{
		"OLD_DOMAIN": "old.test", "NEW_DOMAIN": "new.test",
		"GATEWAY_UPSTREAM": "gateway:8080", "EDGE_TLS": "internal",
	}
}

func redirectRun(t *testing.T, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: redirectCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
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

func TestRedirectCaddyfileValidates(t *testing.T) {
	code, out := redirectRun(t, redirectEnv(), "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "Valid configuration")
	for _, k := range []string{"OLD_DOMAIN", "NEW_DOMAIN", "GATEWAY_UPSTREAM", "EDGE_TLS"} {
		env := redirectEnv()
		delete(env, k)
		code, out := redirectRun(t, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		require.NotEqual(t, 0, code, "validation passed without %s: %s", k, out)
	}
}

// the old names answer 308 to the new ones with path and query, and the
// tracker files still come from the local gateway
func TestRedirectCaddyLive(t *testing.T) {
	ctx := context.Background()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "gateway:"+r.URL.Path)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	env := redirectEnv()
	env["GATEWAY_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: redirectCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
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
	type answer struct {
		status         int
		location, cors string
		body           string
	}
	get := func(host, path string) answer {
		client := &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true}}, // caddy's local ca in a test container
		}
		var a answer
		require.Eventually(t, func() bool {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+path, nil)
			require.NoError(t, err)
			req.Host = host
			resp, err := client.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			a = answer{resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("Access-Control-Allow-Origin"), string(b)}
			return true
		}, 30*time.Second, 250*time.Millisecond)
		return a
	}
	for _, tc := range []struct{ host, path, want string }{
		{"in.old.test", "/v1/c?w=abc", "https://in.new.test/v1/c?w=abc"},
		{"otlp.old.test", "/v1/metrics", "https://otlp.new.test/v1/metrics"},
		{"app.old.test", "/projects?x=1", "https://app.new.test/projects?x=1"},
	} {
		a := get(tc.host, tc.path)
		require.Equal(t, http.StatusPermanentRedirect, a.status, tc.host)
		require.Equal(t, tc.want, a.location)
		require.Equal(t, "*", a.cors)
	}
	a := get("in.old.test", "/v/1.1.0/sdk.js")
	require.Equal(t, http.StatusOK, a.status)
	require.Equal(t, "gateway:/v/1.1.0/sdk.js", a.body)
}
