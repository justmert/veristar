// Package browserbox runs the playwright browsers of the browser suites
// (tests/webpath, tests/gateway TestBrowserMatrix, tests/e2e-console,
// tests/handover) in a container with its own network namespace, while the
// playwright test runner stays on the host.
//
// why: chromium watches the interfaces of its network namespace and aborts
// every navigation in flight with net::ERR_NETWORK_CHANGED when one appears
// or goes away. on a machine where other work creates and removes docker
// networks, the host namespace changes all the time, so chromium tests fail
// for reasons no service under test caused. inside a container chromium sees
// only its own loopback and eth0, which never change during a run.
//
// how: the container runs the official playwright image at the exact version
// of the workspace's @playwright/test, with that same playwright-core mounted
// read only, as `playwright run-server --unsafe` (unsafe lets the projects'
// launch options through: ignoreDefaultArgs, firefoxUserPrefs). the runner
// connects through PW_TEST_CONNECT_WS_ENDPOINT, a variable @playwright/test
// reads itself, so no config changes. the browsers reach the suites' proxies
// on 127.0.0.1 as before: inside the container a relay listens on the same
// loopback ports and passes each connection through a unix socket in a
// mounted directory to the host side of the relay here, which dials the real
// 127.0.0.1 port. nothing on the host listens beyond loopback, and every
// address a spec or config names stays valid.
//
// SA_E2E_BROWSERS=host runs the browsers on the host as before, for local
// debugging (a headed browser, for example).
package browserbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Image is the official playwright image. its tag must equal the
// @playwright/test version of the pnpm catalog (pnpm-workspace.yaml): the
// browser builds in the image are the ones that playwright-core release
// expects, and run-server refuses a client of another version. Start checks
// the match, so a playwright bump fails here until the image moves with it.
const Image = "mcr.microsoft.com/playwright:v1.64.1-noble@sha256:2f05f8a734509a7b3f26a912639488ee08f58a7b292d14d62271bcff9038768f"

// containerPort is where run-server listens inside the container.
const containerPort = "3000"

// Box is one running browser container, or the host browsers when the
// isolation is turned off.
type Box struct {
	// Name is the container name, empty in host mode.
	Name string
	ws   string
	dir  string
	lns  []net.Listener
	wg   sync.WaitGroup
}

// Host reports whether SA_E2E_BROWSERS asks for the browsers on the host.
func Host() bool { return os.Getenv("SA_E2E_BROWSERS") == "host" }

// Start starts a browser container whose loopback ports forward to the
// host's 127.0.0.1 ports given, and removes it when the test ends. the ports
// are the ones a browser connects to (the suites' http proxies); the host
// side may start listening later, a connection is dialled when it arrives.
func Start(t testing.TB, ports ...int) *Box {
	t.Helper()
	if Host() {
		t.Log("browserbox: SA_E2E_BROWSERS=host, the browsers run on the host")
		return &Box{}
	}
	if len(ports) == 0 {
		t.Fatal("browserbox: no port to forward")
	}
	pwCore := playwrightCore(t)
	// a short directory: a unix socket path has a limit of about 100 bytes
	dir, err := os.MkdirTemp("", "sa-bbox-")
	if err != nil {
		t.Fatal(err)
	}
	b := &Box{dir: dir, Name: "sa-browserbox-" + randHex(t)}
	t.Cleanup(b.stop)
	var names []string
	for _, p := range ports {
		var lc net.ListenConfig
		ln, err := lc.Listen(context.Background(), "unix", filepath.Join(dir, strconv.Itoa(p)+".sock"))
		if err != nil {
			t.Fatal(err)
		}
		b.lns = append(b.lns, ln)
		b.wg.Add(1)
		go b.relay(ln, p)
		names = append(names, strconv.Itoa(p))
	}
	uid, gid := os.Getuid(), os.Getgid()
	args := []string{
		"run", "-d", "--rm", "--name", b.Name, "--init",
		// the default bridge: an eth0 that stays as it is. without any
		// interface but loopback chromium would consider itself offline
		"--network", "bridge",
		"-p", "127.0.0.1::" + containerPort,
		// the host user, so the unix sockets created here are writable from
		// inside; chromium needs more shared memory than docker's 64 MB default
		"--user", fmt.Sprintf("%d:%d", uid, gid), "-e", "HOME=/tmp", "--shm-size", "2g",
		"-v", dir + ":/box/sock",
		"-v", pwCore + ":/pw:ro",
		"-v", filepath.Join(sourceDir(t), "box") + ":/box/bin:ro",
		"-e", "BOX_PORTS=" + strings.Join(names, ","),
		"-e", "BOX_LISTEN=" + containerPort,
	}
	// the browsers' time zone and language follow the host's as they did
	// when the browsers ran there
	for _, k := range []string{"TZ", "LANG", "LC_ALL", "LANGUAGE"} {
		if v, ok := os.LookupEnv(k); ok {
			args = append(args, "-e", k+"="+v)
		}
	}
	args = append(args, Image, "node", "/box/bin/server.mjs")
	if out, err := docker(args...).CombinedOutput(); err != nil {
		t.Fatalf("browserbox: docker run: %v\n%s", err, out)
	}
	out, err := docker("port", b.Name, containerPort+"/tcp").Output()
	if err != nil {
		t.Fatalf("browserbox: docker port: %v\n%s", err, b.logs())
	}
	hostPort := ""
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(l, "127.0.0.1:") {
			hostPort = strings.TrimPrefix(l, "127.0.0.1:")
		}
	}
	if hostPort == "" {
		t.Fatalf("browserbox: no loopback port published: %q", out)
	}
	base := "http://127.0.0.1:" + hostPort
	b.ws = "ws://127.0.0.1:" + hostPort + "/"
	deadline := time.Now().Add(time.Minute)
	for {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("browserbox: run-server did not answer on %s: %v\n%s", base, err, b.logs())
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("browserbox: browsers in container %s (%s), loopback ports %v forwarded", b.Name, b.ws, ports)
	return b
}

