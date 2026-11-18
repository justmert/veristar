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

	scope, err := tenants.NewScope(d, "2026-11-18-draft.6", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
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

	superExec(t, pg, `update cp.tenants set accepted_terms_version = '2026-11-18-draft.6' where id = $1`, tenant)
	code, _ = do(http.MethodPut, map[string]any{"program": "Build Award"})
	require.Equal(t, http.StatusOK, code)
}
