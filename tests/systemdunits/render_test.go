//go:build ansiblerender

// Package systemdunits pins properties of the systemd units the ansible roles
// install that only show on a real node. render.yml renders the backup units
// of roles/backup with ansible-playbook from the ansible venv (make
// ansible-venv), so this file sits behind the build tag of make
// ansible-render-test.
//
// every database node carries the backup timers and the patroni leader moves
// between them. in production on 2026-11-15 and 16 the units of the two
// replicas failed on every run ("unable to find primary cluster") because the
// leader gate was rendered for the s3 mode only, not for azure
// (docs/reference/drills/2026-11-13-azure.md, "Backup units failing on the
// replicas").
package systemdunits

import (
	"os"
	"os/exec"
	"path/filepath"
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

// render runs render.yml and returns the output directory, one subdirectory
// per repository mode.
func render(t *testing.T) string {
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
		"-i", "inventory.yml", "render.yml", "-e", "out="+out)
	cmd.Dir = filepath.Join(root, "tests", "systemdunits")
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(root, "infra", "ansible", "ansible.cfg"))
	text, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", text)
	return out
}

var jobs = []string{"full", "diff", "verify"}

func conditions(t *testing.T, out, mode, job string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, mode, "sa-pgbackrest-"+job+".service"))
	require.NoError(t, err)
	var found []string
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "ExecCondition="); ok {
			found = append(found, v)
		}
	}
	return found
}

// systemd unescapes $$ and %% in a command line before it runs it
func unescape(cmd string) string {
	return strings.NewReplacer("$$", "$", "%%", "%").Replace(cmd)
}

func TestBackupUnitsRunOnlyOnTheLeaderWithObjectStorage(t *testing.T) {
	out := render(t)
	for _, tc := range []struct{ mode, ip string }{{"azure", "10.10.1.11"}, {"s3", "10.10.1.12"}} {
		for _, job := range jobs {
			c := conditions(t, out, tc.mode, job)
			require.Len(t, c, 1, "%s %s: one leader gate", tc.mode, job)
			require.Contains(t, c[0], "https://"+tc.ip+":8008/primary", "%s %s", tc.mode, job)
			require.Contains(t, c[0], "--cert /etc/veristar/tls/root/node.crt", "%s %s", tc.mode, job)
		}
	}
	// the repository node of the local converge takes every backup itself
	for _, job := range jobs {
		require.Empty(t, conditions(t, out, "posix-host", job), "posix-host %s", job)
	}
}

// the gate runs as systemd would run it, with curl replaced by a stub that
// answers a given status: the leader runs the job (0), a replica skips it
// cleanly (1, a skipped run is not a failed unit), and anything else fails
// the unit (255), because then no node may be taking the backup.
func TestLeaderGateExitCodes(t *testing.T) {
	out := render(t)
	gate := unescape(conditions(t, out, "azure", "diff")[0])
	require.True(t, strings.HasPrefix(gate, "/bin/sh -c '"), gate)

	stub := filepath.Join(t.TempDir(), "curl")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s' \"$STUB_CODE\"\n[ \"$STUB_CODE\" != 000 ] || exit 7\n"), 0o755))
	gate = strings.ReplaceAll(gate, "/usr/bin/curl", stub)

	for code, want := range map[string]int{"200": 0, "503": 1, "000": 255, "401": 255, "500": 255} {
		cmd := exec.Command("/bin/sh", "-c", gate)
		cmd.Env = append(os.Environ(), "STUB_CODE="+code)
		err := cmd.Run()
		got := 0
		if err != nil {
			var ee *exec.ExitError
			require.ErrorAs(t, err, &ee, "status %s", code)
			got = ee.ExitCode()
		}
		require.Equal(t, want, got, "status %s", code)
	}
}

// every backup unit records the result of a run that was not skipped
// (backupresult_test.go), in every repository mode, and a failing writer
// never fails the backup itself (the leading -)
func TestBackupUnitsRecordTheirResult(t *testing.T) {
	out := render(t)
	for _, mode := range []string{"azure", "s3", "posix-host"} {
		for _, job := range jobs {
			b, err := os.ReadFile(filepath.Join(out, mode, "sa-pgbackrest-"+job+".service"))
			require.NoError(t, err)
			require.Contains(t, strings.Split(string(b), "\n"), "ExecStopPost=-/usr/local/sbin/sa-backup-result "+job, "%s %s", mode, job)
		}
	}
}
