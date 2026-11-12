//go:build webpath

package webpath

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// the example sites of tests/e2e/sites.ts with their fixed ids and ports
type exampleSite struct {
	Key, Host, ID, Pkg string
	Port               int
}

var (
	siteStatic = exampleSite{"static-html", "static.sa-tenant.example", "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01", "@stellar-analytics/example-static-html", 47101}
	siteSPA    = exampleSite{"vite-react-spa", "spa.sa-tenant.example", "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e02", "@stellar-analytics/example-vite-react-spa", 47102}
	siteRR     = exampleSite{"react-router-ssr", "rr.sa-tenant.example", "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e03", "@stellar-analytics/example-react-router-ssr", 47103}
	siteAstro  = exampleSite{"astro-ssr", "astro.sa-tenant.example", "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e04", "@stellar-analytics/example-astro-ssr", 47104}
	allSites   = []exampleSite{siteStatic, siteSPA, siteRR, siteAstro}
)

// fixture ports of tests/e2e/sites.ts
var fixturePorts = []int{47080, 47443, 47090}

// Browsers runs the e2e fixture (real-gateway mode, upstream = the country
// forwarder) and the four example servers once for the whole suite, and runs
// playwright against them with tests/e2e/specs/webpath/playwright.config.ts.
type Browsers struct {
	t        testing.TB
	EventLog string
	OutDir   string
	procs    []*exec.Cmd
	logOff   int64
	runs     int
}

// siteRun is one example server: the site, the tracker release its page
// loads and the package script that builds it.
type siteRun struct {
	Site    exampleSite
	Version string
	Build   string
}

func startBrowsers(t testing.TB, upstream string) *Browsers {
	t.Helper()
	runs := make([]siteRun, 0, len(allSites))
	for _, s := range allSites {
		runs = append(runs, siteRun{Site: s, Version: "1.0.0", Build: "build"})
	}
	return startBrowsersWith(t, upstream, runs)
}

// startBrowsersWith starts the fixture and the given example servers.
func startBrowsersWith(t testing.TB, upstream string, runs []siteRun) *Browsers {
	t.Helper()
	if out, err := exec.Command("node", "--version").Output(); err != nil || !strings.HasPrefix(string(out), "v26.") {
		t.Fatalf("node 26 is required on PATH (source ~/.nvm/nvm.sh && nvm use 26.13.1): %q %v", out, err)
	}
	ports := append([]int{}, fixturePorts...)
	for _, r := range runs {
		ports = append(ports, r.Site.Port)
	}
	for _, p := range ports {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 200*time.Millisecond); err == nil {
			_ = c.Close()
			t.Fatalf("port %d is in use; another e2e run (make gateway-it or pnpm test in tests/e2e) must finish first", p)
		}
	}
	dir := t.TempDir()
	b := &Browsers{t: t, EventLog: filepath.Join(dir, "events.jsonl"), OutDir: filepath.Join(dir, "out")}
	if err := os.MkdirAll(b.OutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.stop)
	b.spawn(t, "fixture", filepath.Join(repoRoot(), "tests", "e2e"), []string{"node", "fixture/main.ts"},
		"SA_E2E_GATEWAY_UPSTREAM="+upstream, "SA_E2E_EVENT_LOG="+b.EventLog)
	waitHTTP(t, "http://127.0.0.1:47090/health", 30*time.Second)
	for _, r := range runs {
		s := r.Site
		b.spawn(t, s.Key, repoRoot(), []string{"sh", "-c", "pnpm --filter " + s.Pkg + " " + r.Build + " && pnpm --filter " + s.Pkg + " start"},
			"SA_SITE_ID="+s.ID, "SA_TRACKER_VERSION="+r.Version, "SA_GATEWAY_ORIGIN=https://"+gatewayHost,
			"SA_PUBLIC_HOST="+s.Host, fmt.Sprintf("PORT=%d", s.Port), "HOST=127.0.0.1")
	}
	for _, r := range runs {
		waitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", r.Site.Port), 4*time.Minute)
	}
	return b
}

