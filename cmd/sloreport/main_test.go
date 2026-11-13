package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCollectThenReport runs both commands against a test fixture that
// serves gatus-shaped json (one page, oldest first), not a real gatus.
func TestCollectThenReport(t *testing.T) {
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	type res struct {
		Success   bool      `json:"success"`
		Timestamp time.Time `json:"timestamp"`
		Errors    []string  `json:"errors,omitempty"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/endpoints/"), "/statuses")
		rs := []res{
			{Success: true, Timestamp: day.Add(-30 * time.Second)},
			{Success: false, Timestamp: day.Add(time.Hour), Errors: []string{"collector answered 503"}},
			{Success: true, Timestamp: day.Add(time.Hour + 30*time.Second)},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "results": rs})
	}))
	defer srv.Close()
	dir := t.TempDir()
	now := func() time.Time { return day.Add(25 * time.Hour) }
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"collect", "-gatus", srv.URL, "-day", "2026-11-12",
		"-out", filepath.Join(dir, "data")}, &out, &errb, now)
	require.Equal(t, 0, code, errb.String())
	require.Contains(t, out.String(), "slo_ingestion 2 results")

	code = run(context.Background(), []string{"report", "-month", "2026-11", "-through", "2026-11-12",
		"-data", filepath.Join(dir, "data"), "-out", dir}, &out, &errb, now)
	require.Equal(t, 0, code, errb.String())
	b, err := os.ReadFile(filepath.Join(dir, "2026-11.md"))
	require.NoError(t, err)
	require.Contains(t, string(b), "| Ingestion (`slo_ingestion`) | 2 | 1 | 1 | 50.0000% |")
	require.Contains(t, string(b), "Missing days: 11")

	require.Equal(t, 2, run(context.Background(), []string{"collect"}, &out, &errb, now))
	require.Equal(t, 2, run(context.Background(), nil, &out, &errb, now))
}
