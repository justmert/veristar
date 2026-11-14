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

// the content security policy app.Caddyfile must send; the console build is
// checked against it in a real browser (apps/console), so a change here is a
// change to what the console may do
const appCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func appCaddyfile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	p := filepath.Join(filepath.Dir(file), "app.Caddyfile")
	_, err := os.Stat(p)
	require.NoError(t, err)
	return p
}

func appEnv() map[string]string {
	return map[string]string{
		"BASE_DOMAIN":           "sa.test",
		"CONTROLPLANE_UPSTREAM": "controlplane:8085",
		"CONSOLE_ROOT":          "/srv/console",
		"APP_TLS":               "internal",
	}
}

// appCaddyRun runs one caddy command on app.Caddyfile to completion and
// returns its exit code and combined output.
func appCaddyRun(t *testing.T, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: appCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
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

func TestAppCaddyfileValidates(t *testing.T) {
	code, out := appCaddyRun(t, appEnv(), "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "Valid configuration")

	// every variable is required: an unset one fails validation
	for _, k := range []string{"BASE_DOMAIN", "CONTROLPLANE_UPSTREAM", "CONSOLE_ROOT", "APP_TLS"} {
		env := appEnv()
		delete(env, k)
		code, out := appCaddyRun(t, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		require.NotEqual(t, 0, code, "validation passed without %s: %s", k, out)
	}

	code, out = appCaddyRun(t, appEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	var adapted string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `{"apps"`) || strings.HasPrefix(line, `{"logging"`) {
			adapted = line
		}
	}
	require.NotEmpty(t, adapted, "no adapted config in output: %s", out)
	for _, want := range []string{
		`"host":["app.sa.test"]`,
		`"dial":"controlplane:8085"`,
		`"root":"/srv/console"`,
		`"Strict-Transport-Security":["max-age=63072000; includeSubDomains"]`,
		`"X-Frame-Options":["DENY"]`,
		`"Cache-Control":["no-store"]`,
	} {
		require.Contains(t, adapted, want)
	}
	require.Contains(t, adapted, appCSP)
	var cfg struct {
		Logging struct {
			Logs map[string]struct {
				Encoder struct {
					Format string `json:"format"`
					Fields map[string]struct {
						Filter string `json:"filter"`
					} `json:"fields"`
				} `json:"encoder"`
			} `json:"logs"`
		} `json:"logging"`
	}
	require.NoError(t, json.Unmarshal([]byte(adapted), &cfg))
	// the site log and caddy's default logger, which writes the access
	// lines of requests for a host the file does not serve
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
		require.Equal(t, "regexp", l.Encoder.Fields["request>uri"].Filter, "%s must cut the query from the uri", name)
	}
	require.Equal(t, 2, filtered, "both loggers are filtered")
}

// controlPlaneDouble stands in for the control plane behind caddy. it records
// request headers and answers like the sso route: a redirect carrying a
// token in Location and a session cookie, both of which must stay out of
// the access log.
type controlPlaneDouble struct {
	mu      sync.Mutex
	headers []http.Header
	paths   []string
}

const ssoToken = "umami-token-must-not-be-logged"

func (c *controlPlaneDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.headers = append(c.headers, r.Header.Clone())
	c.paths = append(c.paths, r.Method+" "+r.URL.Path)
	c.mu.Unlock()
	w.Header().Set("Set-Cookie", "sa_session=cookie-must-not-be-logged; Path=/; HttpOnly; Secure")
	w.Header().Set("Location", "https://web.sa.test/sso?token="+ssoToken+"&url=/websites/x")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusFound)
}

func (c *controlPlaneDouble) lastPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.paths) == 0 {
		return ""
	}
	return c.paths[len(c.paths)-1]
}

func (c *controlPlaneDouble) last() http.Header {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.headers) == 0 {
		return nil
	}
	return c.headers[len(c.headers)-1]
}

