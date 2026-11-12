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

	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
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
