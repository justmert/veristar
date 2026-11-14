//go:build ansiblerender

// Package natsconf pins the nats liveness settings of the edge and core
// ansible roles that the production node loss drill of 2026-11-14 proved
// (docs/reference/drills/2026-11-13-azure.md, NATS route liveness): routes
// ping every 5 s with 3 outstanding, and each service on a converged node
// uses its own node's server first. render.yml renders the real role files
// with ansible-playbook from the ansible venv (make ansible-venv), so the
// suite sits behind its own build tag: make ansible-render-test. the client
// half is pinned in internal/natsx (TestOptionsLiveness).
package natsconf

import (
	"encoding/json"
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
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// render runs render.yml for one layout and returns the output directory,
// one subdirectory per host.
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
	cmd.Dir = filepath.Join(root, "tests", "natsconf")
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(root, "infra", "ansible", "ansible.cfg"))
	text, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", text)
	return out
}

type urls struct {
	Edge string `json:"edge"`
	Core string `json:"core"`
}

func readURLs(t *testing.T, dir, host string) urls {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, host, "urls.json"))
	require.NoError(t, err)
	var u urls
	require.NoError(t, json.Unmarshal(b, &u))
	return u
}

// block returns the body of the first top-level "name {" block of conf.
func block(t *testing.T, conf, name string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` \{\s*$`).FindStringIndex(conf)
	require.NotNil(t, loc, "no %s block", name)
	depth := 1
	for i := loc[1]; i < len(conf); i++ {
		switch conf[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return conf[loc[1]:i]
			}
		}
	}
	t.Fatalf("%s block is not closed", name)
	return ""
}

// directLine returns the setting lines of body that sit at its own level,
// leaving out nested blocks and comments.
func directLines(body string) []string {
	var out []string
	depth := 0
	for _, l := range strings.Split(body, "\n") {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if depth == 0 {
			out = append(out, s)
		}
		depth += strings.Count(s, "{") - strings.Count(s, "}")
	}
	return out
}

var nodes = map[string]string{"node-1": "10.10.1.11", "node-2": "10.10.2.12", "node-3": "10.10.3.13"}

func TestRoutePings(t *testing.T) {
	dir := render(t, "node")
	for host, ip := range nodes {
		b, err := os.ReadFile(filepath.Join(dir, host, "nats.conf"))
		require.NoError(t, err)
		lines := directLines(block(t, string(b), "cluster"))
		require.Contains(t, lines, `ping_interval: "5s"`, host)
		require.Contains(t, lines, `ping_max: 3`, host)
		// the routes go to the two peers, never to the node itself
		routes := regexp.MustCompile(`@([0-9.]+):6222`).FindAllStringSubmatch(string(b), -1)
		require.Len(t, routes, 2, host)
		for _, r := range routes {
			require.NotEqual(t, ip, r[1], host)
		}
	}
}

func TestConvergedNodeUsesItsOwnServer(t *testing.T) {
	dir := render(t, "node")
	for host, ip := range nodes {
		u := readURLs(t, dir, host)
		// the gateway and the collector reach the node's server by its
		// compose name, the core services by the node's private address;
		// neither lists a peer, so a lost peer takes no connection with it
		require.Equal(t, "tls://nats:4222", u.Edge, host)
		require.Equal(t, "tls://"+ip+":4222", u.Core, host)
	}
}

func TestSplitLayoutListsLocalServerFirst(t *testing.T) {
	dir := render(t, "split")
	for _, host := range []string{"edge-1", "edge-2", "edge-3"} {
		u := readURLs(t, dir, host)
		list := strings.Split(u.Edge, ",")
		require.Len(t, list, 3, host)
		require.Equal(t, "tls://nats:4222", list[0], host)
	}
	// each core host starts at a different edge server, and lists all three
	first := map[string]bool{}
	for _, host := range []string{"core-1", "core-2"} {
		list := strings.Split(readURLs(t, dir, host).Core, ",")
		require.Len(t, list, 3, host)
		first[list[0]] = true
	}
	require.Len(t, first, 2)
}

// TestServicesReadTheRenderedURLs checks that every nats client of the
// compose templates takes NATS_URL from the facts the tests above render,
// so a service cannot be given another list on the side.
func TestServicesReadTheRenderedURLs(t *testing.T) {
	root := repoRoot(t)
	check := func(file, want string, services []string, viaAnchor bool) {
		b, err := os.ReadFile(filepath.Join(root, "infra", "ansible", "roles", file))
		require.NoError(t, err)
		text := string(b)
		for _, m := range regexp.MustCompile(`(?m)^\s*NATS_URL:.*$`).FindAllString(text, -1) {
			require.Equal(t, `NATS_URL: "{{ `+want+` }}"`, strings.TrimSpace(m), file)
		}
		for _, svc := range services {
			loc := regexp.MustCompile(`(?m)^  ` + svc + `:.*$`).FindStringIndex(text)
			require.NotNil(t, loc, "%s has no service %s", file, svc)
			rest := text[loc[1]:]
			if next := regexp.MustCompile(`(?m)^  [a-z0-9-]+:`).FindStringIndex(rest); next != nil {
				rest = rest[:next[0]]
			}
			if viaAnchor {
				require.Regexp(t, `<<: (\*nats|\[[^]]*\*nats[^]]*\])`, rest, "%s %s", file, svc)
			} else {
				require.Contains(t, rest, `NATS_URL: "{{ `+want+` }}"`, "%s %s", file, svc)
			}
		}
	}
	check("edge/templates/compose.yml.j2", "edge_nats_urls", []string{"gateway", "collector"}, false)
	check("core/templates/compose.yml.j2", "core_nats_urls", []string{"controlplane", "replayer", "metricswriter", "jobs"}, true)
}
