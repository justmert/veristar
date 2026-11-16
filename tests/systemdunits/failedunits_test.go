package systemdunits

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// listing is `systemctl list-units --all --plain --no-legend --no-pager
// 'sa-*'` as a replica of production printed it on 2026-11-16, after its
// backup units had skipped their runs (Result=exec-condition, so inactive
// dead), shortened, with the verify unit failed as on a leader whose verify
// or check fails.
const listing = `sa-core-start.service          loaded inactive dead    start the core compose project's containers after boot
sa-firewall.service            loaded active   exited  veristar host firewall
sa-pgbackrest-diff.service     loaded inactive dead    pgbackrest diff of stanza sa
sa-pgbackrest-verify.service   loaded failed   failed  pgbackrest verify of stanza sa
sa-reboot-required.service     loaded inactive dead    write node_reboot_required for node_exporter
sa-pgbackrest-diff.timer       loaded active   waiting pgbackrest diff schedule
`

// the writer of sa_systemd_unit_failed (roles/observability tasks/node.yml)
// runs against a stub systemctl: a failed unit counts 1, a skipped run, an
// active and an inactive unit count 0, one series per sa-* unit.
func TestFailedUnitsWriter(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "roles", "observability", "tasks", "node.yml"))
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	var script string
	for _, task := range tasks {
		if c, ok := task["ansible.builtin.copy"].(map[string]any); ok && c["dest"] == "/usr/local/sbin/sa-systemd-failed" {
			script, _ = c["content"].(string)
		}
	}
	require.NotEmpty(t, script, "the sa-systemd-failed writer")
	require.Contains(t, script, "{{ observability_conf_dir }}/textfile/systemd.prom")

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "textfile"), 0o755))
	script = strings.ReplaceAll(script, "{{ observability_conf_dir }}", dir)
	writer := filepath.Join(dir, "sa-systemd-failed")
	require.NoError(t, os.WriteFile(writer, []byte(script), 0o755))

	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "listing"), []byte(listing), 0o644))
	stub := "#!/bin/sh\n" +
		`[ "$*" = "list-units --all --plain --no-legend --no-pager sa-*" ] || { echo "unexpected: $*" >&2; exit 2; }` + "\n" +
		"cat " + filepath.Join(dir, "listing") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "systemctl"), []byte(stub), 0o755))

	cmd := exec.Command("/bin/sh", writer)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	text, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", text)

	out, err := os.ReadFile(filepath.Join(dir, "textfile", "systemd.prom"))
	require.NoError(t, err)
	var series []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(l, "#") {
			series = append(series, l)
		}
	}
	require.Equal(t, []string{
		`sa_systemd_unit_failed{unit="sa-core-start.service"} 0`,
		`sa_systemd_unit_failed{unit="sa-firewall.service"} 0`,
		`sa_systemd_unit_failed{unit="sa-pgbackrest-diff.service"} 0`,
		`sa_systemd_unit_failed{unit="sa-pgbackrest-verify.service"} 1`,
		`sa_systemd_unit_failed{unit="sa-reboot-required.service"} 0`,
		`sa_systemd_unit_failed{unit="sa-pgbackrest-diff.timer"} 0`,
	}, series)
	require.Contains(t, string(out), "# TYPE sa_systemd_unit_failed gauge")
	_, err = os.Stat(filepath.Join(dir, "textfile", "systemd.prom.tmp"))
	require.True(t, os.IsNotExist(err), "no temporary file left")
}
