package docs

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	taskNameRe = regexp.MustCompile(`TaskName\s*=\s*"([a-z0-9-]+)"`)
	jobsRunRe  = regexp.MustCompile("jobs run ([a-z0-9-]+)")
)

// TestJobsRunTasksExist checks every `jobs run <task>` a document names
// against the task names the jobs packages register. a wrong name fails
// with unknown task at the moment an operator needs it, for example during
// the handover freeze.
func TestJobsRunTasksExist(t *testing.T) {
	root := repoRoot(t)
	tasks := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "internal", "jobs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		for _, m := range taskNameRe.FindAllStringSubmatch(read(t, p), -1) {
			tasks[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("no TaskName constants found under internal/jobs")
	}
	for _, f := range markdownFiles(t, root) {
		for _, m := range jobsRunRe.FindAllStringSubmatch(read(t, f), -1) {
			// the make target's placeholder, not a task
			if m[1] == "task" {
				continue
			}
			if !tasks[m[1]] {
				t.Errorf("%s: `jobs run %s` names no task; known tasks are registered as TaskName in internal/jobs", rel(root, f), m[1])
			}
		}
	}
}
