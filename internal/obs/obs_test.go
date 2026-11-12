package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestForbiddenKeys(t *testing.T) {
	refused := []string{
		"ip", "IP", "client_ip", "clientIP", "remote_addr", "x-forwarded-for", "X-Real-IP",
		"user_agent", "userAgent", "User-Agent", "ua",
		"authorization", "Authorization", "cookie", "Set-Cookie",
		"token", "access_token", "scf.token.id", "sessionToken",
		"password", "adminPassword", "secret", "app_secret", "api_key", "apiKey", "secret_seed",
	}
	for _, k := range refused {
		if !Forbidden(k) {
			t.Errorf("%q should be refused", k)
		}
	}
	allowed := []string{"site_id", "tenant_id", "status", "zip", "tip", "ipfs_cid", "path", "duration_ms", "error", "addr", "country"}
	for _, k := range allowed {
		if Forbidden(k) {
			t.Errorf("%q should be allowed", k)
		}
	}
}

func decode(t *testing.T, b *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatalf("not json: %v: %s", err, b.String())
	}
	return m
}

func refusedList(m map[string]any) []string {
	raw, _ := m[RefusedKey].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		s, _ := r.(string)
		out = append(out, s)
	}
	return out
}

func TestLoggerRefusesForbiddenFields(t *testing.T) {
	var b bytes.Buffer
	log := NewLogger(&b, slog.LevelInfo)
	//nolint:sloglint // the guard must also catch keys that break the naming rule
	log.Info("request", "site_id", "s1", "client_ip", "203.0.113.9", "user_agent", "Mozilla/5.0 probe", "Authorization", "Bearer abc123")

	out := b.String()
	for _, leaked := range []string{"203.0.113.9", "Mozilla/5.0 probe", "abc123"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("forbidden value %q reached the log: %s", leaked, out)
		}
	}
	m := decode(t, &b)
	if m["site_id"] != "s1" {
		t.Fatalf("allowed field lost: %s", out)
	}
	got := refusedList(m)
	for _, k := range []string{"client_ip", "user_agent", "Authorization"} {
		if !slices.Contains(got, k) {
			t.Errorf("refused list %v does not name %s", got, k)
		}
	}
}

func TestLoggerRefusesInWithAttrsAndGroups(t *testing.T) {
	var b bytes.Buffer
	log := NewLogger(&b, slog.LevelInfo).With("token", "t-leak-1", "node", "edge-1")
	log.Info("x",
		slog.Group("req", "path", "/v1/e", "cookie", "c-leak-2"),
		slog.Group("password", "inner", "p-leak-3"),
	)
	log.WithGroup("secret").Info("y", "inner", "s-leak-4")

	out := b.String()
	for _, leaked := range []string{"t-leak-1", "c-leak-2", "p-leak-3", "s-leak-4"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("forbidden value %q reached the log: %s", leaked, out)
		}
	}
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %s", len(lines), out)
	}
	first := decode(t, bytes.NewBuffer(lines[0]))
	if first["node"] != "edge-1" {
		t.Fatalf("allowed With attr lost: %s", lines[0])
	}
	got := refusedList(first)
	for _, k := range []string{"token", "req.cookie", "password"} {
		if !slices.Contains(got, k) {
			t.Errorf("refused list %v does not name %s", got, k)
		}
	}
	second := decode(t, bytes.NewBuffer(lines[1]))
	if !slices.Contains(refusedList(second), "secret.inner") {
		t.Errorf("group refusal not reported: %s", lines[1])
	}
}

func TestOpsHandler(t *testing.T) {
	reg := NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "obs_test_total", Help: "test counter"})
	reg.MustRegister(c)
	c.Inc()

	var dbUp atomic.Bool
	log := NewLogger(io.Discard, slog.LevelInfo)
	h := NewOpsHandler(reg, log, Check{Name: "database", Fn: func(context.Context) error {
		if !dbUp.Load() {
			return errors.New("down")
		}
		return nil
	}})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeListener(ctx, NewServer("", h), ln, log) }()
	base := "http://" + ln.Addr().String()

	get := func(path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, _ := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz: %d", code)
	}
	if code, body := get("/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, "database") {
		t.Fatalf("readyz while down: %d %q", code, body)
	}
	dbUp.Store(true)
	if code, _ := get("/readyz"); code != http.StatusOK {
		t.Fatalf("readyz while up: %d", code)
	}
	code, body := get("/metrics")
	if code != http.StatusOK || !strings.Contains(body, "obs_test_total 1") || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("metrics: %d %s", code, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down")
	}
}
