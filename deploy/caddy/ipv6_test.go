package caddy_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	yaml "go.yaml.in/yaml/v3"
)

// curlImage is the client of the ipv6 address test, pinned by digest.
const curlImage = "curlimages/curl:8.11.1@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69"

// composeNetwork is the part of a compose network definition that decides
// how docker forwards a published port into the container.
type composeNetwork struct {
	EnableIPv4 *bool `yaml:"enable_ipv4"`
	EnableIPv6 *bool `yaml:"enable_ipv6"`
}

var jinjaExpr = regexp.MustCompile(`\{\{.*?\}\}`)

// jinjaList matches an expression that builds a list from literals, such as
// the core caddy's networks: {{ ['default', 'public'] + (['edge'] if core_node else []) }}
var (
	jinjaList    = regexp.MustCompile(`\{\{\s*\[.*?\}\}`)
	jinjaLiteral = regexp.MustCompile(`'([^']*)'`)
)

// listOfLiterals turns a list expression into a yaml flow list of every
// string literal in it. for the networks above that is the union of both
// branches, which is what a converged node (every production node) joins.
func listOfLiterals(expr string) string {
	var names []string
	for _, m := range jinjaLiteral.FindAllStringSubmatch(expr, -1) {
		names = append(names, m[1])
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// templateBlock returns the lines of a compose template from the line equal
// to start up to the next line indented at most as deep, with jinja
// statements and merge keys dropped, a list built from literals replaced by
// those literals and every other expression replaced by a plain word; the
// tested keys hold no other expression that matters to docker's forwarding.
func templateBlock(t *testing.T, text, start string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	depth := len(start) - len(strings.TrimLeft(start, " "))
	var out []string
	in := false
	for _, l := range lines {
		if !in {
			if l == start {
				in = true
				out = append(out, strings.TrimPrefix(l, strings.Repeat(" ", depth)))
			}
			continue
		}
		trimmed := strings.TrimSpace(l)
		// jinja statements, and merge keys whose anchors lie outside the block
		if strings.HasPrefix(trimmed, "{%") || strings.HasPrefix(trimmed, "<<:") {
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && len(l)-len(strings.TrimLeft(l, " ")) <= depth {
			break
		}
		l = jinjaList.ReplaceAllStringFunc(l, listOfLiterals)
		out = append(out, strings.TrimPrefix(jinjaExpr.ReplaceAllString(l, "x"), strings.Repeat(" ", depth)))
	}
	return strings.Join(out, "\n")
}

// caddyNetworks reads, from a role's shipped compose template, the networks
// the caddy service joins and how each one is defined. a service without a
// networks key joins only the project's default network.
func caddyNetworks(t *testing.T, role string) ([]string, map[string]composeNetwork) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "roles", role, "templates", "compose.yml.j2"))
	require.NoError(t, err)
	text := string(b)

	var svc map[string]struct {
		Networks []string `yaml:"networks"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(templateBlock(t, text, "  caddy:")), &svc), role)
	joined := svc["caddy"].Networks
	if len(joined) == 0 {
		joined = []string{"default"}
	}
	defs := map[string]composeNetwork{}
	if strings.Contains(text, "\nnetworks:\n") {
		var top struct {
			Networks map[string]composeNetwork `yaml:"networks"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(templateBlock(t, text, "networks:")), &top), role)
		defs = top.Networks
	}
	return joined, defs
}

func dockerCLI(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "docker %s: %s", strings.Join(args, " "), errb.String())
	return strings.TrimSpace(out.String())
}

