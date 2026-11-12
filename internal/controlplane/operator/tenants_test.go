package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/db"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
)

// real postgresql with the migrations, the cp_operator and cp_system pools
func reviewDB(t *testing.T) (*pdb.DB, *dbtest.Postgres) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	acfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, acfg, db.Migrations(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "operator-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return d, pg
}

func superExec(t *testing.T, pg *dbtest.Postgres, sql string, args ...any) {
	t.Helper()
	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	_, err = c.Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

type listAnswer struct {
	Items []struct {
		ID            uuid.UUID `json:"id"`
		Name          string    `json:"name"`
		Status        string    `json:"status"`
		SiteCount     int64     `json:"site_count"`
		SitesInReview int64     `json:"sites_in_review"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func call(t *testing.T, h http.Handler, path string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func names(t *testing.T, body []byte) ([]string, *string) {
	t.Helper()
	var a listAnswer
	require.NoError(t, json.Unmarshal(body, &a), string(body))
	out := []string{}
	for _, it := range a.Items {
		out = append(out, it.Name)
	}
	return out, a.NextCursor
}

func TestOperatorTenantsListAndDetail(t *testing.T) {
	d, pg := reviewDB(t)
	h, err := NewTenants(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", h.Routes)

	ids := map[string]uuid.UUID{}
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i, tn := range []struct{ name, status string }{
		{"Bravo", "active"}, {"alpha", "pending"}, {"Charlie", "rejected"}, {"Delta", "removed"}, {"Echo", "active"},
	} {
		id := uuid.Must(uuid.NewV7())
		ids[tn.name] = id
		superExec(t, pg, `insert into cp.tenants (id, slug, name, status, enrollment_answers, created_at, status_changed_at)
			values ($1, $2, $3, $4, $5, $6, $7)`, id, fmt.Sprintf("t-%d", i), tn.name, tn.status,
			fmt.Sprintf(`{"website":"https://%s.example"}`, tn.name), base.Add(time.Duration(i)*time.Hour),
			base.Add(time.Duration(10-i)*time.Hour))
	}
	// the probe tenant is never shown
	probe := uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status, is_internal) values ($1, 'probe', 'Probe', 'active', true)`, probe)
	// Echo has a site in review and a live site with a searchable domain
	superExec(t, pg, `insert into cp.sites (id, tenant_id, name, domain, status, verify_token) values
		($1, $2, 'a', 'review.example', 'pending', 'x'), ($3, $2, 'b', 'needle-domain.example', 'active', 'y')`,
		uuid.Must(uuid.NewV7()), ids["Echo"], uuid.Must(uuid.NewV7()))

	code, body := call(t, r, "/api/operator/tenants")
	require.Equal(t, http.StatusOK, code, string(body))
	got, next := names(t, body)
	require.Equal(t, []string{"Echo", "Delta", "Charlie", "alpha", "Bravo"}, got, "created_at desc by default")
	require.Nil(t, next)

	code, body = call(t, r, "/api/operator/tenants?sort=name&order=asc&limit=2")
	require.Equal(t, http.StatusOK, code, string(body))
	got, next = names(t, body)
	require.Equal(t, []string{"alpha", "Bravo"}, got, "name sorts case-insensitively")
	require.NotNil(t, next)
	code, body = call(t, r, "/api/operator/tenants?sort=name&order=asc&limit=2&cursor="+url.QueryEscape(*next))
	require.Equal(t, http.StatusOK, code, string(body))
	got, next = names(t, body)
	require.Equal(t, []string{"Charlie", "Delta"}, got)
	// a cursor reused with another sort or order is refused
	code, body = call(t, r, "/api/operator/tenants?sort=name&order=desc&cursor="+url.QueryEscape(*next))
	require.Equal(t, http.StatusBadRequest, code, string(body))
	code, _ = call(t, r, "/api/operator/tenants?sort=created_at&order=asc&cursor="+url.QueryEscape(*next))
	require.Equal(t, http.StatusBadRequest, code)

	code, body = call(t, r, "/api/operator/tenants?sort=status_changed_at&order=asc")
	require.Equal(t, http.StatusOK, code)
	got, _ = names(t, body)
	require.Equal(t, []string{"Echo", "Delta", "Charlie", "alpha", "Bravo"}, got)

	code, body = call(t, r, "/api/operator/tenants?status=active,pending")
	require.Equal(t, http.StatusOK, code)
	got, _ = names(t, body)
	require.Equal(t, []string{"Echo", "alpha", "Bravo"}, got)

	// review: pending tenants and active tenants with a site in review
	code, body = call(t, r, "/api/operator/tenants?review=true")
	require.Equal(t, http.StatusOK, code)
	got, _ = names(t, body)
	require.Equal(t, []string{"Echo", "alpha"}, got)

	// q matches name, slug, website and site domains, case-insensitively
	for q, want := range map[string][]string{"CHAR": {"Charlie"}, "needle": {"Echo"}, "bravo.example": {"Bravo"}, "t-3": {"Delta"}} {
		code, body = call(t, r, "/api/operator/tenants?q="+url.QueryEscape(q))
		require.Equal(t, http.StatusOK, code)
		got, _ = names(t, body)
		require.Equal(t, want, got, q)
	}

	for _, bad := range []string{"status=gone", "sort=slug", "order=up", "q=", "review=false", "limit=0", "cursor=x"} {
		code, body = call(t, r, "/api/operator/tenants?"+bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal(body, &env))
		require.Contains(t, []string{httpx.CodeInvalidQuery, httpx.CodeInvalidCursor}, env.Error.Code, bad)
	}

	// detail of Echo
	code, body = call(t, r, "/api/operator/tenants/"+ids["Echo"].String())
	require.Equal(t, http.StatusOK, code, string(body))
	var det struct {
		Tenant struct {
			Name         string `json:"name"`
			Provisioning string `json:"provisioning"`
			CreatedBy    any    `json:"created_by"`
		} `json:"tenant"`
		Sites []struct {
			Domain   string `json:"domain"`
			InReview bool   `json:"in_review"`
		} `json:"sites"`
		AllowedActions []string         `json:"allowed_actions"`
		Removal        any              `json:"removal"`
		Members        []any            `json:"members"`
		StellarIDs     []any            `json:"stellar_ids"`
		Ingest         map[string]any   `json:"ingest"`
		RecentAudit    []map[string]any `json:"recent_audit"`
	}
	require.NoError(t, json.Unmarshal(body, &det))
	require.Equal(t, "Echo", det.Tenant.Name)
	require.Equal(t, "pending", det.Tenant.Provisioning)
	require.Nil(t, det.Tenant.CreatedBy)
	require.Len(t, det.Sites, 2)
	require.True(t, det.Sites[0].InReview)
	require.Equal(t, []string{"suspend", "remove"}, det.AllowedActions)
	require.Nil(t, det.Removal)
	require.NotNil(t, det.Members)
	require.Empty(t, det.Members)
	require.NotNil(t, det.StellarIDs)
	require.NotNil(t, det.RecentAudit)

	// internal, unknown and malformed ids are 404
	for _, id := range []string{probe.String(), uuid.Must(uuid.NewV7()).String(), "nope"} {
		code, _ = call(t, r, "/api/operator/tenants/"+id)
		require.Equal(t, http.StatusNotFound, code, id)
	}
}

func TestSyncFlags(t *testing.T) {
	d, pg := reviewDB(t)
	superExec(t, pg, `insert into cp.users (github_id, github_login, is_operator) values
		(1, 'stays-op', true), (2, 'loses-op', true), (3, 'gains-op', false), (4, 'never', false)`)
	n, err := SyncFlags(t.Context(), d, map[int64]bool{1: true, 3: true})
	require.NoError(t, err)
	require.Equal(t, 2, n)

	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	rows, err := c.Query(t.Context(), `select github_login from cp.users where is_operator order by github_id`)
	require.NoError(t, err)
	ops, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	require.Equal(t, []string{"stays-op", "gains-op"}, ops)

	rows, err = c.Query(t.Context(), `select a.actor, u.github_login, a.detail::text from cp.audit_log a
		join cp.users u on 'user:' || u.id::text = a.target where a.action = $1 order by u.github_id`, ActionOperatorSync)
	require.NoError(t, err)
	type row struct{ actor, login, detail string }
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.actor, &x.login, &x.detail)
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, row{ActorStartup, "loses-op", `{"is_operator": {"new": false, "old": true}}`}, got[0])
	require.Equal(t, row{ActorStartup, "gains-op", `{"is_operator": {"new": true, "old": false}}`}, got[1])

	// a second start with the same list changes nothing and audits nothing
	n, err = SyncFlags(t.Context(), d, map[int64]bool{1: true, 3: true})
	require.NoError(t, err)
	require.Zero(t, n)
}
