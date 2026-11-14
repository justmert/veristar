//go:build handover

// Package handover rehearses the data side of a handover on this machine
// (docs/handover/rebuild.md): environment a is populated
// through the real pipeline (browsers through the edge caddy, gateway, nats
// and replayer into umami; an instrumented example app through the
// collector and metrics writer into postgresql; tenants signing in through
// the TEST-ONLY oauth fixture), then moved into a separate environment b by
// both migration paths: a pgbackrest restore of a's backup set, copied with
// scripts/migrate.sh, and a direct pg_dump of the stopped source.
// platformctl verify-migration must report equality after each. then the
// same github users sign in to b and see their history, and after the names
// are pointed at b (the domain transfer path: a redirect would reach the
// gateway with origin null, which it refuses) the old snippet delivers
// events into b in chromium, firefox and webkit.
//
// it does not rehearse the independent rebuild on another cloud account and
// domain, which needs both to exist. run it with `make handover-it`.
package handover

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	baseDomain         = "sa-test.example"
	gatewayImage       = "veristar/gateway:handover"
	collectorImage     = "veristar/collector:handover"
	exampleImage       = "veristar/otel-go-nethttp:handover"
	postgresHAImage    = "veristar/postgres-ha:handover"
	caddyPluginImage   = "veristar/caddy:2.11.9"
	tenantBackendImage = "otel/opentelemetry-collector-contrib:0.165.0@sha256:e40d61f10c9a617cca1ffd1be4d7e2b3a4b0f0001798ff07e58cabe82e618cea"
	staticImage        = "gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
	oauthHost          = "oauth." + baseDomain
	oauthPort          = "8080"
	oauthClientID      = "ho-test-client"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func outDir() string {
	dir := filepath.Join(repoRoot(), ".scratch", "handover")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

var bins struct {
	controlplane, jobs, replayer, metricswriter, platformctl, fwd, oauth string
	dir                                                                  string
}

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "handover:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(bins.dir)
	os.Exit(code)
}

func setup() error {
	month := os.Getenv("DBIP_MONTH")
	if month == "" {
		return fmt.Errorf("DBIP_MONTH (YYYY-MM) is required; make handover-it sets it")
	}
	if out, err := exec.Command("node", "--version").Output(); err != nil || !strings.HasPrefix(string(out), "v26.") {
		return fmt.Errorf("node 26 is required on PATH: %q %w", out, err)
	}
	builds := [][]string{
		{"build", "-q", "-f", "deploy/images/gateway.Dockerfile", "--build-arg", "DBIP_MONTH=" + month, "-t", gatewayImage, "."},
		{"build", "-q", "-f", "deploy/images/caddy.Dockerfile", "-t", caddyPluginImage, "deploy/images"},
		{"build", "-q", "-f", "deploy/images/collector.Dockerfile", "-t", collectorImage, "."},
		{"build", "-q", "-f", "deploy/images/postgres-ha.Dockerfile", "-t", postgresHAImage, "deploy/images"},
		{"build", "-q", "-t", exampleImage, "examples/otel-go-nethttp"},
	}
	for _, b := range builds {
		cmd := exec.Command("docker", b...)
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("docker %s: %w\n%s", strings.Join(b, " "), err, out)
		}
	}
	dir, err := os.MkdirTemp("", "sa-ho-bin-")
	if err != nil {
		return err
	}
	bins.dir = dir
	for _, b := range []struct {
		out       *string
		pkg, tags string
	}{
		{&bins.controlplane, "./cmd/controlplane", "oauthfixture"},
		{&bins.jobs, "./cmd/jobs", ""},
		{&bins.replayer, "./cmd/replayer", ""},
		{&bins.metricswriter, "./cmd/metricswriter", ""},
		{&bins.fwd, "./tests/webpath/fwd", ""},
		{&bins.oauth, "./tests/fixtures/githuboauth/cmd/githuboauth-fixture", ""},
	} {
		*b.out = filepath.Join(dir, filepath.Base(b.pkg))
		args := []string{"build", "-trimpath", "-o", *b.out}
		if b.tags != "" {
			args = append(args, "-tags", b.tags)
		}
		cmd := exec.Command("go", append(args, b.pkg)...)
		cmd.Dir = repoRoot()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %w\n%s", b.pkg, err, out)
		}
		if err := os.Chmod(*b.out, 0o755); err != nil {
			return err
		}
	}
	bins.platformctl = filepath.Join(dir, "platformctl-host")
	cmd := exec.Command("go", "build", "-trimpath", "-o", bins.platformctl, "./cmd/platformctl")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build platformctl: %w\n%s", err, out)
	}
	cmd = exec.Command("pnpm", "--filter", "@veristar/console", "build")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("console build: %w\n%s", err, out)
	}
	return nil
}

