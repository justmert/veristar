package systemdunits

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// a backup unit's failed state does not last: when the patroni leader moves,
// the next run on the old leader is skipped (Result=exec-condition) and the
// skip clears the failed state, although nothing was fixed. so every run that
// was not skipped records its end and result through ExecStopPost
// (sa-backup-result), and a skipped run leaves the record as it was.
func TestBackupResultWriter(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infra", "ansible", "roles", "backup", "tasks", "timers.yml"))
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	var script string
	for _, task := range tasks {
		if c, ok := task["ansible.builtin.copy"].(map[string]any); ok && c["dest"] == "/usr/local/sbin/sa-backup-result" {
			script, _ = c["content"].(string)
		}
	}
	require.NotEmpty(t, script, "the sa-backup-result writer")

	dir := t.TempDir()
	script = strings.ReplaceAll(script, "{{ backup_textfile_dir }}", dir)
	require.NotContains(t, script, "{{")
	writer := filepath.Join(dir, "sa-backup-result")
	require.NoError(t, os.WriteFile(writer, []byte(script), 0o755))
	prom := filepath.Join(dir, "backup-verify.prom")

	run := func(result string) {
		t.Helper()
		cmd := exec.Command("/bin/sh", writer, "verify")
		cmd.Env = append(os.Environ(), "SERVICE_RESULT="+result)
		text, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", text)
	}
	series := func() []string {
		t.Helper()
		out, err := os.ReadFile(prom)
		require.NoError(t, err)
		var s []string
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if !strings.HasPrefix(l, "#") {
				s = append(s, l)
			}
		}
		return s
	}
	stamp := regexp.MustCompile(`^sa_backup_last_run_timestamp_seconds\{unit="sa-pgbackrest-verify.service"\} \d{10}$`)

	// a run skipped on a replica writes nothing
	run("exec-condition")
	_, err = os.Stat(prom)
	require.True(t, os.IsNotExist(err))

	run("exit-code")
	s := series()
	require.Len(t, s, 2)
	require.Regexp(t, stamp, s[0])
	require.Equal(t, `sa_backup_last_run_success{unit="sa-pgbackrest-verify.service"} 0`, s[1])

	// the leader moved: the skip keeps the failure on record
	run("exec-condition")
	require.Equal(t, s, series())

	run("success")
	require.Equal(t, `sa_backup_last_run_success{unit="sa-pgbackrest-verify.service"} 1`, series()[1])
	_, err = os.Stat(prom + ".tmp")
	require.True(t, os.IsNotExist(err), "no temporary file left")
}
