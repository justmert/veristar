// Package pins checks that the production image pins held in ansible
// variables equal the one-line dockerfiles under deploy/images/upstream,
// which exist only so dependabot can propose updates for them. a dependabot
// pull request moves the dockerfile; this test stays red until the ansible
// pin is moved with it.
package pins

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	// a pinned entry of a yaml map: two-space indent, key, tag and digest
	pinLine  = regexp.MustCompile(`(?m)^  ([a-z_]+): "?([^\s"]+@sha256:[0-9a-f]{64})"?$`)
	fromLine = regexp.MustCompile(`(?m)^FROM (\S+)$`)
)

// umami moves only through make umami-upgrade, which runs the compatibility
// and isolation gates, so dependabot does not watch it (.github/dependabot.yml)
var notWatched = []string{"umami"}

const root = "../.."

func pins(t *testing.T, file string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range pinLine.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("%s: no pinned image found; the pattern no longer matches the file", file)
	}
	return out
}

func TestUpstreamDockerfilesMatchAnsiblePins(t *testing.T) {
	want := map[string]string{}
	for _, f := range []string{"infra/ansible/vars/pins.yml", "infra/ansible/roles/observability/defaults/main.yml"} {
		for k, ref := range pins(t, f) {
			if slices.Contains(notWatched, k) {
				continue
			}
			name := strings.ReplaceAll(k, "_", "-")
			if prev, ok := want[name]; ok && prev != ref {
				t.Errorf("%s is pinned twice with different references: %s and %s (%s)", k, prev, ref, f)
			}
			want[name] = ref
		}
	}

	files, err := filepath.Glob(filepath.Join(root, "deploy/images/upstream/*.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".Dockerfile")
		seen[name] = true
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		froms := fromLine.FindAllStringSubmatch(string(b), -1)
		if len(froms) != 1 {
			t.Errorf("%s: want exactly one FROM line, got %d", f, len(froms))
			continue
		}
		ref, ok := want[name]
		if !ok {
			t.Errorf("%s pins %s, but no ansible variable names %s", f, froms[0][1], name)
			continue
		}
		if froms[0][1] != ref {
			t.Errorf("%s pins %s but ansible pins %s; move the ansible pin to the same reference", f, froms[0][1], ref)
		}
	}
	for name, ref := range want {
		if !seen[name] {
			t.Errorf("ansible pins %s (%s) but deploy/images/upstream/%s.Dockerfile is missing, so dependabot never proposes an update", name, ref, name)
		}
	}
}
