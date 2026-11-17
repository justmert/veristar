//go:build otelapps

package otelapps

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func compose(t *testing.T, env []string, args ...string) string {
	t.Helper()
	return composeIn(t, "sa-otelapps-test", []string{filepath.Join(repoRoot(t), "deploy/compose/examples.yml")}, env, args...)
}

func TestOtelApps(t *testing.T) {
	sel := selectedApps()
	if len(sel) == 0 {
		t.Fatalf("OTELAPPS_APPS=%q matches no app", os.Getenv("OTELAPPS_APPS"))
	}
	// one build up front, so cases only start containers
	baseEnv := []string{"OTELAPPS_OUT=" + t.TempDir(), "PLATFORM_OTLP_TOKEN=" + token}
	names := []string{}
	for _, a := range sel {
		names = append(names, a.name)
	}
	compose(t, baseEnv, append([]string{"build"}, names...)...)

	for _, a := range sel {
		for _, m := range modes {
			if m.only != "" && m.only != a.name {
				continue
			}
			if want := os.Getenv("OTELAPPS_MODES"); want != "" && !strings.Contains(","+want+",", ","+m.name+",") {
				continue
			}
			t.Run(a.name+"/"+m.name, func(t *testing.T) { runCase(t, a, m) })
		}
	}
}

func runCase(t *testing.T, a app, m mode) {
	out := t.TempDir()
	// the collectors run as uid 10001; precreated world-writable directories
	// keep the files deletable by the test user afterwards
	for _, d := range []string{out, filepath.Join(out, "tenant"), filepath.Join(out, "platform")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OTELAPPS_OUT=" + out, "PLATFORM_OTLP_TOKEN=" + token, fmt.Sprintf("METRICS_EXPORT_INTERVAL_MS=%d", exportInterval.Milliseconds())}
	for k, v := range m.env {
		env = append(env, k+"="+v)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(compose(t, env, "logs", "--no-color", a.name))
		}
		compose(t, env, "down", "--volumes", "--remove-orphans")
	})
	compose(t, env, "up", "-d", "--no-build", "tenant-backend", "platform-standin", a.name)

	base := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	waitHealthy(t, base+"/healthz")
	checkStandinAuth(t)

	// three export intervals of pause, so at least one periodic export
	// happens mid-run and the final one on shutdown carries the rest
	sent, err := driveLoad(base, func() { time.Sleep(3 * exportInterval) })
	if err != nil {
		t.Fatal(err)
	}
	want := map[requestKey]int64{}
	for k, v := range sent {
		want[requestKey{k.Method, a.route(k.Route), k.Status}] = v
	}
	for k, v := range expectedCounts() {
		if sent[k] != v {
			t.Fatalf("driver sent %d for %s, plan says %d", sent[k], k, v)
		}
	}

	// stopping sends sigterm; every app flushes both providers before exiting
	compose(t, env, "stop", "--timeout", "30", a.name)
	code := strings.TrimSpace(dockerOut(t, "inspect", "-f", "{{.State.ExitCode}}", "sa-otelapps-test-"+a.name+"-1"))
	if code != "0" {
		t.Errorf("%s exited with %s after sigterm, want 0", a.name, code)
	}
	logs := compose(t, env, "logs", "--no-color", a.name)
	for _, bad := range []string{"export failed", "failed to export", "Failed to export", "ExportError", "OTLPExporterError", "error sending"} {
		if strings.Contains(logs, bad) {
			t.Errorf("%s logged %q:\n%s", a.name, bad, logs)
		}
	}

	for _, backend := range []string{"tenant", "platform"} {
		dir := filepath.Join(out, backend)
		got := waitForCounts(t, dir, a, m, totalRequests())
		checkMetrics(t, backend, got.metrics, m, want)
		checkTraces(t, backend, got.traces, want)
		checkProbe(t, backend, a, m, got)
	}
}

// checkStandinAuth proves the platform stand-in refuses exports without the
// bearer token, so a passing case means the apps really sent it.
func checkStandinAuth(t *testing.T) {
	t.Helper()
	body := []byte(`{"resourceMetrics":[]}`)
	for _, h := range []string{"", "Bearer wrong"} {
		var res *http.Response
		var err error
		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:47210/v1/metrics", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if h != "" {
				req.Header.Set("Authorization", h)
			}
			if res, err = http.DefaultClient.Do(req); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("platform stand-in answered %d with authorization %q, want 401", res.StatusCode, h)
		}
	}
}

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}
