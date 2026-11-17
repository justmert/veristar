// Package otelcolcfg holds configuration overlays for the scf-otelcol
// binary that tests run on the host. only *_test.go files import it.
package otelcolcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// NoSelfMetrics writes an overlay that turns the collector's own metrics
// off, for the second --config of a test collector. collector/otelcol.yaml
// serves them on the fixed port 8888, which another collector on the host
// may hold; a test that never reads them does not need that port.
func NoSelfMetrics(t testing.TB) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "no-self-metrics.yaml")
	if err := os.WriteFile(p, []byte("service:\n  telemetry:\n    metrics:\n      level: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
