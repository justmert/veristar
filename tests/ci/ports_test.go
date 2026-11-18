package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// a five-digit number that stands on its own: not part of a hash, an
// identifier, a version or a division such as /60000
var fiveDigits = regexp.MustCompile(`(?:^|[^0-9A-Za-z+/=._-])(\d{5})(?:[^0-9A-Za-z]|$)`)

// numbers in the ephemeral range that are not ports, or that are ports for a
// reason recorded here
var notTestPorts = map[string]string{
	"deploy/compose/dev.yml 55432":                  "the dev stack's published postgresql port, which contributors configure clients with; it is not a test port",
	"deploy/compose/gen-env.sh 55432":               "the same dev stack port, written to .env.dev",
	"deploy/compose/dev.yml 50000":                  "a collector rate, not a port",
	"tests/observability/compose.yml 50000":         "a collector rate, not a port",
	"tests/isolation/dbroles/dbroles_test.go 42501": "a postgresql sqlstate, not a port",
}

// the test suites, the compose files and the example apps' own tests listen
// on fixed loopback ports. linux hands out 32768-60999 as source ports of
// outgoing connections (net.ipv4.ip_local_port_range), so a fixed listener
// there fails at random with address in use whenever some connection on the
// machine happens to hold that port. fixed test ports stay below 32768 (the
// suites use 27xxx); the rest bind port 0.
func TestNoFixedPortInEphemeralRange(t *testing.T) {
	dirs := []string{"tests", "deploy/compose", "examples", "scripts", "infra/local", "packages/tracker/test", "packages/tracker/bench"}
	files := []string{"Makefile"}
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				switch e.Name() {
				// dependencies, build output, and recorded results and test
				// vectors, which hold data rather than listeners
				case "node_modules", "dist", "build", "target", ".next", ".svelte-kit", ".astro", ".venv", ".scratch",
					"results", "vectors", "test-results":
					return filepath.SkipDir
				}
				return nil
			}
			name := e.Name()
			if strings.HasSuffix(name, "lock.json") || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".sum") ||
				strings.HasSuffix(name, "lock.yaml") {
				return nil
			}
			switch filepath.Ext(name) {
			case ".go", ".ts", ".mjs", ".js", ".yml", ".yaml", ".sh", ".conf", ".toml", ".py", ".Caddyfile", ".env", "":
				rel, _ := filepath.Rel(root, p)
				// this file names the range itself
				if rel = filepath.ToSlash(rel); rel != "tests/ci/ports_test.go" {
					files = append(files, rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 100 {
		t.Fatalf("scanned only %d files, the walk is broken", len(files))
	}
	used := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range fiveDigits.FindAllStringSubmatch(line, -1) {
				n, _ := strconv.Atoi(m[1])
				if n < 32768 || n > 60999 {
					continue
				}
				key := f + " " + m[1]
				if _, ok := notTestPorts[key]; ok {
					used[key] = true
					continue
				}
				t.Errorf("%s:%d: %d is in the ephemeral port range 32768-60999; use a fixed port below 32768 or port 0: %s",
					f, i+1, n, strings.TrimSpace(line))
			}
		}
	}
	for k := range notTestPorts {
		if !used[k] {
			t.Errorf("exception %q matches nothing any more; remove it", k)
		}
	}
}
