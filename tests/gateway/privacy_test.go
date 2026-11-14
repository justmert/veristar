//go:build gatewayit

package gatewayit

import (
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoAddressAnywhere sends real traffic from a container with its own
// address on the dedicated network, through the edge caddy, so the address
// reaches caddy as its tcp peer and the gateway only through caddy's header.
// then every queued message, every gateway log line, every caddy access log
// line and the gateway metrics are scanned for the address and the user
// agent.
func TestNoAddressAnywhere(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://privacy.sa-tenant.example", "active")
	g := s.AddGateway(t, "edge-1", nil)
	_, caddy := s.AddCaddy(t, "edge-1:8080")

	bin := filepath.Join(t.TempDir(), "probe")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./tests/gateway/probe")
	build.Dir = repoRoot()
	build.Env = append(build.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}

	addr := s.Subnet + ".77"
	agent := "sa-privacy-probe/1.0 (" + randHex(8) + ")"
	mark := lastSeq(t, s.JS)
	waitSite(t, direct(g, site.Origin, agent, "192.0.2.1"), site.ID)
	out := mustDocker(t, "run", "--rm", "--network", s.Net, "--ip", addr,
		"-v", bin+":/probe:ro", "--entrypoint", "/probe", staticImage,
		"https://"+gatewayHost, site.ID, site.Origin, agent, "5")
	t.Logf("probe from %s: %s", addr, out)

	got := queuedAfter(t, s.JS, mark, 5)
	if len(got) != 5 {
		t.Fatalf("%d events queued, want 5", len(got))
	}
	for _, q := range got {
		if q.Event.UA != agent {
			t.Fatalf("queued user agent %q, want the probe's", q.Event.UA)
		}
	}

	resp, err := http.Get(g.OpsURL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	gwLogs := logs(t, g.Name)
	caddyLogs := logs(t, caddy)
	// the caddy log scan only means something if requests were logged: the
	// challenge requests are; the event route is never access-logged
	// (log_skip in deploy/caddy/edge.Caddyfile)
	if n := strings.Count(caddyLogs, `"uri":"/v1/c?`); n < 1 {
		t.Fatalf("caddy logged %d challenge requests, want at least 1:\n%s", n, caddyLogs)
	}
	if n := strings.Count(caddyLogs, `"uri":"/v1/e"`); n != 0 {
		t.Fatalf("caddy logged %d event requests, want none:\n%s", n, caddyLogs)
	}
	scans := map[string]string{"gateway log": gwLogs, "caddy log": caddyLogs, "gateway metrics": string(metrics)}
	for i, q := range got {
		scans["queued message "+string(rune('1'+i))] = string(q.Raw)
	}
	hits := 0
	for where, text := range scans {
		if strings.Contains(text, addr) {
			hits++
			t.Errorf("%s contains the client address %s", where, addr)
		}
	}
	for _, where := range []string{"gateway log", "caddy log", "gateway metrics"} {
		if strings.Contains(scans[where], agent) {
			hits++
			t.Errorf("%s contains the user agent", where)
		}
	}
	t.Logf("scanned %d queued messages, %d gateway log bytes, %d caddy log bytes (%d event requests logged): %d hits",
		len(got), len(gwLogs), len(caddyLogs), strings.Count(caddyLogs, `"uri":"/v1/e"`), hits)
}