func (b *Browsers) spawn(t testing.TB, name, dir string, argv []string, env ...string) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	// own process group, so pnpm's children stop with it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logf, err := os.Create(filepath.Join(b.OutDir, name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	b.procs = append(b.procs, cmd)
}

func (b *Browsers) stop() {
	for _, c := range b.procs {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
		}
	}
	for _, c := range b.procs {
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
}

// Run runs playwright with the webpath config. projects and grep select the
// tests; env adds WEBPATH_* parameters. it returns the summary and the error.
func (b *Browsers) Run(t testing.TB, projects []string, grep string, extra []string, env ...string) (string, error) {
	t.Helper()
	args := []string{"--filter", "@stellar-analytics/e2e", "exec", "playwright", "test", "-c", "specs/webpath/playwright.config.ts"}
	for _, p := range projects {
		args = append(args, "--project", p)
	}
	if grep != "" {
		args = append(args, "--grep", grep)
	}
	args = append(args, extra...)
	cmd := exec.Command("pnpm", args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), append(env, "WEBPATH_OUT="+b.OutDir)...)
	out, err := cmd.CombinedOutput()
	// the full output is kept per run: playwright clears its own results
	// directory on the next run, which would lose the error of a failure
	b.runs++
	_ = os.WriteFile(filepath.Join(b.OutDir, fmt.Sprintf("playwright-%03d.log", b.runs)), out, 0o644)
	if keep := os.Getenv("WEBPATH_LOGS"); keep != "" && err != nil {
		_ = os.WriteFile(filepath.Join(keep, fmt.Sprintf("playwright-%03d.log", b.runs)), out, 0o644)
	}
	summary := string(out)
	if i := strings.LastIndex(summary, "Running "); i >= 0 {
		summary = summary[i:]
	}
	return summary, err
}

// MustRun is Run that fails the test when playwright fails.
func (b *Browsers) MustRun(t testing.TB, projects []string, grep string, env ...string) {
	t.Helper()
	out, err := b.Run(t, projects, grep, nil, env...)
	if err != nil {
		t.Fatalf("playwright %v %q failed: %v\n%s", projects, grep, err, out)
	}
	t.Logf("playwright %v %q:\n%s", projects, grep, lastLines(out, 6))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// LoggedEvent is one line of the fixture's event log: every POST /v1/e the
// browsers made, with the status the real gateway answered.
type LoggedEvent struct {
	Status int    `json:"status"`
	Origin string `json:"origin"`
	Body   struct {
		W  string `json:"w"`
		T  string `json:"t"`
		U  string `json:"u"`
		R  string `json:"r"`
		N  string `json:"n"`
		TS int64  `json:"ts"`
	} `json:"body"`
}

// NewEvents returns the event log lines written since the previous call.
func (b *Browsers) NewEvents(t testing.TB) []LoggedEvent {
	t.Helper()
	f, err := os.Open(b.EventLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(b.logOff, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var out []LoggedEvent
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			b.logOff += int64(len(line))
			var e LoggedEvent
			if jerr := json.Unmarshal(line, &e); jerr != nil {
				t.Fatalf("event log line %q: %v", line, jerr)
			}
			out = append(out, e)
		}
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// all202 fails unless every logged event was answered 202.
func all202(t testing.TB, evs []LoggedEvent) {
	t.Helper()
	for _, e := range evs {
		if e.Status != 202 {
			t.Errorf("browser event %s %s %q on %s answered %d", e.Body.T, e.Body.U, e.Body.N, e.Body.W, e.Status)
		}
	}
}

// Miss is one page load of the burst scenario on which umami.track() events
// never reached the gateway, with what the browser reported meanwhile.
type Miss struct {
	Project string   `json:"project"`
	Page    int      `json:"page"`
	Missing []string `json:"missing"`
	Browser []string `json:"browser"`
}

// Misses returns and clears the misses the burst scenario recorded.
func (b *Browsers) Misses(t testing.TB) []Miss {
	t.Helper()
	path := filepath.Join(b.OutDir, "misses.jsonl")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []Miss
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m Miss
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return out
}
