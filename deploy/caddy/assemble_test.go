package caddy_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// assemble runs deploy/caddy/assemble.sh and writes the node caddyfile to a
// temporary file.
func assemble(t *testing.T, args ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	full := []string{filepath.Join(dir, "assemble.sh"), args[0]}
	for _, a := range args[1:] {
		full = append(full, filepath.Join(dir, a))
	}
	out, err := exec.Command("sh", full...).Output()
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "node.Caddyfile")
	require.NoError(t, os.WriteFile(p, out, 0o644))
	return p
}

func nodeCaddyRun(t *testing.T, path string, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	// the custom build: core needs replace-response, and the bunny variant
	// needs caddy-dns/bunny
	c, err := testcontainers.Run(ctx, umamictr.WebCaddyImage(t),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{HostFilePath: path, ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644}),
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

// the assembled node configs validate with dns-01 through bunny and with the
// local ca, and an unset or unknown SA_TLS fails instead of serving without
// the intended issuer.
func TestAssembledNodeCaddyfiles(t *testing.T) {
	health := map[string]string{"NODE_HEALTH_UPSTREAM": "health:9090", "NODE_HEALTH_PATH": "/edgez"}
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"edge", []string{"edge", "edge.Caddyfile"}, baseEnv()},
		{"core", []string{"core", "app.Caddyfile", "web.Caddyfile"}, map[string]string{
			"BASE_DOMAIN": "sa.test", "CONTROLPLANE_UPSTREAM": "controlplane:8085",
			"CONSOLE_ROOT": "/srv/console", "UMAMI_UPSTREAM": "umami:3000",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := assemble(t, tc.args...)
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			for k, v := range health {
				env[k] = v
			}
			for _, good := range []string{"bunny", "internal"} {
				env["SA_TLS"] = good
				code, out := nodeCaddyRun(t, p, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
				require.Equal(t, 0, code, "SA_TLS=%s: %s", good, out)
			}

			for _, bad := range []string{"", "acme"} {
				env["SA_TLS"] = bad
				code, out := nodeCaddyRun(t, p, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
				require.NotEqual(t, 0, code, "SA_TLS=%q must fail: %s", bad, out)
			}
		})
	}
}

// a caddyfile whose tls line the script does not recognise is refused
func TestAssembleRefusesUnknownInput(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	err := exec.Command("sh", filepath.Join(dir, "assemble.sh"), "core",
		filepath.Join(dir, "web.Caddyfile"), filepath.Join(dir, "web.Caddyfile")).Run()
	require.Error(t, err)
	err = exec.Command("sh", filepath.Join(dir, "assemble.sh"), "edge", filepath.Join(dir, "app.Caddyfile")).Run()
	require.Error(t, err)
}

// both assembled node configs serve caddy's metrics, with the http request
// counters of the global metrics option, on port 2021 for the observability
// relay, while the admin api stays on the container's loopback.
func TestAssembledNodeServesMetrics(t *testing.T) {
	health := map[string]string{"NODE_HEALTH_UPSTREAM": "health:9090", "NODE_HEALTH_PATH": "/edgez", "SA_TLS": "internal"}
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"edge", []string{"edge", "edge.Caddyfile"}, baseEnv()},
		{"core", []string{"core", "app.Caddyfile", "web.Caddyfile"}, map[string]string{
			"BASE_DOMAIN": "sa.test", "CONTROLPLANE_UPSTREAM": "controlplane:8085",
			"CONSOLE_ROOT": "/srv/console", "UMAMI_UPSTREAM": "umami:3000",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := assemble(t, tc.args...)
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			for k, v := range health {
				env[k] = v
			}
			ctx := context.Background()
			c, err := testcontainers.Run(ctx, umamictr.WebCaddyImage(t),
				testcontainers.WithEnv(env),
				testcontainers.WithFiles(testcontainers.ContainerFile{HostFilePath: p, ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644}),
				testcontainers.WithCmd("caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"),
				testcontainers.WithExposedPorts("2021/tcp", "2019/tcp", "80/tcp"),
				testcontainers.WithWaitStrategy(wait.ForHTTP("/metrics").WithPort("2021/tcp").WithStartupTimeout(time.Minute)),
			)
			testcontainers.CleanupContainer(t, c)
			require.NoError(t, err)
			host, err := c.Host(ctx)
			require.NoError(t, err)
			get := func(port, path string) (int, string, error) {
				mp, err := c.MappedPort(ctx, port)
				require.NoError(t, err)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, mp.Port())+path, nil)
				require.NoError(t, err)
				resp, err := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				}}).Do(req)
				if err != nil {
					return 0, "", err
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				return resp.StatusCode, string(b), nil
			}
			// one request through the node's port-80 site, so a request
			// counter exists (the health upstream does not resolve: 503)
			code, _, err := get("80/tcp", "/")
			require.NoError(t, err)
			require.Equal(t, http.StatusServiceUnavailable, code)
			code, body, err := get("2021/tcp", "/metrics")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, code)
			require.Contains(t, body, "caddy_config_last_reload_successful 1")
			require.Contains(t, body, "caddy_http_requests_total{")
			// the admin api listens on loopback only, so the published port
			// leads nowhere
			_, _, err = get("2019/tcp", "/config/")
			require.Error(t, err)
		})
	}
}
