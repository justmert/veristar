package reports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pelletier/go-toml/v2"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/canonjson"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/reports"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/aggregateweb"
	"github.com/justmert/veristar/internal/jobs/integrity"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

type env struct {
	d       *pdb.DB
	srv     *httptest.Server
	key     *keypair.Full
	retired string
	routes  *reports.Routes
	ghID    int64
	now     time.Time
}

func start(t *testing.T) *env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleJobs)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "reports-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	log := slog.New(slog.DiscardHandler)
	scope, err := tenants.NewScope(d, "test-terms", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return errors.New("report routes must not reconcile")
	}), log)
	require.NoError(t, err)
	e := &env{d: d, key: keypair.MustRandom(), retired: keypair.MustRandom().Address(), ghID: 9_500_000, now: time.Now()}
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	signing, err := config.LoadReportSigning(config.FromMap(map[string]string{
		"REPORT_SIGNING_SEED": e.key.Seed(), "REPORT_SIGNING_KEY": e.key.Address(), "REPORT_SIGNING_RETIRED_KEYS": e.retired,
	}))
	require.NoError(t, err)
	e.routes, err = reports.New(reports.Config{Scope: scope, DB: d, Signing: signing, ConsoleOrigin: e.srv.URL,
		Platform: "test", Now: func() time.Time { return e.now }})
	require.NoError(t, err)
	cohort, err := operator.NewTenantCohort(scope, time.Now)
	require.NoError(t, err)
	sessions := session.New(d)
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
		Public:  []controlplane.Routes{signIn, controlplane.RoutesFunc(e.routes.Public)},
		Private: []controlplane.Routes{controlplane.RoutesFunc(e.routes.Private), cohort},
		Mounts: []controlplane.Mount{{Path: "/.well-known", Handler: func() http.Handler {
			r := chi.NewRouter()
			e.routes.WellKnown(r)
			return r
		}()}},
	})
	return e
}

func (e *env) sys(t *testing.T, sql string, args ...any) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), sql, args...)
		return err
	}))
}

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	e.ghID++
	var id uuid.UUID
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			e.ghID, "u"+strconv.FormatInt(e.ghID, 10)).Scan(&id)
	}))
	return id
}

// tenant enrolls an active project with owner and one site holding five
// primary visitors on each of the ten days before today.
func (e *env) tenant(t *testing.T, owner uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `insert into cp.tenants (slug, name, status, created_by, accepted_terms_version)
			values ($1, $1, 'active', $2, 'test-terms') returning id`, slug, owner).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, id, owner)
		return err
	}))
	site := uuid.New()
	e.sys(t, `insert into cp.sites (id, tenant_id, name, domain, verify_token) values ($1, $2, $3, $3, 'x')`, site, id, slug+".example")
	require.NoError(t, e.d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		today := time.Now().UTC().Truncate(24 * time.Hour)
		for i := 1; i <= 10; i++ {
			if _, err := tx.Exec(t.Context(), `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
				values ($1, $2, $3, 'primary', 9, 5, 6, 1, 30)`, site, id, today.AddDate(0, 0, -i)); err != nil {
				return err
			}
		}
		return nil
	}))
	return id
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) client(t *testing.T, user *uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	if user != nil {
		res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
	}
	return &client{e: e, hc: &hc}
}

func (c *client) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, c.e.srv.URL+path, rd)
	require.NoError(t, err)
	req.Header.Set("X-Requested-With", "sa-console")
	req.Header.Set("Origin", c.e.srv.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, raw
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(b, &v), string(b))
	return v
}

