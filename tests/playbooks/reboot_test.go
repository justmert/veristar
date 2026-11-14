// Package playbooks pins properties of the ansible playbooks that a run on a
// real environment depends on and that ansible-lint cannot see.
package playbooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type task struct {
	Name    string         `yaml:"name"`
	When    any            `yaml:"when"`
	Meta    string         `yaml:"ansible.builtin.meta"`
	Reboot  map[string]any `yaml:"ansible.builtin.reboot"`
	Command map[string]any `yaml:"ansible.builtin.command"`
	Include string         `yaml:"ansible.builtin.include_tasks"`
}

type play struct {
	Name  string `yaml:"name"`
	Hosts string `yaml:"hosts"`
	Tasks []task `yaml:"tasks"`
}

func loadPlays(t *testing.T, name string) []play {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "playbooks", name))
	require.NoError(t, err)
	var plays []play
	require.NoError(t, yaml.Unmarshal(b, &plays))
	return plays
}

func playFor(t *testing.T, plays []play, hosts string) play {
	for _, p := range plays {
		if p.Hosts == hosts {
			return p
		}
	}
	t.Fatalf("no play for hosts %q", hosts)
	return play{}
}

func whenText(w any) string {
	switch v := w.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, p := range v {
			parts = append(parts, whenText(p))
		}
		return strings.Join(parts, " and ")
	}
	return ""
}

func index(tasks []task, match func(task) bool) int {
	for i, tk := range tasks {
		if match(tk) {
			return i
		}
	}
	return -1
}

func argvText(tk task) string {
	argv, _ := tk.Command["argv"].([]any)
	s := make([]string, 0, len(argv))
	for _, a := range argv {
		if v, ok := a.(string); ok {
			s = append(s, v)
		}
	}
	return strings.Join(s, " ")
}

// a converged node is in both edge and core. the edge play must leave it to
// the core play before it drains or reboots it: the edge play has no patroni
// switchover, so a reboot there would take the leader down unplanned, and
// it checks a caddy that a converged node runs in the core project.
func TestRebootEdgePlayLeavesConvergedNodesToCore(t *testing.T) {
	edge := playFor(t, loadPlays(t, "reboot.yml"), "edge")
	skip := index(edge.Tasks, func(tk task) bool {
		return tk.Meta == "end_host" && strings.Contains(whenText(tk.When), "groups['core']")
	})
	require.GreaterOrEqual(t, skip, 0, "edge play has no end_host for hosts that are also in core")
	drain := index(edge.Tasks, func(tk task) bool { return tk.Include == "tasks/drain.yml" })
	reboot := index(edge.Tasks, func(tk task) bool { return tk.Reboot != nil })
	require.Less(t, skip, drain, "converged nodes must be skipped before the drain")
	require.Less(t, skip, reboot, "converged nodes must be skipped before the reboot")
}

// the core play reboots converged nodes, which also carry in. and otlp.
// through the same caddy; the gateway must be ready before the undrain.
func TestRebootCorePlayWaitsForTheGatewayOnConvergedNodes(t *testing.T) {
	core := playFor(t, loadPlays(t, "reboot.yml"), "core")
	reboot := index(core.Tasks, func(tk task) bool { return tk.Reboot != nil })
	switchover := index(core.Tasks, func(tk task) bool { return tk.Include == "tasks/switchover.yml" })
	require.Less(t, switchover, reboot, "the leader is handed over before the reboot")
	gateway := index(core.Tasks, func(tk task) bool {
		a := argvText(tk)
		return strings.Contains(a, "/opt/veristar/core") && strings.Contains(a, "gateway:9090/readyz") &&
			strings.Contains(whenText(tk.When), "groups['edge']")
	})
	undrain := index(core.Tasks, func(tk task) bool { return tk.Include == "tasks/undrain.yml" })
	require.Greater(t, gateway, reboot, "core play has no gateway readiness wait for converged nodes after the reboot")
	require.Less(t, gateway, undrain, "the gateway wait must come before the undrain")
}