// Env is what a playwright run needs to use the box: the run-server address
// @playwright/test connects to, and the container name for a spec that
// starts a browser process itself (the prerender check). empty in host mode.
func (b *Box) Env() []string {
	if b.Name == "" {
		return nil
	}
	return []string{"PW_TEST_CONNECT_WS_ENDPOINT=" + b.ws, "SA_E2E_BROWSER_BOX=" + b.Name}
}

// relay passes every connection on a socket to 127.0.0.1:port.
func (b *Box) relay(ln net.Listener, port int) {
	defer b.wg.Done()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			d := net.Dialer{Timeout: 10 * time.Second}
			up, err := d.DialContext(context.Background(), "tcp", addr)
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
			<-done
			<-done
		}()
	}
}

// closeWrite passes an end of stream on, so a half-closed connection
// behaves as it would without the relay.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

func (b *Box) logs() string {
	out, _ := docker("logs", "--tail", "50", b.Name).CombinedOutput()
	return string(out)
}

func (b *Box) stop() {
	if b.Name != "" {
		_ = docker("rm", "-f", b.Name).Run()
	}
	for _, ln := range b.lns {
		_ = ln.Close()
	}
	b.wg.Wait()
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

// docker runs the docker cli with arguments this package builds itself.
func docker(args ...string) *exec.Cmd {
	return exec.CommandContext(context.Background(), "docker", args...) //nolint:gosec // arguments built here: image pin, generated name, temp paths
}

// sourceDir is this package's directory, which holds the scripts the
// container runs (box/).
func sourceDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("browserbox: cannot locate source file")
	}
	return filepath.Dir(file)
}

// playwrightCore resolves the playwright-core package @playwright/test of
// tests/e2e loads, and fails unless its version is the image's.
func playwrightCore(t testing.TB) string {
	t.Helper()
	e2e := filepath.Join(sourceDir(t), "..", "..", "e2e")
	// node resolves it the way the runner does, through the pnpm links
	script := `const { createRequire } = require("node:module");
const r = createRequire(process.argv[1] + "/package.json");
const test = r.resolve("@playwright/test/package.json");
const core = createRequire(test).resolve("playwright-core/package.json");
process.stdout.write(JSON.stringify({ core, version: require(core).version }));`
	var stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), "node", "-e", script, e2e) //nolint:gosec // a fixed script and the repository's own tests/e2e directory
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("browserbox: resolve playwright-core: %v\n%s", err, stderr.String())
	}
	var res struct{ Core, Version string }
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("browserbox: resolve playwright-core: %v: %s", err, out)
	}
	if want := imageVersion(); res.Version != want {
		t.Fatalf("browserbox: playwright-core %s in the workspace, image %s pins %s; move browserbox.Image to the same release",
			res.Version, Image, want)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(res.Core))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// imageVersion is the playwright release of Image's tag, v1.64.1-noble -> 1.64.1.
func imageVersion() string {
	tag := strings.SplitN(strings.SplitN(Image, "@", 2)[0], ":", 2)[1]
	return strings.SplitN(strings.TrimPrefix(tag, "v"), "-", 2)[0]
}

func randHex(t testing.TB) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(errors.Join(errors.New("browserbox: random name"), err))
	}
	return hex.EncodeToString(b[:])
}
