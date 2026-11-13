package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// the prometheus rule files of the observability role. read with regular
// expressions, not a yaml parser, because the shape is fixed and promtool
// (make ansible-lint and the rule tests) already checks the syntax.
const rulesDir = "infra/ansible/roles/observability/files/rules"

var (
	alertRe   = regexp.MustCompile(`^\s*-\s+alert:\s*(\S+)\s*$`)
	runbookRe = regexp.MustCompile(`^\s*runbook:\s*(\S+)\s*$`)
)

type alertRule struct {
	file, name, runbook string
}

func alertRules(t *testing.T, root string) []alertRule {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, rulesDir, "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no rule files under %s: %v", rulesDir, err)
	}
	var out []alertRule
	for _, f := range files {
		for _, line := range strings.Split(read(t, f), "\n") {
			if m := alertRe.FindStringSubmatch(line); m != nil {
				out = append(out, alertRule{file: rel(root, f), name: m[1]})
				continue
			}
			// a runbook annotation belongs to the alert above it; recording
			// rules carry none
			if m := runbookRe.FindStringSubmatch(line); m != nil && len(out) > 0 {
				out[len(out)-1].runbook = m[1]
			}
		}
	}
	return out
}

// TestAlertRunbookAnchors keeps every alert's runbook link working: the
// alert carries one, the document exists, and the anchor is a heading or an
// explicit <a id> in it. a broken anchor sends the person on call to the top
// of a long page in the middle of an incident (ops review).
func TestAlertRunbookAnchors(t *testing.T) {
	root := repoRoot(t)
	docs := map[string]map[string]bool{}
	for _, a := range alertRules(t, root) {
		if a.runbook == "" {
			t.Errorf("%s: alert %s has no runbook annotation", a.file, a.name)
			continue
		}
		path, anchor, ok := strings.Cut(a.runbook, "#")
		if !ok || anchor == "" {
			t.Errorf("%s: alert %s runbook %q has no anchor", a.file, a.name, a.runbook)
			continue
		}
		set, seen := docs[path]
		if !seen {
			b, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Errorf("%s: alert %s runbook document: %v", a.file, a.name, err)
				continue
			}
			set = anchors(string(b))
			docs[path] = set
		}
		if !set[anchor] {
			t.Errorf("%s: alert %s links to %s, but %s has no anchor %q", a.file, a.name, a.runbook, path, anchor)
		}
	}
}

// TestAlertCountStated checks the alert count the documents state against
// the rule files, so adding or removing an alert fails here until the
// documents say the new number (ops review). recording rules are not
// alerts and are not counted.
func TestAlertCountStated(t *testing.T) {
	root := repoRoot(t)
	n := len(alertRules(t, root))
	for _, c := range []struct {
		file string
		re   *regexp.Regexp
	}{
		{"docs/sla.md", regexp.MustCompile(`(\d+) alert rules`)},
		{"docs/runbooks/README.md", regexp.MustCompile(`of (\d+) alerts`)},
	} {
		m := c.re.FindStringSubmatch(read(t, filepath.Join(root, c.file)))
		if m == nil {
			t.Errorf("%s no longer states the alert count (%s)", c.file, c.re)
			continue
		}
		if got, _ := strconv.Atoi(m[1]); got != n {
			t.Errorf("%s states %d alerts, but %s holds %d alert rules: update the number in %s", c.file, got, rulesDir, n, c.file)
		}
	}
}