// stage times every step, for the report in docs/handover
type stage struct {
	t     *testing.T
	start time.Time
	times []string
}

func (s *stage) done(name string) {
	d := time.Since(s.start).Round(time.Second)
	s.times = append(s.times, fmt.Sprintf("%-44s %s", name, d))
	s.t.Logf("stage %s took %s", name, d)
	s.start = time.Now()
}

// state is what the populate spec wrote: users, tenants, sites and what
// was sent, read back by the later phases and by the checks here
type state struct {
	Users   []struct{ ID int64 } `json:"users"`
	Tenants []struct {
		ID       string `json:"id"`
		Requests int64  `json:"requests"`
		Sites    []struct {
			ID        string `json:"id"`
			Host      string `json:"host"`
			Pageviews int64  `json:"pageviews"`
		} `json:"sites"`
	} `json:"tenants"`
}

func readState(t *testing.T, path string) state {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Tenants) < 2 {
		t.Fatalf("populate wrote %d tenants, want at least 2", len(st.Tenants))
	}
	return st
}

// playwright runs one phase of tests/e2e-console/handover against whatever
// the switchboard points at.
func playwright(t *testing.T, sb *switchboard, s *Stack, phase, statePath, appsCtl string, projects ...string) {
	t.Helper()
	routes, err := json.Marshal(sb.Routes())
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--filter", "@veristar/e2e-console", "exec", "playwright", "test", "-c", "handover.config.ts"}
	for _, p := range projects {
		args = append(args, "--project="+p)
	}
	args = append(args, "--grep", "@"+phase)
	_ = os.Remove(filepath.Join(outDir(), "real-failures.jsonl"))
	cmd := exec.Command("pnpm", args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(),
		"CE2E_BASE_DOMAIN="+baseDomain,
		"CE2E_OAUTH_URL=http://"+oauthHost+":"+oauthPort,
		"CE2E_OAUTH_CTL="+s.OAuthCtl,
		"CE2E_PROXY_PORT="+freePort(t),
		"CE2E_SITE_CTL_PORT="+freePort(t),
		"CE2E_ROUTES="+string(routes),
		"CE2E_OUT="+outDir(),
		"CE2E_APPS_CTL="+appsCtl,
		"HO_STATE="+statePath,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runErr := cmd.Run()
	if b, err := os.ReadFile(filepath.Join(outDir(), "real-failures.jsonl")); err == nil && len(b) > 0 {
		t.Errorf("failed attempts not caused by a network change:\n%s", b)
	}
	if runErr != nil {
		t.Fatalf("playwright %s: %v", phase, runErr)
	}
}

// waitFor polls a query until it returns want.
func waitFor(t *testing.T, what string, timeout time.Duration, got func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		if last = got(); last == want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s: got %q, want %q after %s", what, last, want, timeout)
}

func migrate(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(), "scripts", "migrate.sh"), args...)
	cmd.Dir = repoRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migrate.sh %v: %v\n%s", args, err, out)
	}
	t.Logf("migrate.sh %s:\n%s", args[0], out)
	return string(out)
}

func verify(t *testing.T, src, dst string) string {
	t.Helper()
	cmd := exec.Command(bins.platformctl, "verify-migration", "--source", src, "--target", dst)
	out, err := cmd.CombinedOutput()
	t.Logf("verify-migration:\n%s", out)
	if err != nil {
		t.Fatalf("verify-migration: %v", err)
	}
	if !strings.Contains(string(out), "result: source and target are equal") {
		t.Fatal("verify-migration did not report equality")
	}
	return string(out)
}

