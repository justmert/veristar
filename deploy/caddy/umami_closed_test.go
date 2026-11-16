package caddy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// umami stays on the private network: the control plane, the jobs and the
// replayer reach it there, and no caddy site that faces the internet may
// name it or serve a host for it. these checks fail when a caddyfile starts
// proxying to umami again or a web.<BASE_DOMAIN> site comes back.

// umamiRef is any way a caddyfile could reach umami: its upstream variable,
// its service name on the compose network, or a web.<domain> site
var umamiRef = regexp.MustCompile(`(?i)umami|\bweb\.\{\$`)

// every input caddyfile of deploy/caddy is free of umami, comments included,
// so an upstream cannot hide behind an environment variable either
func TestCaddyfilesNameNoUmami(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(file), "*.Caddyfile"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for i, line := range strings.Split(string(b), "\n") {
			require.False(t, umamiRef.MatchString(line), "%s:%d names umami or a web. site: %s", filepath.Base(f), i+1, line)
		}
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(file), "web.Caddyfile"))
	require.True(t, os.IsNotExist(err), "web.Caddyfile is back")
}

// the assembled configs a node runs adapt to json whose hosts are only the
// public names (in., otlp., app.) and whose upstreams never dial umami
func TestAssembledConfigsReachNoUmami(t *testing.T) {
	health := map[string]string{"NODE_HEALTH_UPSTREAM": "health:9090", "NODE_HEALTH_PATH": "/edgez", "SA_TLS": "internal"}
	for _, tc := range []struct {
		name  string
		args  []string
		env   map[string]string
		hosts []string
	}{
		{"core", []string{"core", "app.Caddyfile"}, coreEnv(), []string{"app.sa.test"}},
		{"node", []string{"node", "edge.Caddyfile", "app.Caddyfile"}, nodeEnv(), []string{"in.sa.test", "otlp.sa.test", "app.sa.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := assemble(t, tc.args...)
			b, err := os.ReadFile(p)
			require.NoError(t, err)
			require.NotRegexp(t, umamiRef, string(b))
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			for k, v := range health {
				env[k] = v
			}
			code, out := nodeCaddyRun(t, p, env, "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
			require.Equal(t, 0, code, out)
			require.NotContains(t, strings.ToLower(out), "umami")
			require.NotContains(t, out, `"web.sa.test"`)
			for _, h := range tc.hosts {
				require.Contains(t, out, `"`+h+`"`, "the adapted config serves %s", h)
			}
		})
	}
}