func (e *env) auditCount(t *testing.T, action, target string) int {
	t.Helper()
	var n int
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from cp.audit_log where action = $1 and target = $2`, action, target).Scan(&n)
	}))
	return n
}

func TestUsageReports(t *testing.T) {
	e := start(t)
	ua, ub, member := e.user(t), e.user(t), e.user(t)
	a, b := e.tenant(t, ua, "alpha"), e.tenant(t, ub, "beta")
	e.sys(t, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, a, member)
	ca, cb, anon := e.client(t, &ua), e.client(t, &ub), e.client(t, nil)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from, to := today.AddDate(0, 0, -10).Format(time.DateOnly), today.AddDate(0, 0, -reports.FinalDays).Format(time.DateOnly)

	code, raw := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", map[string]string{"from": from, "to": to})
	require.Equal(t, http.StatusCreated, code, string(raw))
	created := decode[reports.Detail](t, raw)
	id := created.ID
	require.Regexp(t, `^[a-z2-7]{26}$`, id)
	require.Equal(t, e.srv.URL+"/r/"+id, created.URL)

	t.Run("the file is canonical, signed and holds the aggregates", func(t *testing.T) {
		file, err := json.Marshal(created.File)
		require.NoError(t, err)
		v, err := reports.Verify(file)
		require.NoError(t, err)
		require.Equal(t, e.key.Address(), v.Signer)
		canon, err := canonjson.Canonicalize(created.File.Report)
		require.NoError(t, err)
		require.Equal(t, string(canon), string(created.File.Report), "the stored body is its own canonical form")
		rep := decode[reports.Report](t, created.File.Report)
		require.Equal(t, int64(35), rep.Web.Visitors, "seven days of five primary visitors")
		require.Equal(t, int64(63), rep.Web.Pageviews)
		require.Equal(t, "0.166667", *rep.Web.BounceRate)
		require.Equal(t, 7, rep.Period.Days)
		require.Equal(t, "alpha", rep.Project.Slug)
		require.Equal(t, e.key.Address(), rep.Issuer.SigningKey)
		require.Equal(t, e.srv.URL+reports.StellarTOMLPath, rep.Issuer.StellarTOML)
		require.Equal(t, reports.FormatVersion, rep.Methods.Format)
		require.Len(t, rep.Web.Series, 7)
		require.NotNil(t, rep.Cohort.KPIs["visitors"].Percentile)

		// any change of a signed byte fails
		tampered := bytes.Replace(file, []byte(`"visitors":35`), []byte(`"visitors":36`), 1)
		require.NotEqual(t, file, tampered)
		_, err = reports.Verify(tampered)
		require.Error(t, err)
		// so does a signature by another key that names itself
		other := keypair.MustRandom()
		sig, body := created.File.Signature, created.File.Report
		sig.Signer = other.Address()
		forged, err := json.Marshal(reports.File{Report: body, Signature: sig})
		require.NoError(t, err)
		_, err = reports.Verify(forged)
		require.Error(t, err)
		require.Equal(t, 1, e.auditCount(t, reports.ActionCreate, reports.Target(id)))
	})

	t.Run("the public link shows that report alone, without an account", func(t *testing.T) {
		code, raw := anon.call(t, http.MethodGet, "/api/public/reports/"+id, nil)
		require.Equal(t, http.StatusOK, code, string(raw))
		pub := decode[reports.PublicReport](t, raw)
		require.Equal(t, string(created.File.Report), string(pub.File.Report))
		require.Equal(t, e.key.Address(), pub.Keys.Current)
		require.Equal(t, []string{e.retired}, pub.Keys.Retired)
		require.Equal(t, 1, e.auditCount(t, reports.ActionView, reports.Target(id)))
		for _, p := range []string{"zzzzzzzzzzzzzzzzzzzzzzzzzz", "short", a.String(), id + "a"} {
			code, _ := anon.call(t, http.MethodGet, "/api/public/reports/"+p, nil)
			require.Equal(t, http.StatusNotFound, code, p)
		}
		code, raw = ca.call(t, http.MethodGet, "/api/tenants/"+a.String()+"/reports", nil)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, int64(1), decode[reports.ReportList](t, raw).Items[0].ViewCount)
	})

	t.Run("another project's member sees nothing of it", func(t *testing.T) {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/tenants/" + a.String() + "/reports"},
			{http.MethodGet, "/api/tenants/" + a.String() + "/reports/" + id},
			{http.MethodPost, "/api/tenants/" + a.String() + "/reports/" + id + "/revoke"},
			{http.MethodGet, "/api/tenants/" + b.String() + "/reports/" + id},
			{http.MethodPost, "/api/tenants/" + b.String() + "/reports/" + id + "/revoke"},
		} {
			code, raw := cb.call(t, c.method, c.path, nil)
			require.Equal(t, http.StatusNotFound, code, "%s %s: %s", c.method, c.path, raw)
		}
		code, raw := cb.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", map[string]string{"from": from, "to": to})
		require.Equal(t, http.StatusNotFound, code, string(raw))
		code, raw = cb.call(t, http.MethodGet, "/api/tenants/"+b.String()+"/reports", nil)
		require.Equal(t, http.StatusOK, code)
		require.Empty(t, decode[reports.ReportList](t, raw).Items)
		// and an anonymous client reaches no member route
		code, _ = anon.call(t, http.MethodGet, "/api/tenants/"+a.String()+"/reports", nil)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("periods end on a final day", func(t *testing.T) {
		// today and the three days before it are read again by the daily
		// aggregation and grading, so none of them may be signed
		for i := range reports.FinalDays {
			end := today.AddDate(0, 0, -i).Format(time.DateOnly)
			code, raw := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", map[string]string{"from": from, "to": end})
			require.Equal(t, http.StatusUnprocessableEntity, code, end)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, reports.CodePeriodNotFinal, body.Error.Code, string(raw))
		}
		require.Equal(t, aggregateweb.RereadDays+1, reports.FinalDays)
		require.Equal(t, integrity.RereadDays+1, reports.FinalDays)
	})

	t.Run("periods are closed days of at most 366", func(t *testing.T) {
		for _, p := range []map[string]string{
			{"from": to, "to": from},
			{"from": today.AddDate(-2, 0, 0).Format(time.DateOnly), "to": to},
			{"from": "2026-13-01", "to": to},
			{"to": to},
		} {
			code, raw := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", p)
			require.Equal(t, http.StatusUnprocessableEntity, code, string(raw))
		}
	})

	t.Run("a revoked link answers 404 and stays revoked", func(t *testing.T) {
		e.sys(t, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, b, ua)
		code, raw := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports/"+id+"/revoke", nil)
		require.Equal(t, http.StatusOK, code, string(raw))
		require.NotNil(t, decode[reports.Summary](t, raw).RevokedAt)
		code, _ = anon.call(t, http.MethodGet, "/api/public/reports/"+id, nil)
		require.Equal(t, http.StatusNotFound, code)
		code, _ = ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports/"+id+"/revoke", nil)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, 1, e.auditCount(t, reports.ActionRevoke, reports.Target(id)))
		// the member still reads its own revoked report
		code, _ = ca.call(t, http.MethodGet, "/api/tenants/"+a.String()+"/reports/"+id, nil)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("the stored report is immutable for every role", func(t *testing.T) {
		err := e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `update cp.usage_reports set body = '{}' where id = $1`, id)
			return err
		})
		require.Error(t, err)
		err = e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `update cp.usage_reports set revoked_at = null where id = $1`, id)
			return err
		})
		require.Error(t, err)
	})

	t.Run("stellar.toml lists the current and retired keys", func(t *testing.T) {
		res, err := anon.hc.Get(e.srv.URL + reports.StellarTOMLPath)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
		require.True(t, strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		var doc struct {
			Version  string   `toml:"VERSION"`
			Accounts []string `toml:"ACCOUNTS"`
		}
		require.NoError(t, toml.Unmarshal(body, &doc))
		require.Equal(t, []string{e.key.Address(), e.retired}, doc.Accounts)
		require.Equal(t, "2.7.0", doc.Version)
	})

	t.Run("a member proposes the cohort, an owner only", func(t *testing.T) {
		cm := e.client(t, &member)
		body := map[string]any{"program": "Build Award", "round": "SCF #38", "award_date": "2026-10-01"}
		code, _ := cm.call(t, http.MethodPut, "/api/tenants/"+a.String()+"/cohort/proposal", body)
		require.Equal(t, http.StatusForbidden, code)
		code, raw := ca.call(t, http.MethodPut, "/api/tenants/"+a.String()+"/cohort/proposal", body)
		require.Equal(t, http.StatusOK, code, string(raw))
		rec := decode[operator.CohortRecord](t, raw)
		require.Equal(t, "SCF #38", *rec.Proposal.Round)
		require.Nil(t, rec.Confirmed)
		code, raw = cm.call(t, http.MethodGet, "/api/tenants/"+a.String()+"/cohort", nil)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, "Build Award", *decode[operator.CohortRecord](t, raw).Proposal.Program)
		code, _ = cb.call(t, http.MethodGet, "/api/tenants/"+a.String()+"/cohort", nil)
		require.Equal(t, http.StatusNotFound, code)
		code, _ = ca.call(t, http.MethodPut, "/api/tenants/"+a.String()+"/cohort/proposal", map[string]any{})
		require.Equal(t, http.StatusUnprocessableEntity, code)
		require.Equal(t, 1, e.auditCount(t, operator.ActionCohortPropose, "tenant:"+a.String()))
	})

	t.Run("creation is limited per project", func(t *testing.T) {
		for i := 1; i < reports.CreateLimit; i++ {
			code, raw := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", map[string]string{"from": to, "to": to})
			require.Equal(t, http.StatusCreated, code, string(raw))
		}
		code, _ := ca.call(t, http.MethodPost, "/api/tenants/"+a.String()+"/reports", map[string]string{"from": to, "to": to})
		require.Equal(t, http.StatusTooManyRequests, code)
		// another project keeps its own budget
		code, raw := cb.call(t, http.MethodPost, "/api/tenants/"+b.String()+"/reports", map[string]string{"from": to, "to": to})
		require.Equal(t, http.StatusCreated, code, string(raw))
	})
}
