package erasure_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/erasure"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/jobs/aggregateweb"
	"github.com/justmert/veristar/internal/jobs/erasurefollowup"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// clock is the gateways' injected clock, so a test can pass utc midnight.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// logBuf collects every log line of every component the test runs, so the
// residue check can read them.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// env is real postgresql (platform), nats with the topology's users and
// permissions, umami, the gateway's visitor code answering edge.visitor-token
// as the gateway user, the replayer as the replayer user, the control plane
// api with the erasure routes as the controlplane user, and the jobs
// follow-up as the jobs user.
type env struct {
	pg    *dbtest.Postgres
	d     *pdb.DB
	um    *umamictr.Stack
	logs  *logBuf
	log   *slog.Logger
	clock *clock
	srv   *httptest.Server
	fx    *httptest.Server
	nats  *natsctr.Server
	creds map[string]string
	admin jetstream.JetStream
	gwJS  jetstream.JetStream
	gwNC  *nats.Conn
	gwSub *nats.Subscription
	vis   *gateway.Visitor
	rp    struct {
		mu     sync.Mutex
		cancel context.CancelFunc
		done   chan struct{}
	}
	leader *scheduler.Leader
	agg    *aggregateweb.Aggregator
	follow *erasurefollowup.Followup
	eraser *erasure.Service
	// flaky is the umami client of the eraser; a test arms it to fail
	// session deletes, as an umami or network error would
	flaky  *flakyUmami
	tenant uuid.UUID
	site   jobstest.WebSite
	owner  *client
	nextGH int64
	terms  string
	// root is the top-level test: long-lived resources clean up with it,
	// not with the subtest that happened to start them
	root *testing.T
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (e *env) conn(t *testing.T, service string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	cfg, err := config.LoadNATS(config.FromMap(map[string]string{
		"NATS_URL": e.nats.URL, "NATS_REPLICAS": "1", "NATS_USER": service, "NATS_PASSWORD": e.creds[service],
	}))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, service, e.log)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return nc, js
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := t.Context()
	e := &env{logs: &logBuf{}, clock: &clock{t: time.Now().UTC()}, nextGH: 810_000, root: t}
	e.log = slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// nats with the per-service users of the topology, so a permission the
	// erasure workflow lacks fails here
	users := natsx.Users()
	e.creds = map[string]string{}
	for _, u := range users {
		e.creds[u.Name] = "pw-" + u.Name + "-" + randHex(8)
	}
	authConf, err := natsx.RenderAuthorization(users, e.creds)
	require.NoError(t, err)
	e.nats = natsctr.StartSingle(t, natsctr.WithConfig(string(authConf)))
	_, e.admin = e.conn(t, natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, e.admin, spec)
	require.NoError(t, err)

	e.pg = jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(e.pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleJobs)
	require.NoError(t, err)
	e.d, err = pdb.Open(ctx, pcfg, "erasure-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)

	e.um = umamictr.Start(t)
	cpNC, cpJS := e.conn(t, natsx.ServiceControlPlane)
	// the site, its umami team and websites, and its registry entry, written
	// as the control plane writes registry_sites
	e.tenant, e.site = func() (uuid.UUID, jobstest.WebSite) {
		tenant, sites := jobstest.NewWebTenant(t, e.pg, e.um, cpJS, "erasure-test", 1)
		return tenant, sites[0]
	}()

	// gateway: visitor tokens from the injected clock, answering requests
	gwNC, gwJS := e.conn(t, natsx.ServiceGateway)
	e.gwJS = gwJS
	e.gwNC = gwNC
	e.serveGateway(t, e.clock)

	e.startReplayer(t)

	// jobs: leader on the jobs user's locks bucket, aggregates, follow-up
	_, jobsJS := e.conn(t, natsx.ServiceJobs)
	locks, err := jobsJS.KeyValue(ctx, natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(locks, natsx.LockJobs, "erasure-test")
	require.NoError(t, err)
	_, err = lock.Acquire(ctx)
	require.NoError(t, err)
	e.leader, err = scheduler.NewLeader(lock, e.d)
	require.NoError(t, err)
	sites := registry.NewSiteWatcher(jobsJS, e.log)
	wctx, wcancel := context.WithCancel(context.Background())
	t.Cleanup(wcancel)
	go func() { _ = sites.Run(wctx) }()
	require.Eventually(t, sites.Loaded, 30*time.Second, 50*time.Millisecond)
	e.agg, err = aggregateweb.New(e.um.Admin, sites, time.Now, e.log, prometheus.NewRegistry())
	require.NoError(t, err)
	e.follow, err = erasurefollowup.New(e.agg, time.Now, e.log)
	require.NoError(t, err)

	// control plane
	pub, err := registry.NewPublisher(ctx, cpJS)
	require.NoError(t, err)
	key, err := umami.NewPasswordKey([]byte(randHex(16)))
	require.NoError(t, err)
	rec := &provision.Reconciler{Admin: e.um.Admin, Registry: pub, Key: key, Tx: e.d.WithSystem, Now: time.Now, Log: e.log}
	cur, err := terms.Current()
	require.NoError(t, err)
	scope, err := tenants.NewScope(e.d, cur.Version, rec, e.log)
	require.NoError(t, err)
	t.Cleanup(scope.Wait)
	e.flaky = &flakyUmami{Admin: e.um.Admin}
	e.eraser, err = erasure.New(erasure.Config{DB: e.d, NC: cpNC, Umami: e.flaky, QueueWait: 2 * time.Minute, Now: time.Now, Log: e.log})
	require.NoError(t, err)
	t.Cleanup(e.eraser.Wait)
	routes, err := erasure.NewRoutes(scope, e.eraser)
	require.NoError(t, err)
	sessions := session.New(e.d)

	var api http.Handler
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.ServeHTTP(w, r) }))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	fx := githuboauth.New(githuboauth.Config{
		ClientID: "erasure-test", ClientSecret: "erasure-test-secret", RedirectURL: e.srv.URL + "/api/auth/github/callback",
	})
	e.fx = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fx.Close)
	stateKey := make([]byte, 32)
	_, _ = rand.Read(stateKey)
	ah, err := auth.New(auth.Config{
		Reconcile: scope.Reconcile, ClientID: "erasure-test",
		ClientSecret: config.FromMap(map[string]string{"S": "erasure-test-secret"}).Secret("S", 1),
		RedirectURL:  e.srv.URL + "/api/auth/github/callback", StateKey: stateKey, Operators: map[int64]bool{},
		Endpoints: auth.Endpoints{
			OAuth: oauth2.Endpoint{AuthURL: e.fx.URL + "/login/oauth/authorize", TokenURL: e.fx.URL + "/login/oauth/access_token",
				AuthStyle: oauth2.AuthStyleInParams},
			APIBase: e.fx.URL,
		},
	}, e.d, sessions)
	require.NoError(t, err)
	api = controlplane.NewHandler(controlplane.Options{
		Log: e.log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public:  []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{controlplane.RoutesFunc(ah.Private), routes},
	})
	e.terms = cur.Version
	e.owner = e.member(t, "owner", cur.Version)
	return e
}

