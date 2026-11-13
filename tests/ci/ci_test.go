// Package ci checks the ci tooling that cannot run on github yet (the review
// O-7 and O-27): that scripts/ci-local.sh covers every job and make step of
// .github/workflows/ci.yml, and that the slo collection picks exactly the
// days that are missing on origin.
package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const root = "../.."

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// a job of ci.yml without its local twin would be a gate ci-local silently
// skips; a make step missing locally would be a gate run differently
func TestCILocalCoversEveryCIJob(t *testing.T) {
	wf := read(t, ".github/workflows/ci.yml")
	local := read(t, "scripts/ci-local.sh")

	_, body, ok := strings.Cut(wf, "\njobs:\n")
	if !ok {
		t.Fatal("ci.yml has no jobs block")
	}
	var jobs []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z][a-z0-9_-]*):$`).FindAllStringSubmatch(body, -1) {
		jobs = append(jobs, m[1])
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs found in ci.yml")
	}
	var localJobs []string
	for _, m := range regexp.MustCompile(`(?m)^job_([a-z0-9_]+)\(\) \{$`).FindAllStringSubmatch(local, -1) {
		localJobs = append(localJobs, m[1])
	}
	all := regexp.MustCompile(`(?m)^all_jobs="([^"]*)"$`).FindStringSubmatch(local)
	if all == nil {
		t.Fatal("ci-local.sh has no all_jobs list")
	}
	if got := strings.Fields(all[1]); !slices.Equal(got, jobs) {
		t.Errorf("all_jobs in ci-local.sh is %v, ci.yml has %v in that order", got, jobs)
	}
	sorted := slices.Clone(jobs)
	slices.Sort(sorted)
	slices.Sort(localJobs)
	if !slices.Equal(sorted, localJobs) {
		t.Errorf("ci-local.sh defines job functions %v, ci.yml has jobs %v", localJobs, sorted)
	}

	// every make step of ci.yml, with its arguments up to any variable
	// override the runner needs (paths under RUNNER_TEMP)
	for _, m := range regexp.MustCompile(`(?m)^\s+- run: (make [a-z][a-z0-9-]*(?: CMD=[a-z]+)?)`).FindAllStringSubmatch(wf, -1) {
		if !strings.Contains(local, m[1]) {
			t.Errorf("ci.yml runs %q, ci-local.sh does not", m[1])
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	// a throwaway repository: the identity is set for these commands only
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitDay(t *testing.T, dir, branch, file string) {
	t.Helper()
	git(t, dir, "checkout", "-q", "-B", branch, "origin/main")
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", file)
	git(t, dir, "commit", "-q", "-m", file)
	git(t, dir, "push", "-q", "origin", branch)
}

func TestSLOCollectPicksMissingDays(t *testing.T) {
	script, err := filepath.Abs(filepath.Join(root, "scripts/slo-collect.sh"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	work := filepath.Join(tmp, "work")
	git(t, tmp, "init", "-q", "--bare", origin)
	git(t, tmp, "init", "-q", work)
	git(t, work, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, work, "remote", "add", "origin", origin)
	git(t, work, "push", "-q", "origin", "HEAD:main")
	git(t, work, "fetch", "-q", "origin")

	// the window from 2026-11-03 is 2026-10-28 to 2026-11-02, across two
	// months: one day merged to main, two on their month branches
	commitDay(t, work, "main", "docs/slo/data/2026-10-28.json")
	git(t, work, "fetch", "-q", "origin")
	commitDay(t, work, "slo/2026-10", "docs/slo/data/2026-10-30.json")
	commitDay(t, work, "slo/2026-11", "docs/slo/data/2026-11-01.json")
	// a file only in the working tree does not count: the run may not have pushed it
	if err := os.WriteFile(filepath.Join(work, "docs/slo/data/2026-11-02.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "fetch", "-q", "origin")

	cmd := exec.Command("sh", script, "missing", "2026-11-03")
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("missing: %v\n%s", err, out)
	}
	want := "2026-10-29\n2026-10-31\n2026-11-02\n"
	if string(out) != want {
		t.Errorf("missing days:\n%s\nwant:\n%s", out, want)
	}
}
