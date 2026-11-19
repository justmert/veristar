package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
)

// a cohort proposal is a mutating tenant route: while the project has not
// accepted the current terms it answers 403 terms_not_accepted, as every
// such route does (contract 4.2.6); reading the cohort keeps working.
func TestCohortProposalNeedsTheCurrentTerms(t *testing.T) {
	_, pg := reviewDB(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "operator-test-tenant")
	require.NoError(t, err)
	t.Cleanup(d.Close)

	owner, tenant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, github_id, github_login) values ($1, 11, 'owner')`, owner)
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status, created_by, accepted_terms_version)
		values ($1, 'terms', 'terms', 'active', $2, '2026-01-01')`, tenant, owner)
	superExec(t, pg, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, tenant, owner)

	scope, err := tenants.NewScope(d, "2026-11-19.1", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return nil
	}), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	h, err := NewTenantCohort(scope, time.Now)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: owner})))
		})
	})
	r.Route("/api", h.Routes)
	do := func(method string, body any) (int, string) {
		var rd *bytes.Reader
		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, "/api/tenants/"+tenant.String()+"/cohort", rd)
		if method == http.MethodPut {
			req = httptest.NewRequest(method, "/api/tenants/"+tenant.String()+"/cohort/proposal", rd)
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Error.Code
	}

	code, ecode := do(http.MethodPut, map[string]any{"program": "Build Award"})
	require.Equal(t, http.StatusForbidden, code)
	require.Equal(t, httpx.CodeTermsNotAccepted, ecode)
	code, _ = do(http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)

	superExec(t, pg, `update cp.tenants set accepted_terms_version = '2026-11-19.1' where id = $1`, tenant)
	code, _ = do(http.MethodPut, map[string]any{"program": "Build Award"})
	require.Equal(t, http.StatusOK, code)
}

// an operator dismisses the proposal while the project writes a new one:
// the project's audit row names the proposal its write replaced, not the
// one it read before the operator's change committed.
func TestCohortProposalAuditsWhatItReplaced(t *testing.T) {
	_, pg := reviewDB(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "operator-test-tenant")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	owner, tenant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, github_id, github_login) values ($1, 11, 'owner')`, owner)
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status, created_by, accepted_terms_version)
		values ($1, 'race', 'race', 'active', $2, 't')`, tenant, owner)
	superExec(t, pg, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, tenant, owner)
	superExec(t, pg, `insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_by, proposed_at, proposed_source)
		values ($1, 'First', $2, now(), 'member')`, tenant, owner)
	scope, err := tenants.NewScope(d, "t", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error { return nil }),
		slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	h, err := NewTenantCohort(scope, time.Now)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: owner})))
		})
	})
	r.Route("/api", h.Routes)

	// the operator's dismiss, written and not yet committed
	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	tx, err := c.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `update cp.tenant_cohorts set proposed_program = null, proposed_by = null,
		proposed_at = null, proposed_source = null where tenant_id = $1`, tenant)
	require.NoError(t, err)

	done := make(chan int, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"program": "Second"})
		req := httptest.NewRequest(http.MethodPut, "/api/tenants/"+tenant.String()+"/cohort/proposal", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		done <- w.Code
	}()
	mon, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = mon.Close(context.Background()) }()
	require.Eventually(t, func() bool {
		var n int
		err := mon.QueryRow(t.Context(), `select count(*) from pg_stat_activity
			where wait_event_type = 'Lock' and pid <> $1`, tx.Conn().PgConn().PID()).Scan(&n)
		return err == nil && n > 0
	}, 20*time.Second, 20*time.Millisecond)
	require.NoError(t, tx.Commit(t.Context()))
	require.Equal(t, http.StatusOK, <-done)

	var detail map[string]any
	require.NoError(t, mon.QueryRow(t.Context(), `select detail from cp.audit_log where action = $1`, ActionCohortPropose).Scan(&detail))
	change := detail["proposal"].(map[string]any)
	require.Nil(t, change["old"], "the dismissed proposal was what the write replaced")
}
