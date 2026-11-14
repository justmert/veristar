package caddy_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

func webCaddyfile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	p := filepath.Join(filepath.Dir(file), "web.Caddyfile")
	_, err := os.Stat(p)
	require.NoError(t, err)
	return p
}

func themeDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(webCaddyfile(t)), "umami-theme", "site")
}

// themePaths reads the path list of the @theme matcher in web.Caddyfile.
func themePaths(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(webCaddyfile(t))
	require.NoError(t, err)
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "@theme path "); ok {
			return strings.Fields(rest)
		}
	}
	t.Fatal("web.Caddyfile has no @theme path matcher")
	return nil
}

func webEnv() map[string]string {
	return map[string]string{
		"BASE_DOMAIN":    "web.test",
		"UMAMI_UPSTREAM": "umami:3000",
		"WEB_TLS":        "internal",
	}
}

// webCaddyRun runs one caddy command on web.Caddyfile to completion and
// returns its exit code and combined output.
func webCaddyRun(t *testing.T, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, umamictr.WebCaddyImage(t),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath: webCaddyfile(t), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644,
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

func TestWebCaddyfileValidates(t *testing.T) {
	// the live tests start caddy through umamictr; both must run caddy built
	// on the pinned image, with the replace-response plugin the db-ip
	// attribution needs
	require.Equal(t, caddyImage, umamictr.CaddyImage)
	dockerfile, err := os.ReadFile(filepath.Join(filepath.Dir(webCaddyfile(t)), "..", "images", "caddy.Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(dockerfile), "\nFROM "+caddyImage+"\n", "the web caddy image must end on the pinned caddy")

	code, out := webCaddyRun(t, webEnv(), "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "Valid configuration")

	// every variable is required: an unset one fails validation
	for _, k := range []string{"BASE_DOMAIN", "UMAMI_UPSTREAM", "WEB_TLS"} {
		env := webEnv()
		delete(env, k)
		code, out := webCaddyRun(t, env, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		require.NotEqual(t, 0, code, "validation passed without %s: %s", k, out)
	}

	code, out = webCaddyRun(t, webEnv(), "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	require.Equal(t, 0, code, out)
	var adapted string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `{"apps"`) || strings.HasPrefix(line, `{"logging"`) {
			adapted = line
		}
	}
	require.NotEmpty(t, adapted, "no adapted config in output: %s", out)
	for _, want := range []string{
		`"host":["web.web.test"]`,
		`"dial":"umami:3000"`,
		`"Location":["https://app.web.test/"]`,
		`"Referrer-Policy":["no-referrer"]`,
		`"Cache-Control":["no-store"]`,
		// db-ip attribution on umami's html, from an uncompressed upstream body
		`"handler":"replace_response"`,
		`"match":{"headers":{"Content-Type":["text/html*"]}}`,
		`"search":"\u003c/body\u003e"`,
		`"replace":"\u003ca href=\"https://db-ip.com\"`,
		`IP Geolocation by DB-IP\u003c/a\u003e\u003c/body\u003e`,
		`"Accept-Encoding":["identity"]`,
		// the umami theme (docs/adr/0004): the stylesheet link before
		// </head>, and the theme files from /srv/umami-theme
		`"search":"\u003c/head\u003e"`,
		`"replace":"\u003clink rel=\"stylesheet\" href=\"/_veristar/theme.css\"\u003e\u003c/head\u003e"`,
		`"root":"/srv/umami-theme"`,
		`"handler":"file_server"`,
		`"Cache-Control":["no-cache"]`,
		`"X-Content-Type-Options":["nosniff"]`,
		`"Content-Security-Policy":["default-src 'none'; frame-ancestors 'none'"]`,
	} {
		require.Contains(t, adapted, want)
	}
	// every path the theme matcher names is a file of the theme, so caddy
	// never answers one of umami's icon paths with a 404
	paths := themePaths(t)
	require.Contains(t, paths, "/_veristar/*")
	for _, p := range paths {
		if p == "/_veristar/*" {
			continue
		}
		require.Contains(t, adapted, `"`+p+`"`)
		_, err := os.Stat(filepath.Join(themeDir(t), filepath.FromSlash(p)))
		require.NoError(t, err, "%s is in the theme matcher but not in the theme", p)
	}
	_, err = os.Stat(filepath.Join(themeDir(t), "_veristar", "theme.css"))
	require.NoError(t, err)
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
