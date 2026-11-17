//go:build ansiblerender

package playbooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// patroni in synchronous mode switches over only to the synchronous standby,
// so a rolling deploy in inventory order handed the leader to the next node
// to be replaced, up to three switchovers per run. deploy.yml and reboot.yml
// visit the patroni leader last (tasks/leader_last.yml), so it hands over
// once, to a member that is already done. a cluster that answers nowhere
// keeps the inventory order.
func TestLeaderLastOrder(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	root := filepath.Clean(filepath.Join(dir, "..", ".."))
	venv := os.Getenv("ANSIBLE_VENV")
	if venv == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		venv = filepath.Join(home, ".local", "share", "veristar", "ansible-venv")
	}
	member := func(name, role string) map[string]string {
		return map[string]string{"name": name, "role": role, "state": "streaming"}
	}
	for _, tc := range []struct {
		name    string
		members []map[string]string
		want    []string
	}{
		{"leader first in the inventory", []map[string]string{
			{"name": "node-1", "role": "leader", "state": "running"}, member("node-2", "sync_standby"), member("node-3", "replica"),
		}, []string{"node-2", "node-3", "node-1"}},
		{"leader in the middle", []map[string]string{
			member("node-1", "sync_standby"), {"name": "node-2", "role": "leader", "state": "running"}, member("node-3", "replica"),
		}, []string{"node-1", "node-3", "node-2"}},
		{"leader last already", []map[string]string{
			member("node-1", "replica"), member("node-2", "sync_standby"), {"name": "node-3", "role": "leader", "state": "running"},
		}, []string{"node-1", "node-2", "node-3"}},
		{"no leader known", []map[string]string{}, []string{"node-1", "node-2", "node-3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			vars, err := json.Marshal(map[string]any{"leader_last_members": tc.members})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(tmp, "members.json"), vars, 0o644))
			out := filepath.Join(tmp, "order")
			cmd := exec.Command(filepath.Join(venv, "bin", "ansible-playbook"),
				"-i", "leaderlast-inventory.yml", "leaderlast.yml",
				"-e", "@"+filepath.Join(tmp, "members.json"), "-e", "out="+out)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(root, "infra", "ansible", "ansible.cfg"))
			text, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", text)
			b, err := os.ReadFile(out)
			require.NoError(t, err)
			require.Equal(t, tc.want, strings.Fields(string(b)))
		})
	}
}

// the serial core plays of deploy.yml and reboot.yml run over the ordered
// group, right after the play that fills it
func TestCorePlaysUseTheLeaderLastOrder(t *testing.T) {
	for _, tc := range []struct{ file, group string }{{"deploy.yml", "deploy_core_order"}, {"reboot.yml", "reboot_core_order"}} {
		plays := loadPlays(t, tc.file)
		for i, p := range plays {
			if p.Hosts != tc.group {
				continue
			}
			require.Greater(t, i, 0, tc.file)
			prev := plays[i-1]
			require.Equal(t, "core", prev.Hosts, tc.file)
			require.Equal(t, "tasks/leader_last.yml", prev.Tasks[0].Include, tc.file)
		}
		require.Equal(t, tc.group, playFor(t, plays, tc.group).Hosts)
	}
}
