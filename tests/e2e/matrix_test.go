//go:build e2ematrix

// Package e2ematrix runs the browser matrix of tests/e2e (chromium, firefox,
// webkit and firefox with global privacy control on every example site)
// against the fixture gateway, with the browsers in the official playwright
// image (tests/fixtures/browserbox): chromium aborts navigations whenever
// the host's network interfaces change, which a shared machine's docker work
// causes. the playwright runner, the fixture and the example servers stay on
// the host. run it with `make e2e-matrix`; tests/gateway TestBrowserMatrix
// runs the same specs against the real gateway image.
package e2ematrix

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/justmert/veristar/tests/fixtures/browserbox"
)

// the fixture proxy every browser uses (sites.ts PORTS.proxy)
const proxyPort = 27080

func TestBrowserMatrix(t *testing.T) {
	if out, err := exec.Command("node", "--version").Output(); err != nil || !strings.HasPrefix(string(out), "v26.") {
		t.Fatalf("node 26 is required on PATH (source ~/.nvm/nvm.sh && nvm use 26.13.1): %q %v", out, err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	box := browserbox.Start(t, proxyPort)
	cmd := exec.Command("pnpm", "--filter", "@veristar/e2e", "run", "matrix")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), box.Env()...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("playwright: %v", err)
	}
}
