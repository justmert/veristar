package docs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoTrackedBuildOutput fails when a file that .gitignore excludes is
// still tracked, such as a compiled binary, a wheel or python bytecode. they
// are opaque to review and to the licence gate, and running the scripts
// rewrites them (ops review). a tracked file already deleted from the
// working tree is a removal waiting for its commit and is not reported.
func TestNoTrackedBuildOutput(t *testing.T) {
	root := repoRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", "ls-files", "-ci", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "not a git repository") {
			t.Skip("not a git checkout")
		}
		t.Fatalf("git ls-files: %v", err)
	}
	for _, f := range strings.Fields(string(out)) {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("%s is tracked but matches .gitignore: remove it from the repository", f)
		}
	}
}