// sum is a helper for the counts the specs report
func sumPageviews(st state) map[string]int64 {
	out := map[string]int64{}
	for _, tn := range st.Tenants {
		for _, s := range tn.Sites {
			out[s.ID] = s.Pageviews
		}
	}
	return out
}

func TestHandover(t *testing.T) {
	st := &stage{t: t, start: time.Now()}
	defer func() {
		_ = os.WriteFile(filepath.Join(outDir(), "timings.txt"), []byte(strings.Join(st.times, "\n")+"\n"), 0o644)
	}()
	statePath := filepath.Join(outDir(), "state.json")
	_ = os.Remove(statePath)
	hosts := []string{"app." + baseDomain + ":443", "web." + baseDomain + ":443", "in." + baseDomain + ":443", oauthHost + ":" + oauthPort}
	sb := newSwitchboard(t, hosts)

	// environment a, the one handed over
	a := newStack(t, "a")
	a.StartPostgres(t, true)
	a.StartUmami(t, true)
	a.StartNATS(t)
	a.StartOAuthFixture(t)
	a.StartControlPlane(t)
	a.StartReplayer(t)
	a.StartCollector(t)
	a.StartMetricsWriter(t)
	a.StartTenantBackend(t)
	a.StartEdge(t)
	a.StartWeb(t)
	a.StartApp(t)
	appsA := a.AppsControl(t)
	sb.Point(t, a)
	st.done("environment a up")

	playwright(t, sb, a, "populate", statePath, appsA, "chromium")
	data := readState(t, statePath)
	st.done("populate through the real pipeline")

	// everything accepted reached its store before anything stops
	for id, n := range sumPageviews(data) {
		waitFor(t, "pageviews of site "+id+" in umami a", 3*time.Minute, func() string {
			return a.psql(t, "umami", "select count(*) from website_event where website_id = '"+id+"' and event_type = 1")
		}, strconv.FormatInt(n, 10))
	}
	a.StopApps(t)
	for _, tn := range data.Tenants {
		waitFor(t, "api requests of tenant "+tn.ID+" in minute rows of a", 3*time.Minute, func() string {
			return a.psql(t, "platform", "select coalesce(sum(count), 0) from api.metrics_minute where source = 'metric' and tenant_id = '"+tn.ID+"'")
		}, strconv.FormatInt(tn.Requests, 10))
	}
	// a leading daemon runs the api rollup and the web aggregates at once
	a.RestartJobs(t)
	for _, tn := range data.Tenants {
		waitFor(t, "hourly api rows of tenant "+tn.ID, 3*time.Minute, func() string {
			return a.psql(t, "platform", "select coalesce(sum(count), 0) from api.metrics_hourly where source = 'metric' and tenant_id = '"+tn.ID+"'")
		}, strconv.FormatInt(tn.Requests, 10))
	}
	for id, n := range sumPageviews(data) {
		waitFor(t, "web aggregate of site "+id, 3*time.Minute, func() string {
			return a.psql(t, "platform", "select coalesce(sum(pageviews), 0) from agg.web_daily where site_id = '"+id+"' and band = 'primary'")
		}, strconv.FormatInt(n, 10))
	}
	st.done("data stored, rolled up and aggregated in a")

	// drain and stop: nothing may write to a's databases during the copy.
	// the edge goes first, so no new event is accepted, then the consumers
	// once their queues are empty
	edgeA := []string{a.name("fwd"), a.Edge, a.name("gateway"), a.name("collector")}
	a.Stop(t, edgeA...)
	waitFor(t, "WEB_EVENTS and API_METRICS of a drained", time.Minute, func() string {
		var n uint64
		for _, name := range []string{"WEB_EVENTS", "API_METRICS"} {
			str, err := a.JS.Stream(t.Context(), name)
			if err != nil {
				return err.Error()
			}
			info, err := str.Info(t.Context())
			if err != nil {
				return err.Error()
			}
			n += info.State.Msgs
		}
		return strconv.FormatUint(n, 10)
	}, "0")
	a.Stop(t, a.name("replayer"), a.name("metricswriter"), a.Jobs, a.name("controlplane"), a.name("app"), a.name("web"), a.Umami)
	st.done("environment a drained and stopped")

	// the pgbackrest path: a full backup of a, restored in the new
	// environment to the end of that backup
	mustDocker(t, "exec", a.Postgres, "pgbackrest", "--stanza=sa", "--type=full", "backup")
	info := mustDocker(t, "exec", a.Postgres, "pgbackrest", "--stanza=sa", "--output=json", "info")
	var infos []struct {
		Backup []struct {
			Label string `json:"label"`
		} `json:"backup"`
	}
	if err := json.Unmarshal([]byte(info), &infos); err != nil || len(infos) != 1 || len(infos[0].Backup) == 0 {
		t.Fatalf("pgbackrest info: %v %s", err, info)
	}
	label := infos[0].Backup[len(infos[0].Backup)-1].Label
	st.done("pgbackrest full backup of a (" + label + ")")

	// environment b, built by the new operator with its own secrets
	b := newStack(t, "b")
	b.StartPostgres(t, false)
	b.StartUmami(t, true)
	b.StartNATS(t)
	b.Stop(t, b.Umami)
	st.done("environment b bootstrapped")

	// restore into a fresh volume on b's side, then copy from that
	// standalone server, as make migrate-export METHOD=pgbackrest does with
	// restore.yml on the new environment's restore node
	restoreVol := b.name("restore")
	mustDocker(t, "volume", "create", restoreVol)
	b.vols = append(b.vols, restoreVol)
	pgbr := []string{"run", "--rm", "--user", "postgres", "-v", restoreVol + ":/var/lib/postgresql",
		"-v", a.RepoVolume + ":/var/spool/pgbackrest:ro", "-v", a.RepoConf + ":/etc/pgbackrest/pgbackrest.conf:ro"}
	mustDocker(t, append(append([]string(nil), pgbr...), "--entrypoint", "mkdir", postgresHAImage, "-m", "0700", "-p", "/var/lib/postgresql/18/docker")...)
	mustDocker(t, append(append([]string(nil), pgbr...), "--entrypoint", "pgbackrest", postgresHAImage,
		"--stanza=sa", "--set="+label, "--type=immediate", "--target-action=promote", "restore")...)
	restored := b.name("restored")
	restoredPort := freePort(t)
	mustDocker(t, "run", "-d", "--name", restored, "--network", b.Net, "--user", "postgres", "-p", "127.0.0.1:"+restoredPort+":5432",
		"-v", restoreVol+":/var/lib/postgresql", "-v", a.RepoVolume+":/var/spool/pgbackrest:ro",
		"-v", a.RepoConf+":/etc/pgbackrest/pgbackrest.conf:ro", "--entrypoint", "docker-entrypoint.sh", postgresHAImage,
		"postgres", "-c", "archive_mode=off", "-c", "restore_command=pgbackrest --stanza=sa archive-get %f \"%p\"")
	b.track(restored)
	b.waitPostgres(t, restored)
	st.done("pgbackrest restore of " + label + " in b")

	restoredEnv := filepath.Join(t.TempDir(), "restored.env")
	if err := os.WriteFile(restoredEnv, []byte(strings.Join([]string{
		"PLATFORM_DB_HOST=127.0.0.1", "PLATFORM_DB_PORT=" + restoredPort, "PLATFORM_DB_NAME=platform",
		"PLATFORM_DB_SSLMODE=disable", "PLATFORM_DB_ADMIN_USER=postgres",
		// a physical copy carries the source's logins
		"PLATFORM_DB_ADMIN_PASSWORD=" + a.PostgresPW, "UMAMI_DB_NAME=umami",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dumps := t.TempDir()
	viaBackup := filepath.Join(dumps, "via-pgbackrest")
	migrate(t, "export", restoredEnv, viaBackup)
	b.Stop(t, restored)
	st.done("export from the restored server")

	envA := func() string { return a.EnvFile(t) }
	// verify-migration reads umami a's /stats; umami only reads here
	a.StartUmami(t, false)
	importInto := func(dir string) {
		t.Helper()
		migrate(t, "import", b.EnvFile(t), dir)
		b.hostPlatformctl(t, nil, "db", "migrate")
		// the imported umami database holds a's admin password; b keeps its own
		b.StartUmami(t, false)
		b.hostPlatformctl(t, map[string]string{
			"UMAMI_URL": b.UmamiURL.String(), "UMAMI_ADMIN_PASSWORD": b.UmamiPW, "UMAMI_ADMIN_PASSWORD_PREVIOUS": a.UmamiPW,
		}, "umami", "rotate-admin")
	}
	importInto(viaBackup)
	st.done("import of the pgbackrest copy into b")
	out1 := verify(t, envA(), b.EnvFile(t))
	st.done("verify-migration after the pgbackrest path")

	// the different-provider path: pg_dump straight from the stopped source,
	// imported over the first copy
	a.Stop(t, a.Umami)
	direct := filepath.Join(dumps, "direct")
	migrate(t, "export", a.EnvFile(t), direct)
	a.StartUmami(t, false)
	b.Stop(t, b.Umami)
	importInto(direct)
	st.done("pg_dump path export and import")
	out2 := verify(t, envA(), b.EnvFile(t))
	st.done("verify-migration after the pg_dump path")
	_ = os.WriteFile(filepath.Join(outDir(), "verify-migration.txt"), []byte("pgbackrest path:\n"+out1+"\npg_dump path:\n"+out2), 0o644)

	// a is no longer needed; free the machine before b's services start
	a.cleanup(t)

	// b's own user password key for today, and the registries from cp
	b.UserKey = key32()
	b.JobsRun(t, "umami-rotate", "--force")
	b.JobsRun(t, "reconcile")
	st.done("umami-rotate --force and reconcile on b")
	b.StartOAuthFixture(t)
	b.StartControlPlane(t)
	b.StartReplayer(t)
	b.StartCollector(t)
	b.StartMetricsWriter(t)
	b.StartEdge(t)
	b.StartWeb(t)
	b.StartApp(t)
	appsB := b.AppsControl(t)
	// the transfer: the same names now lead to b
	sb.Point(t, b)
	st.done("environment b up, names pointed at b")

	playwright(t, sb, b, "history", statePath, appsB, "chromium")
	st.done("tenants signed in to b and saw their history")

	before := map[string]int64{}
	for id := range sumPageviews(data) {
		n, _ := strconv.ParseInt(b.psql(t, "umami", "select count(*) from website_event where website_id = '"+id+"' and event_type = 1"), 10, 64)
		before[id] = n
	}
	engines := []string{"chromium", "firefox", "webkit"}
	playwright(t, sb, b, "cutover", statePath, appsB, engines...)
	// the cutover spec loads the first site of the first tenant once per
	// engine with the snippet pasted before the migration
	first := data.Tenants[0].Sites[0].ID
	// at least one per engine; a retried attempt (network change only, see
	// playwright()) may have had its pageview accepted before it failed
	deadline := time.Now().Add(2 * time.Minute)
	for {
		n, _ := strconv.ParseInt(b.psql(t, "umami", "select count(*) from website_event where website_id = '"+first+"' and event_type = 1"), 10, 64)
		if n >= before[first]+int64(len(engines)) {
			t.Logf("old snippet: %d pageviews before the cutover, %d after", before[first], n)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old snippet: %d pageviews before the cutover, %d after, want at least %d more", before[first], n, len(engines))
		}
		time.Sleep(time.Second)
	}
	hostsSeen := b.psql(t, "umami", "select string_agg(distinct hostname, ',') from website_event where website_id = '"+first+"'")
	if hostsSeen != data.Tenants[0].Sites[0].Host {
		t.Fatalf("events of site %s carry hostnames %q", first, hostsSeen)
	}
	st.done("old snippet delivered into b in three engines")
}
