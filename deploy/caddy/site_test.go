package caddy_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// the content security policy of the website at <BASE_DOMAIN>
// (app.Caddyfile, site_security_headers): its own files plus the platform's
// tracker from the gateway in.<BASE_DOMAIN>
const siteCSP = "default-src 'self'; script-src 'self' https://in.sa.test; connect-src 'self' https://in.sa.test; style-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// the website's routes proxy nothing: the adapted config of its host holds
// file servers, headers and redirects only
func TestSiteRoutesProxyNothing(t *testing.T) {
	code, out := appCaddyRun(t, appEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	var adapted string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `{"apps"`) || strings.HasPrefix(line, `{"logging"`) {
			adapted = line
		}
	}
	require.NotEmpty(t, adapted, "no adapted config in output: %s", out)
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Match []struct {
							Host []string `json:"host"`
						} `json:"match"`
						Handle json.RawMessage `json:"handle"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	require.NoError(t, json.Unmarshal([]byte(adapted), &cfg))
	var found int
	for _, srv := range cfg.Apps.HTTP.Servers {
		for _, r := range srv.Routes {
			for _, m := range r.Match {
				if strings.Join(m.Host, ",") != "sa.test,www.sa.test" {
					continue
				}
				found++
				h := string(r.Handle)
				require.NotContains(t, h, "reverse_proxy", "the website must proxy nothing")
				require.NotContains(t, h, "controlplane")
				require.Contains(t, h, `"root":"/srv/site"`)
				require.Contains(t, h, siteCSP)
			}
		}
	}
	require.Equal(t, 1, found, "one route serves the website's two names")
}

func TestSiteServesStaticPages(t *testing.T) {
	ctx := context.Background()

	// a website build with the shape vite produces: two pages, an unhashed
	// file from public/ and hashed assets
	dist := t.TempDir()
	const index = `<!doctype html><html><head><script type="module" src="/assets/index-abc.js"></script></head><body><div id="root"></div></body></html>`
	const notFound = `<!doctype html><html><head><title>Page not found</title></head><body></body></html>`
	files := map[string]string{
		"index.html":          index,
		"404.html":            notFound,
		"favicon.svg":         `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"assets/index-abc.js": "console.log(1)",
	}
	var cfiles []testcontainers.ContainerFile
	for name, body := range files {
		p := filepath.Join(dist, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		cfiles = append(cfiles, testcontainers.ContainerFile{HostFilePath: p, ContainerFilePath: "/srv/site/" + name, FileMode: 0o644})
	}
	cfiles = append(cfiles, testcontainers.ContainerFile{HostFilePath: appCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644})

	// the control plane double counts every request that reaches it; the
	// website's names must send it none, whatever the path
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
		testcontainers.WithFiles(cfiles...),
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

	const ua = "Mozilla/5.0 (X11; Linux x86_64) SiteLogProbe/1.0"
	// the status and headers of an answer whose body is read and closed
	type answer struct {
		StatusCode int
		Header     http.Header
	}
	get := func(host, path string) (answer, string) {
		t.Helper()
		client := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				// caddy's local ca signs the test certificate; the test checks
				// routing and headers, not the chain
				TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+path, nil)
		require.NoError(t, err)
		req.Host = host
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-Forwarded-For", "198.51.100.77")
		resp, err := client.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return answer{resp.StatusCode, resp.Header}, string(b)
	}
	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ServerName: "sa.test", InsecureSkipVerify: true},
		}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+"/", nil)
		req.Host = "sa.test"
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}, 30*time.Second, 250*time.Millisecond, "caddy did not answer over tls")

	securityHeaders := func(resp answer) {
		t.Helper()
		require.Equal(t, siteCSP, resp.Header.Get("Content-Security-Policy"))
		require.Equal(t, "max-age=63072000; includeSubDomains", resp.Header.Get("Strict-Transport-Security"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		require.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
		require.Empty(t, resp.Header.Get("Server"))
		require.Empty(t, resp.Header.Get("Set-Cookie"))
	}

	// the home page, cached briefly
	resp, body := get("sa.test", "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, index, body)
	require.Equal(t, "public, max-age=300", resp.Header.Get("Cache-Control"))
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html"), resp.Header.Get("Content-Type"))
	securityHeaders(resp)

	// an unhashed file from public/ is cached briefly as well
	resp, _ = get("sa.test", "/favicon.svg")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "public, max-age=300", resp.Header.Get("Cache-Control"))
	securityHeaders(resp)

	// hashed assets are immutable
	resp, body = get("sa.test", "/assets/index-abc.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "console.log(1)", body)
	require.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
	securityHeaders(resp)

	// www moves to the bare domain with path and query kept
	resp, _ = get("www.sa.test", "/about?ref=x")
	require.Equal(t, http.StatusPermanentRedirect, resp.StatusCode)
	require.Equal(t, "https://sa.test/about?ref=x", resp.Header.Get("Location"))
	require.NotEmpty(t, resp.Header.Get("Strict-Transport-Security"))

	// an unknown path, a missing asset and the console's and the control
	// plane's paths are the website's 404 page with status 404
	for _, p := range []string{"/no-such-page", "/assets/missing.js", "/api/me", "/mcp", "/oauth/authorize",
		"/.well-known/stellar.toml", "/sign-in", "/projects"} {
		resp, body = get("sa.test", p)
		require.Equal(t, http.StatusNotFound, resp.StatusCode, p)
		require.Equal(t, notFound, body, p)
		securityHeaders(resp)
	}
	require.Empty(t, cp.lastPath(), "a website request reached the control plane")

	// the console keeps its own site and policy beside the website
	resp, _ = get("app.sa.test", "/api/me")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, appCSP, resp.Header.Get("Content-Security-Policy"))

	// the access log keeps no client identifier
	var all string
	require.Eventually(t, func() bool {
		logs, err := c.Logs(ctx)
		require.NoError(t, err)
		b, err := io.ReadAll(logs)
		require.NoError(t, err)
		all = string(b)
		return strings.Count(all, `"logger":"http.log.access`) >= 12
	}, 10*time.Second, 100*time.Millisecond, "no access log lines: %s", all)
	for _, secret := range []string{ua, "SiteLogProbe", "198.51.100.77", "ref=x"} {
		require.NotContains(t, all, secret)
	}
}