// flakyUmami is the real umami admin client whose session deletes fail
// while failDeletes is above zero, one failure per call.
type flakyUmami struct {
	*umami.Admin
	failDeletes atomic.Int32
}

func (f *flakyUmami) DeleteSession(ctx context.Context, website, session string) error {
	if f.failDeletes.Add(-1) >= 0 {
		return errors.New("umami delete session: injected failure")
	}
	f.failDeletes.Store(0)
	return f.Admin.DeleteSession(ctx, website, session)
}

// serveGateway replaces the gateway's visitor code with one on clock c.
// a visitor never goes back to an earlier day, so passing midnight and
// returning needs a new one, as a restarted gateway would be.
func (e *env) serveGateway(t *testing.T, c *clock) {
	t.Helper()
	if e.gwSub != nil {
		require.NoError(t, e.gwSub.Drain())
	}
	secrets, err := e.gwJS.KeyValue(t.Context(), natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	e.vis, err = gateway.NewVisitor(secrets, c.Now)
	require.NoError(t, err)
	e.gwSub, err = e.vis.ServeTokenRequests(e.gwNC, e.log)
	require.NoError(t, err)
	require.NoError(t, e.gwNC.Flush())
	sub := e.gwSub
	e.root.Cleanup(func() { _ = sub.Drain() })
}

// startReplayer runs the replayer as the replayer user until stopReplayer.
func (e *env) startReplayer(t *testing.T) {
	t.Helper()
	rpNC, _ := e.conn(e.root, natsx.ServiceReplayer)
	r, err := replayer.New(t.Context(), replayer.Config{
		Settings: replayer.Settings{NodeID: "rp-" + randHex(3), OpsAddr: "127.0.0.1:0", UmamiURLs: []*url.URL{e.um.URL}, Workers: 4, MaxInFlight: 64},
		NC:       rpNC, Replicas: 1, Umami: []*umami.Client{e.um.Client}, Log: e.log, Registry: prometheus.NewRegistry(),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()
	e.rp.mu.Lock()
	e.rp.cancel, e.rp.done = cancel, done
	e.rp.mu.Unlock()
	e.root.Cleanup(e.stopReplayer)
}

func (e *env) stopReplayer() {
	e.rp.mu.Lock()
	cancel, done := e.rp.cancel, e.rp.done
	e.rp.cancel, e.rp.done = nil, nil
	e.rp.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

type client struct {
	e  *env
	hc *http.Client
	id uuid.UUID
}

// member signs a new github user in through the oauth fixture and makes it
// a member of the test tenant with role.
func (e *env) member(t *testing.T, role, termsVersion string) *client {
	t.Helper()
	e.nextGH++
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &client{e: e, hc: &hc}
	res, err := c.hc.Get(e.srv.URL + "/api/auth/github/start?return_to=/")
	require.NoError(t, err)
	_ = res.Body.Close()
	authz, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", strconv.FormatInt(e.nextGH, 10))
	form.Set("fixture_login", fmt.Sprintf("user%d", e.nextGH))
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = plain.PostForm(e.fx.URL+"/login/oauth/authorize", form)
	require.NoError(t, err)
	_ = res.Body.Close()
	res, err = c.hc.Get(res.Header.Get("Location"))
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)

	su, err := e.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = su.Close(context.Background()) }()
	require.NoError(t, su.QueryRow(t.Context(), `select id from cp.users where github_id = $1`, e.nextGH).Scan(&c.id))
	_, err = su.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, $3)`, e.tenant, c.id, role)
	require.NoError(t, err)
	_, err = su.Exec(t.Context(), `update cp.tenants set accepted_terms_version = $2 where id = $1`, e.tenant, termsVersion)
	require.NoError(t, err)
	return c
}

type resp struct {
	status int
	body   []byte
}

func (r resp) obj(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.body, &m), string(r.body))
	return m
}

func (r resp) code(t *testing.T) string {
	t.Helper()
	return r.obj(t)["error"].(map[string]any)["code"].(string)
}

func (c *client) call(t *testing.T, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, rd)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.e.srv.URL)
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return resp{status: res.StatusCode, body: b}
}

func (e *env) path() string { return "/api/tenants/" + e.tenant.String() + "/erasure" }

// erase submits a request and waits for its result.
func (e *env) erase(t *testing.T, c *client, body map[string]any) map[string]any {
	t.Helper()
	r := c.call(t, http.MethodPost, e.path(), body)
	require.Equal(t, http.StatusAccepted, r.status, string(r.body))
	m := r.obj(t)
	require.Equal(t, "running", m["status"])
	return e.await(t, c, m["id"].(string))
}

func (e *env) await(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	var m map[string]any
	require.Eventually(t, func() bool {
		r := c.call(t, http.MethodGet, e.path()+"/"+id, nil)
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		m = r.obj(t)
		return m["status"] != "running"
	}, 3*time.Minute, 200*time.Millisecond)
	return m
}

// visit publishes page views of one visitor the way the gateway does after
// its checks: the token from the gateway's own visitor code and today's
// secret, then a WebEvent on web.events.<site> as the gateway user.
func (e *env) visit(t *testing.T, ip, ua, band string, n int) string {
	t.Helper()
	ctx := t.Context()
	addr, err := gateway.ParseClientIP(ip)
	require.NoError(t, err)
	vt, err := e.vis.Token(ctx, e.site.Site.ID, addr, ua)
	require.NoError(t, err)
	website := e.site.Site.ID
	if band == events.BandQuarantine {
		website = e.site.Quarantine
	}
	for i := range n {
		id, err := events.NewEventID()
		require.NoError(t, err)
		ev := events.WebEvent{ID: id, SiteID: e.site.Site.ID, WebsiteID: website, Band: band, VT: vt, UA: ua,
			Type: events.TypePageview, Hostname: e.site.Domain, URL: "/page/" + strconv.Itoa(i), Title: "page",
			Screen: "1440x900", Language: "en-US", AcceptedAt: time.Now().UnixMilli()}
		b, err := ev.Marshal()
		require.NoError(t, err)
		_, err = natsx.PublishDedup(ctx, e.gwJS, natsx.StreamWebEvents, ev.Subject(), id, b, nil)
		require.NoError(t, err)
	}
	return vt
}

// drained waits until the replayer took every queued event.
func (e *env) drained(t *testing.T) {
	t.Helper()
	s, err := e.admin.Stream(t.Context(), natsx.StreamWebEvents)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		info, err := s.Info(t.Context())
		require.NoError(t, err)
		return info.State.Msgs == 0
	}, 2*time.Minute, 100*time.Millisecond)
}

func (e *env) umamiCount(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	c, err := pgx.Connect(t.Context(), fmt.Sprintf("postgres://postgres:%s@%s:%d/umami?sslmode=disable",
		url.QueryEscape(e.um.PostgresPassword), e.um.PostgresHost, e.um.PostgresPort))
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	var n int64
	require.NoError(t, c.QueryRow(t.Context(), q, args...).Scan(&n))
	return n
}

func (e *env) visitRecord(t *testing.T, vt string) *replayer.VisitRecord {
	t.Helper()
	kv, err := e.admin.KeyValue(t.Context(), natsx.BucketVisitState)
	require.NoError(t, err)
	en, err := kv.Get(t.Context(), replayer.VisitKey(e.site.Site.ID, vt))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	require.NoError(t, err)
	var r replayer.VisitRecord
	require.NoError(t, json.Unmarshal(en.Value(), &r))
	return &r
}

func (e *env) sessionsOf(t *testing.T, vt string) []string {
	t.Helper()
	r := e.visitRecord(t, vt)
	require.NotNil(t, r, "no visit record")
	var out []string
	for _, w := range r.Websites {
		out = append(out, w.SessionID)
	}
	return out
}

func (e *env) eventsOfSessions(t *testing.T, ids []string) int64 {
	t.Helper()
	return e.umamiCount(t, `select count(*) from website_event where session_id::text = any($1)`, ids)
}

func (e *env) followup(t *testing.T) erasurefollowup.Result {
	t.Helper()
	res, err := e.follow.Run(t.Context(), e.leader.Tx, e.leader.SystemTx)
	require.NoError(t, err)
	return res
}

func (e *env) aggPageviews(t *testing.T, band string, day time.Time) int64 {
	t.Helper()
	var n int64
	err := e.d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select coalesce(sum(pageviews), 0) from agg.web_daily where site_id = $1 and band = $2 and day = $3`,
			uuid.MustParse(e.site.Site.ID), band, day).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

const (
	uaA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 erasure-a"
	uaB = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15 erasure-b"
	uaC = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 erasure-c"
	uaD = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0 erasure-d"
	ipA = "198.51.100.23"
	ipB = "203.0.113.77"
	ipC = "2001:db8:5:6::9"
	ipD = "198.51.100.200"
	uaE = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 erasure-e"
	ipE = "198.51.100.61"
	uaF = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 erasure-f"
	ipF = "198.51.100.62"
)

//nolint:contextcheck // the helpers take the test and use its context, t.Context()
func TestErasure(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	site := e.site.Site.ID
	today := aggregateweb.UTCDay(time.Now())

	vtA := e.visit(t, ipA, uaA, events.BandPrimary, 3)
	e.visit(t, ipA, uaA, events.BandQuarantine, 1)
	vtB := e.visit(t, ipB, uaB, events.BandPrimary, 2)
	e.drained(t)
	sessA := e.sessionsOf(t, vtA)
	require.Len(t, sessA, 2, "visitor A has a session on each band")
	sessB := e.sessionsOf(t, vtB)
	require.Equal(t, int64(4), e.eventsOfSessions(t, sessA))
	_, err := e.agg.BackfillSite(ctx, e.leader.Tx, e.site.Site, today, today)
	require.NoError(t, err)
	require.Equal(t, int64(5), e.aggPageviews(t, "primary", today))
	require.Equal(t, int64(1), e.aggPageviews(t, "quarantine", today))

	t.Run("validation and roles", func(t *testing.T) {
		member := e.member(t, "member", e.terms)
		r := member.call(t, http.MethodPost, e.path(), map[string]any{"kind": "session", "site_id": site, "session_id": uuid.NewString()})
		require.Equal(t, http.StatusForbidden, r.status)
		require.Equal(t, "owner_required", r.code(t))
		for _, body := range []map[string]any{
			{"kind": "visitor_today", "site_id": site, "ip": "not-an-ip", "user_agent": uaA},
			{"kind": "visitor_today", "site_id": site, "ip": ipA},
			{"kind": "session", "site_id": site, "session_id": uuid.NewString(), "ip": ipA},
			{"kind": "site_reset", "site_id": site, "confirm_domain": "wrong.example"},
			{"kind": "session", "site_id": uuid.NewString(), "session_id": uuid.NewString()},
			{"kind": "forget", "site_id": site},
		} {
			r := e.owner.call(t, http.MethodPost, e.path(), body)
			require.Equal(t, http.StatusUnprocessableEntity, r.status, "%v: %s", body["kind"], r.body)
			// the refusal never quotes the address or the user agent
			require.NotContains(t, string(r.body), ipA)
			require.NotContains(t, string(r.body), uaA)
		}
	})

	t.Run("visitor today", func(t *testing.T) {
		m := e.erase(t, e.owner, map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipA, "user_agent": uaA})
		require.Equal(t, "done", m["status"], m)
		require.Equal(t, "erased", m["outcome"])
		require.Equal(t, today.Format(time.DateOnly), m["day"])
		require.EqualValues(t, 2, m["sessions_deleted"])
		require.EqualValues(t, 4, m["events_deleted"])
		require.Zero(t, e.eventsOfSessions(t, sessA))
		require.Zero(t, e.umamiCount(t, `select count(*) from session where session_id::text = any($1)`, sessA))
		require.Nil(t, e.visitRecord(t, vtA), "the visit record went with the sessions")
		// the other visitor is untouched
		require.Equal(t, int64(2), e.eventsOfSessions(t, sessB))

		// aggregates of the day are read again
		res := e.followup(t)
		require.Equal(t, 1, res.Reaggregated)
		require.Equal(t, int64(2), e.aggPageviews(t, "primary", today))
		require.Equal(t, int64(0), e.aggPageviews(t, "quarantine", today))
		r := e.owner.call(t, http.MethodGet, e.path()+"/"+m["id"].(string), nil)
		require.NotNil(t, r.obj(t)["reaggregated_at"])
	})

	t.Run("queued event and dlq", func(t *testing.T) {
		vtC := e.visit(t, ipC, uaC, events.BandPrimary, 1)
		e.drained(t)
		sessC := e.sessionsOf(t, vtC)
		// the replayer is down; one more event of C waits in WEB_EVENTS and
		// an undeliverable one sits in the dlq
		e.stopReplayer()
		e.visit(t, ipC, uaC, events.BandPrimary, 1)
		_, rpJS := e.conn(t, natsx.ServiceReplayer)
		dead := fmt.Appendf(nil, `{"id":"x","site_id":%q,"vt":%q,"ua":%q,"broken":true}`, site, vtC, uaC)
		_, err := natsx.PublishDedup(ctx, rpJS, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, site), "dead-"+randHex(4), dead, nil)
		require.NoError(t, err)
		other := fmt.Appendf(nil, `{"id":"y","site_id":%q,"vt":%q}`, site, strings.Repeat("ab", 32))
		_, err = natsx.PublishDedup(ctx, rpJS, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, site), "other-"+randHex(4), other, nil)
		require.NoError(t, err)

		r := e.owner.call(t, http.MethodPost, e.path(), map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipC, "user_agent": uaC})
		require.Equal(t, http.StatusAccepted, r.status, string(r.body))
		id := r.obj(t)["id"].(string)
		// a second request for the site waits its turn
		r2 := e.owner.call(t, http.MethodPost, e.path(), map[string]any{"kind": "session", "site_id": site, "session_id": uuid.NewString()})
		require.Equal(t, http.StatusConflict, r2.status)
		require.Equal(t, "erasure_in_progress", r2.code(t))
		time.Sleep(2 * time.Second)
		r = e.owner.call(t, http.MethodGet, e.path()+"/"+id, nil)
		require.Equal(t, "running", r.obj(t)["status"], "the run waits for the queued event")

		e.startReplayer(t)
		m := e.await(t, e.owner, id)
		require.Equal(t, "done", m["status"], m)
		require.EqualValues(t, 1, m["sessions_deleted"])
		require.EqualValues(t, 2, m["events_deleted"], "the queued event was delivered first, then erased")
		require.EqualValues(t, 1, m["dlq_messages_deleted"])
		require.Zero(t, e.eventsOfSessions(t, sessC))
		dlq, err := e.admin.Stream(ctx, natsx.StreamWebDLQ)
		require.NoError(t, err)
		info, err := dlq.Info(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), info.State.Msgs, "only the other visitor's dlq message is left")
	})

	t.Run("next day", func(t *testing.T) {
		tomorrow := &clock{t: today.AddDate(0, 0, 1).Add(5 * time.Second)}
		e.serveGateway(t, tomorrow)
		m := e.erase(t, e.owner, map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipB, "user_agent": uaB})
		require.Equal(t, "done", m["status"], m)
		require.Equal(t, "nothing_found", m["outcome"])
		require.Equal(t, today.AddDate(0, 0, 1).Format(time.DateOnly), m["day"])
		require.Contains(t, m["message"], "cannot be linked to a person")
		require.EqualValues(t, 0, m["sessions_deleted"])
		require.Nil(t, m["affected_from"])
		// B's data of the previous day stays: it can no longer be linked
		require.Equal(t, int64(2), e.eventsOfSessions(t, sessB))
		e.serveGateway(t, e.clock)
	})

	t.Run("session", func(t *testing.T) {
		m := e.erase(t, e.owner, map[string]any{"kind": "session", "site_id": site, "session_id": uuid.NewString()})
		require.Equal(t, "nothing_found", m["outcome"], m)

		vtD := e.visit(t, ipD, uaD, events.BandPrimary, 2)
		e.drained(t)
		sessD := e.sessionsOf(t, vtD)
		require.Len(t, sessD, 1)
		m = e.erase(t, e.owner, map[string]any{"kind": "session", "site_id": site, "session_id": sessD[0]})
		require.Equal(t, "erased", m["outcome"], m)
		require.Equal(t, sessD[0], m["session_id"])
		require.EqualValues(t, 1, m["sessions_deleted"])
		require.EqualValues(t, 2, m["events_deleted"])
		require.Zero(t, e.eventsOfSessions(t, sessD))
		require.Nil(t, e.visitRecord(t, vtD))
		require.Equal(t, int64(2), e.eventsOfSessions(t, sessB))
	})

	t.Run("site reset", func(t *testing.T) {
		e.visit(t, ipA, uaA, events.BandQuarantine, 2)
		e.drained(t)
		m := e.erase(t, e.owner, map[string]any{"kind": "site_reset", "site_id": site, "confirm_domain": e.site.Domain})
		require.Equal(t, "erased", m["outcome"], m)
		require.EqualValues(t, 2, m["sessions_deleted"])
		require.EqualValues(t, 4, m["events_deleted"])
		for _, w := range []string{site, e.site.Quarantine} {
			require.Zero(t, e.umamiCount(t, `select count(*) from website_event where website_id = $1`, uuid.MustParse(w)))
			require.Zero(t, e.umamiCount(t, `select count(*) from session where website_id = $1`, uuid.MustParse(w)))
		}
		e.followup(t)
		require.Zero(t, e.aggPageviews(t, "primary", today))
		require.Zero(t, e.aggPageviews(t, "quarantine", today))

		r := e.owner.call(t, http.MethodGet, e.path(), nil)
		require.Equal(t, http.StatusOK, r.status)
		items := r.obj(t)["items"].([]any)
		require.Len(t, items, 6)
		require.Equal(t, "site_reset", items[0].(map[string]any)["kind"], "newest first")
	})

	// W-3: a run that removed the visit record and then failed is finished
	// by the next request of the same day, which still finds the sessions
	t.Run("a failed visitor run is finished by the next request", func(t *testing.T) {
		vtE := e.visit(t, ipE, uaE, events.BandPrimary, 2)
		e.drained(t)
		sessE := e.sessionsOf(t, vtE)
		require.Len(t, sessE, 1)
		e.flaky.failDeletes.Store(1)
		m := e.erase(t, e.owner, map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipE, "user_agent": uaE})
		require.Equal(t, "failed", m["status"], m)
		require.Equal(t, int64(2), e.eventsOfSessions(t, sessE), "nothing was deleted yet")
		m = e.erase(t, e.owner, map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipE, "user_agent": uaE})
		require.Equal(t, "done", m["status"], m)
		require.Equal(t, "erased", m["outcome"], m)
		require.EqualValues(t, 1, m["sessions_deleted"])
		require.EqualValues(t, 2, m["events_deleted"])
		require.Zero(t, e.eventsOfSessions(t, sessE))
		require.Nil(t, e.visitRecord(t, vtE))
		kv, err := e.admin.KeyValue(ctx, natsx.BucketVisitState)
		require.NoError(t, err)
		_, err = kv.Get(ctx, "erasing."+site+"."+vtE)
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound, "the run's progress record is removed at the end")
	})

	// W-4: after the memory bucket that links visitors to sessions was
	// emptied (a nats restart), a visit of today is reported as not located,
	// never as not recorded
	t.Run("a visit whose link was lost is not located", func(t *testing.T) {
		vtF := e.visit(t, ipF, uaF, events.BandPrimary, 1)
		e.drained(t)
		sessF := e.sessionsOf(t, vtF)
		require.NoError(t, e.admin.DeleteKeyValue(ctx, natsx.BucketVisitState))
		// the replayer recreates its bucket, as after a restart
		require.Eventually(t, func() bool {
			_, err := e.admin.KeyValue(ctx, natsx.BucketVisitState)
			return err == nil
		}, 30*time.Second, 100*time.Millisecond)
		m := e.erase(t, e.owner, map[string]any{"kind": "visitor_today", "site_id": site, "ip": ipF, "user_agent": uaF})
		require.Equal(t, "done", m["status"], m)
		require.Equal(t, "nothing_found", m["outcome"], m)
		msg := m["message"].(string)
		require.Contains(t, msg, "could not be located")
		require.Contains(t, msg, "was emptied at")
		require.NotContains(t, msg, "was recorded")
		// the visit is still stored; the message says how to remove it
		require.Equal(t, int64(1), e.eventsOfSessions(t, sessF))
		require.Contains(t, msg, "site reset")
	})

	t.Run("no residue", func(t *testing.T) {
		markers := []string{ipA, ipC, ipE, ipF, uaA, uaC, uaE, uaF, "erasure-a", "erasure-c", "erasure-e", "erasure-f"}
		var dumps []string
		su, err := e.pg.Superuser(ctx, "platform")
		require.NoError(t, err)
		defer func() { _ = su.Close(context.Background()) }()
		for _, q := range []string{
			`select coalesce(string_agg(row_to_json(r)::text, ' '), '') from cp.erasure_requests r`,
			`select coalesce(string_agg(row_to_json(a)::text, ' '), '') from cp.audit_log a`,
		} {
			var s string
			require.NoError(t, su.QueryRow(ctx, q).Scan(&s))
			dumps = append(dumps, s)
		}
		require.Contains(t, dumps[0], "visitor_today")
		dumps = append(dumps, e.logs.String())
		dumps = append(dumps, e.natsDump(t))
		for _, d := range dumps {
			for _, mk := range markers {
				require.NotContains(t, d, mk)
			}
		}
		// the raw user agent never reached umami's tables either
		for _, mk := range []string{"erasure-a", "erasure-c", "erasure-e", "erasure-f"} {
			require.Zero(t, e.umamiCount(t, `select count(*) from session s where row_to_json(s)::text like '%' || $1 || '%'`, mk))
		}
	})
}

// natsDump reads every message of every stream, buckets included.
func (e *env) natsDump(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	var b strings.Builder
	for name := range e.admin.StreamNames(ctx).Name() {
		s, err := e.admin.Stream(ctx, name)
		require.NoError(t, err)
		info, err := s.Info(ctx)
		require.NoError(t, err)
		for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
			m, err := s.GetMsg(ctx, seq)
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				continue
			}
			require.NoError(t, err)
			b.WriteString(m.Subject)
			b.Write(m.Data)
			for k, v := range m.Header {
				b.WriteString(k + strings.Join(v, ","))
			}
		}
	}
	return b.String()
}
