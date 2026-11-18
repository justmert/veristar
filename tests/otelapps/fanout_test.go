//go:build otelapps

package otelapps

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCollectorFanout runs examples/otel-collector-fanout/collector.yaml, the
// configuration the onboarding guide for a tenant's own collector shows, on
// the stock contrib image. an app exports once to it; the test checks that
// the metrics and traces reach both the tenant backend and the platform
// stand-in, which refuses anything without the bearer token, so arrival
// there proves the collector added it.
func TestCollectorFanout(t *testing.T) {
	out := t.TempDir()
	for _, d := range []string{out, filepath.Join(out, "tenant"), filepath.Join(out, "platform")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OTELAPPS_OUT=" + out, "PLATFORM_OTLP_TOKEN=" + token}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(compose(t, env, "logs", "--no-color", "tenant-collector"))
		}
		compose(t, env, "down", "--volumes", "--remove-orphans")
	})
	compose(t, env, "up", "-d", "tenant-backend", "platform-standin", "tenant-collector")
	checkStandinAuth(t)

	// a unique service name, so only this run's data can match
	service := fmt.Sprintf("fanout-test-%d", time.Now().UnixNano())
	now := time.Now().UnixNano()
	metrics := fmt.Sprintf(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":%q}}]},
"scopeMetrics":[{"metrics":[{"name":"http.server.request.duration","unit":"s","histogram":{"aggregationTemporality":2,
"dataPoints":[{"startTimeUnixNano":"%d","timeUnixNano":"%d","count":"1","sum":0.02,
"explicitBounds":[0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10],
"bucketCounts":["0","0","1","0","0","0","0","0","0","0","0","0","0","0","0"],
"attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"http.route","value":{"stringValue":"/accounts/{id}"}},
{"key":"http.response.status_code","value":{"intValue":"200"}}]}]}}]}]}]}`, service, now-int64(time.Second), now)
	traces := fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":%q}}]},
"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"GET /accounts/{id}","kind":2,
"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},
{"key":"http.route","value":{"stringValue":"/accounts/{id}"}},{"key":"http.response.status_code","value":{"intValue":"200"}}]}]}]}]}`,
		service, now-int64(20*time.Millisecond), now)

	postWhenUp(t, "http://127.0.0.1:27211/v1/metrics", metrics)
	postWhenUp(t, "http://127.0.0.1:27211/v1/traces", traces)

	for _, backend := range []string{"tenant", "platform"} {
		for _, file := range []string{"metrics.jsonl", "traces.jsonl"} {
			path := filepath.Join(out, backend, file)
			waitContains(t, path, service)
		}
	}
}

// postWhenUp posts an otlp/json body until the collector accepts it; the
// receiver opens its port a moment after the container starts
func postWhenUp(t *testing.T, url, body string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		res, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
			err = fmt.Errorf("status %d", res.StatusCode)
		}
		if time.Now().After(deadline) {
			t.Fatalf("POST %s: %v", url, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// waitContains polls a file exporter's output until it holds s
func waitContains(t *testing.T, path, s string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(b), s) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s never contained %s", path, s)
}
