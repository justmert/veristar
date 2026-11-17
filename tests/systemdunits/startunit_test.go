package systemdunits

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// at boot docker's restart policy races the start-at-boot units: a container
// restarted before its dependency is up crash-loops for seconds and compose
// start gives up on it. on node 3 of production at 2026-11-14 23:33
// sa-core-start stayed failed although umami was healthy 24 s later, so every
// start unit (core, edge, observability) retries a failed start.
func TestStartUnitsRetryAFailedStart(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	roles := filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "roles")
	for _, tc := range []struct{ file, unit string }{
		{filepath.Join(roles, "core", "tasks", "start_unit.yml"), "sa-core-start.service"},
		{filepath.Join(roles, "edge", "tasks", "start_unit.yml"), "sa-edge-start.service"},
		{filepath.Join(roles, "observability", "tasks", "main.yml"), "sa-observability-start.service"},
	} {
		t.Run(tc.unit, func(t *testing.T) {
			b, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			var tasks []map[string]any
			require.NoError(t, yaml.Unmarshal(b, &tasks))

			var unit string
			for _, task := range tasks {
				if c, ok := task["ansible.builtin.copy"].(map[string]any); ok && c["dest"] == "/etc/systemd/system/"+tc.unit {
					unit, _ = c["content"].(string)
				}
			}
			require.NotEmpty(t, unit, tc.unit)

			section := func(name string) []string {
				var lines []string
				in := false
				for _, l := range strings.Split(unit, "\n") {
					l = strings.TrimSpace(l)
					if strings.HasPrefix(l, "[") {
						in = l == "["+name+"]"
						continue
					}
					if in && l != "" && !strings.HasPrefix(l, "#") {
						lines = append(lines, l)
					}
				}
				return lines
			}
			svc := section("Service")
			require.Contains(t, svc, "Type=oneshot")
			require.Contains(t, svc, "Restart=on-failure")
			require.Contains(t, svc, "RestartSec=20s")
			// still bounded: a start that keeps failing leaves the unit failed
			require.Contains(t, section("Unit"), "StartLimitBurst=6")
			require.Contains(t, section("Unit"), "StartLimitIntervalSec=30min")
		})
	}
}