func TestAppCaddyServesConsoleAndApi(t *testing.T) {
	ctx := context.Background()

	// a console build with the shape vite produces: the shell and hashed assets
	dist := t.TempDir()
	const shell = `<!doctype html><html><head><script type="module" src="/assets/index-abc.js"></script></head><body><div id="root"></div></body></html>`
	require.NoError(t, os.WriteFile(filepath.Join(dist, "index.html"), []byte(shell), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dist, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "assets", "index-abc.js"), []byte("console.log(1)"), 0o644))

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	cp := &controlPlaneDouble{}
	srv := &http.Server{Handler: cp, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	env := appEnv()
	env["CONTROLPLANE_UPSTREAM"] = "host.docker.internal:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	c, err := testcontainers.Run(ctx, caddyImage,
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{HostFilePath: appCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: filepath.Join(dist, "index.html"), ContainerFilePath: "/srv/console/index.html", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: filepath.Join(dist, "assets", "index-abc.js"), ContainerFilePath: "/srv/console/assets/index-abc.js", FileMode: 0o644},
		),
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

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			// caddy's local ca signs the test certificate; the test checks
			// routing, headers and logs, not the chain
			TLSClientConfig: &tls.Config{ServerName: "app.sa.test", InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	const ua = "Mozilla/5.0 (X11; Linux x86_64) AppLogProbe/3.2"
	type answer struct {
		StatusCode int
		Header     http.Header
	}
	get := func(path string) (answer, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+path, nil)
		require.NoError(t, err)
		req.Host = "app.sa.test"
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Cookie", "sa_session=request-cookie-must-not-be-logged")
		req.Header.Set("X-Forwarded-For", "198.51.100.77")
		// a client cannot choose the address the sign-in limits key on
		req.Header.Set("X-Sa-Client-Addr", "198.51.100.88")
		resp, err := client.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return answer{resp.StatusCode, resp.Header}, string(b)
	}
	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+"/", nil)
		req.Host = "app.sa.test"
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}, 30*time.Second, 250*time.Millisecond, "caddy did not answer over tls")

	securityHeaders := func(resp answer, referrer ...string) {
		t.Helper()
		want := "same-origin"
		if len(referrer) > 0 {
			want = referrer[0]
		}
		require.Equal(t, appCSP, resp.Header.Get("Content-Security-Policy"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		require.Equal(t, want, resp.Header.Get("Referrer-Policy"))
		require.NotEmpty(t, resp.Header.Get("Strict-Transport-Security"))
		require.Empty(t, resp.Header.Get("Server"))
	}

	// a client-side route is answered with the shell, never cached stale
	resp, body := get("/projects/0193a6f3-0101-7a22-b3c4-d5e6f7a8b9c0/sites")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, shell, body)
	require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	securityHeaders(resp)

	// hashed assets are immutable; a missing asset is a 404, not the shell
	resp, body = get("/assets/index-abc.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "console.log(1)", body)
	require.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
	resp, body = get("/assets/missing.js")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NotContains(t, body, "<html")
	securityHeaders(resp)

	// /api goes to the control plane unchanged, with no client address
	resp, _ = get("/api/sso/umami?site=probe-site")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), ssoToken)
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	// the control plane's no-referrer on the token redirect is kept
	securityHeaders(resp, "no-referrer")
	h := cp.last()
	require.NotNil(t, h, "the request did not reach the control plane")
	require.Empty(t, h.Values("X-Forwarded-For"))
	require.Empty(t, h.Values("X-Real-Ip"))
	require.Empty(t, h.Values("Forwarded"))
	// the sign-in limits key on the connection's address, set by caddy;
	// the value the client sent is replaced (contract 4.1.1)
	require.Len(t, h.Values("X-Sa-Client-Addr"), 1)
	require.NotEqual(t, "198.51.100.88", h.Get("X-Sa-Client-Addr"))
	require.NotEqual(t, "198.51.100.77", h.Get("X-Sa-Client-Addr"))
	_, err = netip.ParseAddr(h.Get("X-Sa-Client-Addr"))
	require.NoError(t, err, "caddy sends a bare address")
	require.Equal(t, "sa_session=request-cookie-must-not-be-logged", h.Get("Cookie"), "the session cookie must reach the control plane")

	// the mcp endpoint, the oauth paths and the discovery documents go to
	// the control plane (contract 12); the consent page is a console route
	for _, p := range []string{"/mcp", "/oauth/authorize", "/oauth/token", "/oauth/register", "/oauth/revoke",
		"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server"} {
		method := http.MethodGet
		if p == "/mcp" || p == "/oauth/token" || p == "/oauth/register" || p == "/oauth/revoke" {
			method = http.MethodPost
		}
		req, err := http.NewRequestWithContext(ctx, method, "https://"+endpoint+p, nil)
		require.NoError(t, err)
		req.Host = "app.sa.test"
		req.Header.Set("Authorization", "Bearer sa_mcp_token-must-not-be-logged")
		res, err := client.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, method+" "+p, cp.lastPath(), "%s did not reach the control plane", p)
		require.Equal(t, "Bearer sa_mcp_token-must-not-be-logged", cp.last().Get("Authorization"))
		require.Empty(t, cp.last().Values("X-Sa-Client-Addr"))
	}
	resp, body = get("/ai/consent?request=x")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, shell, body)
	_, _ = get("/.well-known/security.txt")
	require.NotEqual(t, "GET /.well-known/security.txt", cp.lastPath(), "only the oauth documents leave the console")

	// caddy's own error, the control plane gone, still carries the headers
	require.NoError(t, srv.Close())
	resp, _ = get("/api/me")
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	securityHeaders(resp)

	// the access log keeps none of the identifying or secret values
	var all string
	require.Eventually(t, func() bool {
		logs, err := c.Logs(ctx)
		require.NoError(t, err)
		b, err := io.ReadAll(logs)
		require.NoError(t, err)
		all = string(b)
		return strings.Count(all, `"logger":"http.log.access`) >= 15
	}, 10*time.Second, 100*time.Millisecond, "no access log lines: %s", all)
	for _, secret := range []string{
		ua, "AppLogProbe", "198.51.100.77", "198.51.100.88", "request-cookie-must-not-be-logged",
		"cookie-must-not-be-logged", ssoToken, "probe-site", "token-must-not-be-logged",
	} {
		require.NotContains(t, all, secret)
	}
}