// TestCaddySeesIPv6ClientAddress builds, for the edge and the
// core tier, caddy's networks exactly as the shipped compose template
// defines them, publishes caddy's 443 on an ipv4 and an ipv6 host address
// as the template does, and sends one request over each family from a
// client on a routed network (no masquerading, like a visitor on the
// internet). the upstream answers with the address header caddy set: the
// gateway's X-Edge-Client-Ip on the edge, the control plane's
// X-Sa-Client-Addr on the core. both must equal the client's own address;
// before the fix every ipv6 client arrived as the compose bridge's ipv4
// gateway through docker-proxy.
func TestCaddySeesIPv6ClientAddress(t *testing.T) {
	tiers := []struct {
		role, caddyfile, alias, host, path string
		env                                map[string]string
	}{
		{"edge", "edge.Caddyfile", "gateway", "in.edge.test", "/v1/e", baseEnv()},
		{"core", "app.Caddyfile", "controlplane", "app.sa.test", "/api/me", appEnv()},
	}
	for _, tier := range tiers {
		t.Run(tier.role, func(t *testing.T) {
			joined, defs := caddyNetworks(t, tier.role)
			id := fmt.Sprintf("sa-n3t-%s-%d", tier.role, rand.Uint32())
			r := rand.IntN(200) + 20
			s6 := rand.Uint32N(0xffff)
			cleanup := []func(){}
			t.Cleanup(func() {
				for i := len(cleanup) - 1; i >= 0; i-- {
					cleanup[i]()
				}
			})
			rm := func(args ...string) func() {
				return func() { _ = exec.Command("docker", args...).Run() }
			}

			// the visitor's side: dual stack and routed, so docker neither
			// masquerades nor proxies what leaves it
			client := id + "-client"
			v4net := fmt.Sprintf("10.%d.%d", r, rand.IntN(250)+1)
			v6net := fmt.Sprintf("fd5a:%x:%x:1", s6, rand.Uint32N(0xffff))
			dockerCLI(t, "network", "create", "--ipv6",
				"--subnet", v4net+".0/24", "--subnet", v6net+"::/64",
				"-o", "com.docker.network.bridge.gateway_mode_ipv4=routed",
				"-o", "com.docker.network.bridge.gateway_mode_ipv6=routed", client)
			cleanup = append(cleanup, rm("network", "rm", client))

			// caddy's networks as the template has them; the subnets are
			// chosen per run so parallel runs on one host do not clash
			nets := make([]string, 0, len(joined))
			for i, name := range joined {
				def := defs[name]
				n := id + "-" + name
				args := []string{"network", "create"}
				if def.EnableIPv4 != nil && !*def.EnableIPv4 {
					args = append(args, "--ipv4=false")
				}
				if def.EnableIPv6 != nil && *def.EnableIPv6 {
					args = append(args, "--ipv6", "--subnet", fmt.Sprintf("fd5a:%x:%x:%d::/64", s6, rand.Uint32N(0xffff), i+2))
				}
				dockerCLI(t, append(args, n)...)
				cleanup = append(cleanup, rm("network", "rm", n))
				nets = append(nets, n)
			}

			dir := t.TempDir()
			up := filepath.Join(dir, "upstream.Caddyfile")
			require.NoError(t, os.WriteFile(up, []byte(":8080, :8085 {\n\trespond \"{http.request.header.X-Edge-Client-Ip}{http.request.header.X-Sa-Client-Addr}\"\n}\n"), 0o644))
			require.NoError(t, os.Chmod(dir, 0o755))
			upstream := id + "-up"
			dockerCLI(t, "run", "-d", "--name", upstream, "--network", nets[0], "--network-alias", tier.alias,
				"-v", up+":/etc/caddy/Caddyfile:ro", caddyImage)
			cleanup = append(cleanup, rm("rm", "-f", upstream))

			_, file, _, _ := runtime.Caller(0)
			cf := filepath.Join(filepath.Dir(file), tier.caddyfile)
			name := id + "-caddy"
			args := []string{"create", "--name", name, "--network", nets[0],
				"-p", v4net + ".1::443", "-p", "[" + v6net + "::1]::443",
				"-v", cf + ":/etc/caddy/Caddyfile:ro"}
			for k, v := range tier.env {
				args = append(args, "-e", k+"="+v)
			}
			dockerCLI(t, append(args, caddyImage)...)
			cleanup = append(cleanup, rm("rm", "-f", name))
			for _, n := range nets[1:] {
				dockerCLI(t, "network", "connect", n, name)
			}
			dockerCLI(t, "start", name)

			ports := map[string]string{}
			for _, l := range strings.Split(dockerCLI(t, "port", name, "443/tcp"), "\n") {
				i := strings.LastIndex(l, ":")
				ports[strings.Trim(l[:i], "[]")] = l[i+1:]
			}
			for _, fam := range []struct{ addr, self string }{
				{v4net + ".1", v4net + ".9"},
				{v6net + "::1", v6net + "::9"},
			} {
				port, ok := ports[fam.addr]
				require.True(t, ok, "no published port on %s: %v", fam.addr, ports)
				dst := fam.addr
				if strings.Contains(dst, ":") {
					dst = "[" + dst + "]"
				}
				// caddy issues its internal certificate on the first request,
				// so the client retries while the site is not served yet
				got := dockerCLI(t, "run", "--rm", "--network", client, "--ip", v4net+".9", "--ip6", v6net+"::9",
					curlImage, "-sk", "--retry", "20", "--retry-all-errors", "--retry-delay", "1", "--max-time", "10",
					"--resolve", tier.host+":"+port+":"+dst, "https://"+tier.host+":"+port+tier.path)
				require.Equal(t, fam.self, got, "%s tier: client address caddy passed upstream for a request to %s", tier.role, fam.addr)
			}
		})
	}
}
