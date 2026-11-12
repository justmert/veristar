package apimetrics_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/controlplane"
	"github.com/justmert/stellar-analytics/internal/controlplane/apimetrics"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/db/jobsq"
	"github.com/justmert/stellar-analytics/internal/db/writerq"
	"github.com/justmert/stellar-analytics/internal/jobs/apirollup"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
)

// how the rows get into the tables: minute rows and warnings go in through
// the metrics writer's own queries (writerq.AddMinute, MarkRollupPending,
// AddIngestWarning) as the metrics_writer role, exactly the statements its
// batch transaction sends after the delta step; hourly and daily rows come
// from the real api-rollup task under a real jobs leader lock. the collector
// and the delta engine in front of AddMinute are covered by the this stage
// exactness suite (internal/metricswriter); driving them here would add a
// collector process per test without touching any code under test.

// now is the server time every test resolves ranges against. it lies after
// the migration's partition window, so every partition the data needs is
// made here, the way the partition job makes them.
var now = time.Date(2031, 6, 15, 10, 30, 20, 0, time.UTC)

var retention = config.APIRetention{MinuteDays: 30, HourlyMonths: 36}

type env struct {
	pg  *dbtest.Postgres
	d   *pdb.DB
	srv *httptest.Server
	su  *pgx.Conn
	gh  int64
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := t.Context()
	e := &env{pg: jobstest.Postgres(t), gh: 5_100_000}
	pcfg, err := pdb.LoadPoolConfig(e.pg.Config("platform"),
		pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleMetricsWriter, pdb.RoleJobs)
	require.NoError(t, err)
	e.d, err = pdb.Open(ctx, pcfg, "apimetrics-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)
	e.su, err = e.pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.su.Close(context.Background()) })
	// the independent sql of the tests buckets days in utc
	_, err = e.su.Exec(ctx, `set timezone = 'UTC'`)
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)
	// the api metrics routes are read-only and never reconcile; a call
	// would be a bug, so it fails loudly
	scope, err := tenants.NewScope(e.d, "test-terms", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return errors.New("api metrics routes must not reconcile")
	}), log)
	require.NoError(t, err)
	t.Cleanup(scope.Wait)
	routes, err := apimetrics.NewRoutes(scope, apimetrics.Resolver{Now: func() time.Time { return now }, Retention: retention})
	require.NoError(t, err)

	sessions := session.New(e.d)
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	// sign-in without the oauth fixture: the session is the real one, only
	// the github round trip is skipped, because these tests are about what a
	// signed-in member reads
	signIn := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			id, err := uuid.Parse(r.URL.Query().Get("user"))
			if err != nil {
				return httpx.NotFound()
			}
			if err := sessions.SignIn(r.Context(), id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public:  []controlplane.Routes{signIn},
		Private: []controlplane.Routes{routes},
	})
	return e
}

func (e *env) sys(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), fn))
}

// member makes a user and a tenant with that user as owner
func (e *env) member(t *testing.T, status string) (user, tenant uuid.UUID) {
	t.Helper()
	e.gh++
	e.sys(t, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			e.gh, "u"+strconv.FormatInt(e.gh, 10)).Scan(&user); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(),
			`insert into cp.tenants (slug, name, status, created_by) values ($1, 'Project', $2, $3) returning id`,
			"p-"+uuid.NewString()[:8], status, user).Scan(&tenant); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, tenant, user)
		return err
	})
	return user, tenant
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) client(t *testing.T, user uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return &client{e: e, hc: &hc}
}

// get answers status and raw body of a GET under /api
func (c *client) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.e.srv.URL+"/api"+path, nil)
	require.NoError(t, err)
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, b
}

// ok GETs path, requires 200 and decodes into v
func (c *client) ok(t *testing.T, path string, v any) {
	t.Helper()
	st, b := c.get(t, path)
	require.Equal(t, http.StatusOK, st, "%s: %s", path, b)
	require.NoError(t, json.Unmarshal(b, v), string(b))
}

// errCode GETs path and returns status and error code
func (c *client) errCode(t *testing.T, path string) (int, string) {
	t.Helper()
	st, b := c.get(t, path)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &env), string(b))
	return st, env.Error.Code
}

// h builds a canonical bucket array from bucket index to count
func h(m map[int]int64) [15]int64 {
	var c [15]int64
	for i, v := range m {
		c[i] = v
	}
	return c
}

// point is one delta histogram as the writer stores it
type point struct {
	tenant  uuid.UUID
	at      time.Time
	service string
	method  string
	route   string
	status  int16
	source  string
	counts  [15]int64
	lossy   bool
}

type warning struct {
	tenant uuid.UUID
	day    time.Time
	kind   string
	count  int64
}

// write creates the partitions the points need, writes them and the
// warnings through the writer's queries as metrics_writer, then runs the
// real rollup so hourly and daily rows exist.
func (e *env) write(t *testing.T, points []point, warnings []warning) {
	t.Helper()
	ctx := t.Context()
	days, months := map[time.Time]bool{}, map[time.Time]bool{}
	for _, p := range points {
		u := p.at.UTC()
		days[time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)] = true
		months[time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)] = true
	}
	require.NoError(t, e.d.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		for d := range days {
			if _, err := q.CreateMinutePartition(ctx, d); err != nil {
				return err
			}
		}
		for m := range months {
			if _, err := q.CreateHourlyPartition(ctx, m); err != nil {
				return err
			}
		}
		return nil
	}))

	require.NoError(t, e.d.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		q := writerq.New(tx)
		var mins []writerq.AddMinuteParams
		var marks []writerq.MarkRollupPendingParams
		for _, p := range points {
			var total int64
			for _, v := range p.counts {
				total += v
			}
			mins = append(mins, writerq.AddMinuteParams{
				TenantID: p.tenant, BucketStart: p.at, Service: p.service, Method: p.method, Route: p.route,
				StatusCode: p.status, Source: p.source, Counts: p.counts[:], Sum: float64(total) * 0.01,
				Count: total, Lossy: p.lossy,
			})
			marks = append(marks, writerq.MarkRollupPendingParams{TenantID: p.tenant, BucketStart: p.at})
		}
		var berr error
		q.AddMinute(ctx, mins).Exec(func(_ int, err error) { berr = errors.Join(berr, err) })
		q.MarkRollupPending(ctx, marks).Exec(func(_ int, err error) { berr = errors.Join(berr, err) })
		if berr != nil {
			return berr
		}
		for _, w := range warnings {
			if err := q.AddIngestWarning(ctx, writerq.AddIngestWarningParams{
				TenantID: w.tenant, Day: w.day, Kind: w.kind, Count: w.count,
			}); err != nil {
				return err
			}
		}
		return nil
	}))

	srv := jobstest.NATS(t)
	leader, _ := jobstest.Leader(t, srv, e.d, "apimetrics-test")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	res, err := r.Run(ctx, leader.Tx)
	require.NoError(t, err)
	require.Positive(t, res.HourlyRows)
	require.Positive(t, res.DailyRows)
}
