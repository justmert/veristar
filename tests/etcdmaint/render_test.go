//go:build ansiblerender

// Package etcdmaint pins the etcd space settings of roles/edge. etcd keeps
// every revision until it is compacted and refuses all writes once its
// database reaches the quota (the NOSPACE alarm), which stops patroni's
// leader renewals, switchovers and failovers. render.yml renders the real
// compose template and defaults with ansible-playbook from the ansible venv
// (make ansible-venv), so the suite sits behind the build tag of make
// ansible-render-test.
package etcdmaint

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

var hosts = []string{"node-1", "node-2", "node-3"}

func render(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	venv := os.Getenv("ANSIBLE_VENV")
	if venv == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		venv = filepath.Join(home, ".local", "share", "veristar", "ansible-venv")
	}
	out := t.TempDir()
	cmd := exec.Command(filepath.Join(venv, "bin", "ansible-playbook"),
		"-i", "inventory.yml", "render.yml", "-e", "out="+out)
	cmd.Dir = filepath.Join(root, "tests", "etcdmaint")
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(root, "infra", "ansible", "ansible.cfg"))
	text, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", text)
	return out
}

func TestEtcdCompactsAndHasAnExplicitQuota(t *testing.T) {
	out := render(t)
	for _, h := range hosts {
		b, err := os.ReadFile(filepath.Join(out, h, "compose.yml"))
		require.NoError(t, err)
		var c struct {
			Services map[string]struct {
				Command []string `yaml:"command"`
			} `yaml:"services"`
		}
		require.NoError(t, yaml.Unmarshal(b, &c))
		cmd := c.Services["etcd"].Command
		require.NotEmpty(t, cmd, h)
		require.Contains(t, cmd, "--auto-compaction-mode=periodic", h)
		require.Contains(t, cmd, "--auto-compaction-retention=1h", h)
		require.Contains(t, cmd, "--quota-backend-bytes=2147483648", h)
	}
}

// a member blocks its reads and writes while it defragments, so no two
// members may share a slot: each calendar is valid for systemd and the
// members run at least 20 minutes apart
func TestEtcdDefragRunsOneMemberAtATime(t *testing.T) {
	out := render(t)
	var at []time.Time
	for _, h := range hosts {
		b, err := os.ReadFile(filepath.Join(out, h, "defrag-calendar"))
		require.NoError(t, err)
		cal := strings.TrimSpace(string(b))
		require.Regexp(t, `^Tue \*-\*-\* \d\d:\d\d:00 UTC$`, cal, h)
		if p, err := exec.LookPath("systemd-analyze"); err == nil {
			text, err := exec.Command(p, "calendar", cal).CombinedOutput()
			require.NoError(t, err, "%s: %s", cal, text)
		}
		ts, err := time.Parse("15:04:05", strings.Fields(cal)[2])
		require.NoError(t, err)
		at = append(at, ts)
	}
	for i := range at {
		for j := i + 1; j < len(at); j++ {
			d := at[j].Sub(at[i])
			if d < 0 {
				d = -d
			}
			require.GreaterOrEqual(t, d, 20*time.Minute, "%s and %s", hosts[i], hosts[j])
		}
	}
}
