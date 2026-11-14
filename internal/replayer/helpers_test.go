package replayer_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	mobynet "github.com/moby/moby/api/types/network"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// desktopUA is a current desktop chrome user agent. umami's bot check passes
// it and stores the session as a laptop.
const desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// botUA is googlebot, which umami's isbot check refuses
// (docs/reference/umami-contract.md item 6).
const botUA = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

// natsEnv is a test nats server with the full topology applied.
type natsEnv struct {
	srv *natsctr.Server
	nc  *nats.Conn
	js  jetstream.JetStream
}

func startNATS(t *testing.T) *natsEnv {
	t.Helper()
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(context.Background(), js, spec); err != nil {
		t.Fatal(err)
	}
	return &natsEnv{srv: srv, nc: nc, js: js}
}

// devUmami returns the admin of the shared dev umami (make dev-up and
// umami-bootstrap). the tests only create their own websites and delete
// them again.
func devUmami(t *testing.T) (*url.URL, *umami.Admin) {
	t.Helper()
	raw := os.Getenv("UMAMI_IT_URL")
	pw := os.Getenv("UMAMI_ADMIN_PASSWORD")
	if raw == "" || pw == "" {
		t.Skip("UMAMI_IT_URL and UMAMI_ADMIN_PASSWORD not set; load deploy/compose/.env.dev against the dev stack")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := umami.New(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := umami.NewAdmin(c, umami.SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	return u, a
}

// site is a registered site with its two umami websites in a fresh team.
type site struct {
	ID         string
	Quarantine string
}

func newSite(t *testing.T, a *umami.Admin) site {
	t.Helper()
	ctx := testCtx(t, time.Minute)
	team, _, err := a.CreateTeam(ctx, "rp-"+uuid.NewString()[:12], "")
	if err != nil {
		t.Fatal(err)
	}
	s := site{ID: uuid.NewString(), Quarantine: uuid.NewString()}
	t.Cleanup(func() {
		// deleting the team deletes its websites and their data
		if err := a.DeleteTeam(context.Background(), team.ID); err != nil {
			t.Errorf("cleanup: delete team: %v", err)
		}
	})
	for _, id := range []string{s.ID, s.Quarantine} {
		if _, err := a.CreateWebsite(ctx, umami.NewWebsite{ID: id, Name: "rp-it", Domain: "rp.example", TeamID: team.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func testCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func newVT(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// pageview builds a primary-band pageview accepted at the given time.
func pageview(t *testing.T, s site, vt, path string, at time.Time) *events.WebEvent {
	t.Helper()
	id, err := events.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return &events.WebEvent{
		ID: id, SiteID: s.ID, WebsiteID: s.ID, Band: events.BandPrimary, VT: vt, UA: desktopUA,
		Country: "DE", Type: events.TypePageview, Hostname: "rp.example", URL: path,
		Title: "rp", Screen: "1920x1080", Language: "en-US", AcceptedAt: at.UnixMilli(),
	}
}

func publish(t *testing.T, js jetstream.JetStream, evs ...*events.WebEvent) {
	t.Helper()
	ctx := testCtx(t, time.Minute)
	for _, ev := range evs {
		b, err := ev.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, ev.Subject(), ev.ID, b, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// running is an in-process replayer.
type running struct {
	r      *replayer.Replayer
	ops    string
	cancel context.CancelFunc
	done   chan error
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// startReplayer runs a replayer in this process with its own nats
// connection, as a separate instance would have.
func startReplayer(t *testing.T, natsURL string, umamiURL *url.URL, node string) *running {
	t.Helper()
	return startReplayerTo(t, natsURL, []*url.URL{umamiURL}, node, 16, 512)
}

// startReplayerTo is startReplayer with several umami processes and chosen
// lane and in-flight counts.
func startReplayerTo(t *testing.T, natsURL string, umamiURLs []*url.URL, node string, workers, maxInFlight int) *running {
	t.Helper()
	nc, err := nats.Connect(natsURL, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ucs := make([]*umami.Client, 0, len(umamiURLs))
	for _, u := range umamiURLs {
		uc, err := umami.New(u, nil)
		if err != nil {
			t.Fatal(err)
		}
		ucs = append(ucs, uc)
	}
	reg := obs.NewRegistry()
	log := obs.NewLogger(testWriter{t}, slog.LevelInfo).With("node", node)
	ops := freeAddr(t)
	set := replayer.Settings{NodeID: node, OpsAddr: ops, UmamiURLs: umamiURLs, Workers: workers, MaxInFlight: maxInFlight}
	r, err := replayer.New(context.Background(), replayer.Config{Settings: set, NC: nc, Replicas: 1, Umami: ucs, Log: log, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	srvDone := make(chan error, 1)
	go func() { srvDone <- obs.Serve(ctx, obs.NewServer(ops, r.Ops()), log) }()
	rr := &running{r: r, ops: "http://" + ops, cancel: cancel, done: done}
	t.Cleanup(func() {
		rr.stop(t)
		<-srvDone
		nc.Close()
	})
	return rr
}

func (rr *running) stop(t *testing.T) {
	t.Helper()
	if rr.cancel == nil {
		return
	}
	rr.cancel()
	rr.cancel = nil
	select {
	case err := <-rr.done:
		if err != nil {
			t.Errorf("replayer run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("replayer did not stop within 30 s")
	}
}

// testWriter sends log lines to t.Log, so they appear with the failing test.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

var metricLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^}]*\})?) ([0-9eE.+-]+|NaN)`)

// scrape reads one instance's /metrics into series to value.
func scrape(t *testing.T, base string) map[string]float64 {
	t.Helper()
	m, err := scrapeErr(base)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// scrapeOK is scrape for a process that may be gone: an error gives an
// empty map.
func scrapeOK(base string) map[string]float64 {
	m, err := scrapeErr(base)
	if err != nil {
		return map[string]float64{}
	}
	return m
}

func scrapeErr(base string) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for line := range strings.SplitSeq(string(b), "\n") {
		m := metricLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v, err := strconv.ParseFloat(m[2], 64)
		if err == nil {
			out[m[1]] = v
		}
	}
	return out, nil
}

// waitFor polls cond every 100 ms until it holds or timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// drained reports whether the replayer consumer has nothing pending and
// nothing unacknowledged, and the work-queue stream is empty.
func drained(t *testing.T, js jetstream.JetStream) bool {
	t.Helper()
	ctx := testCtx(t, 10*time.Second)
	c, err := js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.Stream(ctx, natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	sinfo, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.NumPending == 0 && info.NumAckPending == 0 && sinfo.State.Msgs == 0
}

// streamMsgs reads every message of a stream by sequence. an ordered
// consumer is not allowed on a work-queue stream that already has a
// consumer, so the messages are read with direct gets.
func streamMsgs(t *testing.T, js jetstream.JetStream, stream string) []*jetstream.RawStreamMsg {
	t.Helper()
	ctx := testCtx(t, 30*time.Second)
	st, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []*jetstream.RawStreamMsg
	if info.State.Msgs == 0 {
		return nil
	}
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq; seq++ {
		m, err := st.GetMsg(ctx, seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// edgeStats sums the EDGE_STATS counts published by replayer nodes for a site.
func edgeStats(t *testing.T, js jetstream.JetStream, siteID string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, m := range streamMsgs(t, js, natsx.StreamEdgeStats) {
		stats, err := events.DecodeEdgeStats(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stats {
			if s.SiteID == siteID && strings.HasPrefix(s.Node, "replayer-") {
				out[s.Outcome] += s.Count
			}
		}
	}
	return out
}

// imageFromDevYML returns a digest-pinned image reference from dev.yml, so
// the dedicated stack runs exactly the images of the dev stack.
func imageFromDevYML(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(natsctr.RepoRoot(t), "deploy", "compose", "dev.yml"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\s*image:\s*(` + regexp.QuoteMeta(repo) + `:[^\s@]+@sha256:[0-9a-f]{64})\s*$`)
	m := re.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no digest-pinned %s image in dev.yml", repo)
	}
	return string(m[1])
}

// umamiStack is a dedicated umami with its own postgres, so a test can stop
// either without disturbing the shared dev stack. with several processes,
// URL and um are the first one's, and URLs and ums list every process.
type umamiStack struct {
	URL   *url.URL
	URLs  []*url.URL
	Admin *umami.Admin
	pg    *testcontainers.DockerContainer
	um    *testcontainers.DockerContainer
	ums   []*testcontainers.DockerContainer
}

func randomSecret(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func startUmamiStack(t *testing.T) *umamiStack {
	t.Helper()
	return startUmamiProcesses(t, 1, nil)
}

// startUmamiProcesses runs n umami processes against one postgres, all with
// one APP_SECRET, as a core node runs them. cpus, when set, caps each umami
// container's cpu (in cores), so a measurement does not take the shared
// host.
func startUmamiProcesses(t *testing.T, n int, cpus *float64) *umamiStack {
	t.Helper()
	ctx := context.Background()
	nw, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	dbPass := randomSecret(t)
	pg, err := testcontainers.Run(ctx, imageFromDevYML(t, "postgres"),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_USER": "umami", "POSTGRES_PASSWORD": dbPass, "POSTGRES_DB": "umami", "TZ": "UTC",
		}),
		tcnetwork.WithNetwork([]string{"pg"}, nw),
		// the entrypoint runs a temporary server during init; the second
		// "ready" line is the real one
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	testcontainers.CleanupContainer(t, pg)
	if err != nil {
		t.Fatal(err)
	}
	appSecret := randomSecret(t)
	st := &umamiStack{pg: pg}
	for i := range n {
		u, um := runUmami(t, nw, "postgresql://umami:"+dbPass+"@pg:5432/umami", appSecret, i, cpus)
		st.URLs = append(st.URLs, u)
		st.ums = append(st.ums, um)
	}
	st.URL, st.um = st.URLs[0], st.ums[0]
	c, err := umami.New(st.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	pw := randomSecret(t)
	if _, err := umami.Bootstrap(testCtx(t, time.Minute), c, pw); err != nil {
		t.Fatal(err)
	}
	st.Admin, err = umami.NewAdmin(c, umami.SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// runUmami starts one umami process on the stack's network. processes are
// started one after another: each runs umami's migration check at start,
// and only the first finds work to do.
func runUmami(t *testing.T, nw *testcontainers.DockerNetwork, dbURL, appSecret string, i int, cpus *float64) (*url.URL, *testcontainers.DockerContainer) {
	t.Helper()
	port := freeAddr(t)
	_, hostPort, err := net.SplitHostPort(port)
	if err != nil {
		t.Fatal(err)
	}
	loopback := netip.MustParseAddr("127.0.0.1")
	// umami's environment, as in dev.yml
	um, err := testcontainers.Run(context.Background(), imageFromDevYML(t, "ghcr.io/umami-software/umami"),
		testcontainers.WithEnv(map[string]string{
			"DATABASE_URL":       dbURL,
			"APP_SECRET":         appSecret,
			"CLIENT_IP_HEADER":   "x-edge-client-ip",
			"ENABLE_CLIENT_SALT": "1",
			"SALT_ROTATION":      "daily",
			"PRIVATE_MODE":       "1",
			"DISABLE_TELEMETRY":  "1",
			"DISABLE_UPDATES":    "1",
			"DISABLE_LOGIN":      "1",
			"TZ":                 "UTC",
		}),
		tcnetwork.WithNetwork([]string{"umami-" + strconv.Itoa(i)}, nw),
		testcontainers.WithExposedPorts("3000/tcp"),
		// a fixed host port, so the replayer's url stays valid across a stop
		// and start of the container
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = mobynet.PortMap{
				mobynet.MustParsePort("3000/tcp"): {{HostIP: loopback, HostPort: hostPort}},
			}
			if cpus != nil {
				hc.NanoCPUs = int64(*cpus * 1e9)
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/api/heartbeat").WithPort("3000/tcp").
			WithStartupTimeout(4*time.Minute)),
	)
	testcontainers.CleanupContainer(t, um)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse("http://127.0.0.1:" + hostPort)
	if err != nil {
		t.Fatal(err)
	}
	return u, um
}

func stopContainer(t *testing.T, c *testcontainers.DockerContainer) {
	t.Helper()
	timeout := 10 * time.Second
	if err := c.Stop(context.Background(), &timeout); err != nil {
		t.Fatal(err)
	}
}

func startContainer(t *testing.T, c *testcontainers.DockerContainer) {
	t.Helper()
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// waitHeartbeat polls umami until it answers.
func waitHeartbeat(t *testing.T, u *url.URL, timeout time.Duration) {
	t.Helper()
	c, err := umami.New(u, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, timeout, "umami heartbeat", func() bool {
		return c.Heartbeat(context.Background()) == nil
	})
}

// pathCounts returns views per path of a website in a range, read through
// umami's /metrics?type=path.
func pathCounts(t *testing.T, a *umami.Admin, website string, r umami.Range, limit int) map[string]int64 {
	t.Helper()
	rows, err := a.Metrics(testCtx(t, time.Minute), website, umami.MetricsQuery{Range: r, Type: "path", Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, row := range rows {
		out[row.X] = row.Y
	}
	return out
}
