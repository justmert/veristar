//go:build ansiblerender

// Package corecaddy checks the caddyfile the core role installs on a core
// or converged node (roles/core tasks/caddyfile.yml): it is exactly what
// deploy/caddy/assemble.sh builds from the files the caddy tests check
// (deploy/caddy), and it serves the public website at the bare domain and
// www beside the console, from static files only. render.yml runs the
// role's task with ansible-playbook from the ansible venv (make
// ansible-venv), so the suite sits behind its own build tag: make
// ansible-render-test.
package corecaddy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// render runs render.yml for one layout and returns the assembled caddyfile
func render(t *testing.T, layout string) string {
	t.Helper()
	root := repoRoot(t)
	venv := os.Getenv("ANSIBLE_VENV")
	if venv == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		venv = filepath.Join(home, ".local", "share", "veristar", "ansible-venv")
	}
	out := t.TempDir()
	cmd := exec.Command(filepath.Join(venv, "bin", "ansible-playbook"),
		"-i", "inventory.yml", "render.yml", "-e", "layout="+layout, "-e", "out="+out)
	cmd.Dir = filepath.Join(root, "tests", "corecaddy")
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(root, "infra", "ansible", "ansible.cfg"))
	text, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", text)
	b, err := os.ReadFile(filepath.Join(out, "Caddyfile"))
	require.NoError(t, err)
	return string(b)
}

// assemble runs deploy/caddy/assemble.sh directly
func assemble(t *testing.T, args ...string) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "deploy", "caddy")
	full := []string{filepath.Join(dir, "assemble.sh"), args[0]}
	for _, a := range args[1:] {
		full = append(full, filepath.Join(dir, a))
	}
	out, err := exec.Command("sh", full...).Output()
	require.NoError(t, err)
	// the role writes the command's stdout, which ansible keeps without its
	// trailing newline, plus one newline: the same bytes
	return string(out)
}

// siteBlock returns the site block of the website from an assembled file
func siteBlock(t *testing.T, caddyfile string) string {
	t.Helper()
	start := strings.Index(caddyfile, "\n{$BASE_DOMAIN}, www.{$BASE_DOMAIN} {\n")
	require.GreaterOrEqual(t, start, 0, "no website site block")
	rest := caddyfile[start+1:]
	end := strings.Index(rest, "\n}\n")
	require.Greater(t, end, 0)
	return rest[:end+2]
}

func TestNodeCaddyfileServesTheWebsite(t *testing.T) {
	for _, tc := range []struct {
		layout string
		args   []string
	}{
		{"node", []string{"node", "edge.Caddyfile", "app.Caddyfile"}},
		{"core", []string{"core", "app.Caddyfile"}},
	} {
		t.Run(tc.layout, func(t *testing.T) {
			got := render(t, tc.layout)
			require.True(t, assemble(t, tc.args...) == got, "the role installs exactly the assembled file")

			site := siteBlock(t, got)
			// the node's certificate issuer, the files of the image and the
			// www redirect
			require.Contains(t, site, "\timport sa_tls_{$SA_TLS}\n")
			require.Contains(t, site, "root {$SITE_ROOT}")
			require.Contains(t, site, "redir https://{$BASE_DOMAIN}{uri} 308")
			require.Contains(t, site, "import site_security_headers")
			// static files only: no upstream of any kind, umami least of all
			require.NotContains(t, site, "reverse_proxy")
			require.NotRegexp(t, regexp.MustCompile(`(?i)umami|UPSTREAM`), site)

			// the website's health on port 80 for traffic manager
			require.Contains(t, got, "\thandle /site {\n\t\tredir https://{host}{uri} 308\n\t}\n")
		})
	}
}

// the core role's caddy service runs the console image, which carries the
// website at /srv/site, and names that directory as SITE_ROOT
func TestComposeNamesTheSiteRoot(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "infra", "ansible", "roles", "core", "templates", "compose.yml.j2"))
	require.NoError(t, err)
	text := string(b)
	loc := regexp.MustCompile(`(?m)^  caddy:$`).FindStringIndex(text)
	require.NotNil(t, loc, "no caddy service")
	svc := text[loc[1]:]
	if next := regexp.MustCompile(`(?m)^  [a-z0-9-]+:`).FindStringIndex(svc); next != nil {
		svc = svc[:next[0]]
	}
	require.Contains(t, svc, "image: {{ platform_images.console }}")
	require.Contains(t, svc, "SITE_ROOT: /srv/site\n")
	require.Contains(t, svc, "CONSOLE_ROOT: /srv/console\n")
}
