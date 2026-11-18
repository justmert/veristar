package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// composeStopRe matches a docker compose command that stops services and
// the rest of its line
var composeStopRe = regexp.MustCompile("docker compose (?:stop|rm -sf|kill)((?: [a-z][a-z0-9-]*)+)")

// TestRunbooksStopEveryUmamiProcess keeps the operator documents from
// stopping umami process 1 alone. the core node runs core_umami_processes
// umami services (umami, umami-2, ...); a runbook that stops only `umami`
// leaves the others running, which keeps a connection to the umami
// database open (a restore's drop database then fails) and keeps
// delivering part of the events it says are queued.
func TestRunbooksStopEveryUmamiProcess(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, dir := range []string{"docs/runbooks", "docs/deploy", "docs/handover"} {
		m, err := filepath.Glob(filepath.Join(root, dir, "*.md"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Fatal("no documents found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range composeStopRe.FindAllStringSubmatch(line, -1) {
				services := strings.Fields(m[1])
				hasUmami, hasSecond := false, false
				for _, s := range services {
					hasUmami = hasUmami || s == "umami"
					hasSecond = hasSecond || s == "umami-2"
				}
				if hasUmami && !hasSecond {
					rel, _ := filepath.Rel(root, f)
					t.Errorf("%s:%d stops umami but not umami-2: %q", rel, i+1, strings.TrimSpace(m[0]))
				}
			}
		}
	}
}
