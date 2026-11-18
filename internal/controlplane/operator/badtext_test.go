package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/umami"
)

// noUmami is a WebReader for routes that never read umami.
type noUmami struct{}

func (noUmami) GetWebsite(context.Context, string) (umami.Website, bool, error) {
	return umami.Website{}, false, nil
}

func (noUmami) Stats(context.Context, string, umami.Range) (umami.Stats, error) {
	return umami.Stats{}, nil
}

// a query value or cursor value postgresql cannot hold as text (a nul
// character, invalid utf-8) or as its column type (a uuid in urn form, year
// 0) is the client's mistake: 400, never a 500 from the database.
func TestValuesPostgresqlRefusesAre400(t *testing.T) {
	d, pg := reviewDB(t)
	opID := uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, github_id, github_login, is_operator) values ($1, 7, 'op', true)`, opID)
	k, err := GenerateKey()
	require.NoError(t, err)
	keyID := uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.operator_api_keys (id, name, prefix, hash, scope, expires_at, created_by)
		values ($1, 'k', $2, $3, 'global', now() + interval '1 day', $4)`, keyID, k.Display, k.Hash[:], opID)

	log := slog.New(slog.DiscardHandler)
	machine, err := NewMachine(MachineConfig{DB: d, Umami: noUmami{}, Log: log, Rate: 1000, Burst: 1000})
	require.NoError(t, err)
	g, err := NewGlobal(d)
	require.NoError(t, err)
	a, err := NewAuditLog(d)
	require.NoError(t, err)
	tn, err := NewTenants(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(httpx.Base(log))
	r.Route("/api/operator", func(o chi.Router) {
		g.Routes(o)
		a.Routes(o)
		tn.Routes(o)
	})
	r.Mount(MachinePath, machine.Handler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	get := func(t *testing.T, path string, q url.Values, key bool) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path+"?"+q.Encode(), nil)
		require.NoError(t, err)
		if key {
			req.Header.Set("Authorization", "Bearer "+k.Plain)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&env)
		return res.StatusCode, env.Error.Code
	}

	t.Run("operator query values", func(t *testing.T) {
		for _, c := range []struct {
			path string
			q    url.Values
		}{
			{"/api/operator/web/projects", url.Values{"q": {"a\x00b"}}},
			{"/api/operator/web/projects", url.Values{"q": {"a\xffb"}}},
			{"/api/operator/api/projects", url.Values{"program": {"x\x00"}}},
			{"/api/operator/api/projects", url.Values{"round": {"\xc3"}}},
			{"/api/operator/tenants", url.Values{"q": {"\x00"}}},
			{"/api/operator/audit", url.Values{"actor": {"user:\x00"}}},
			{"/api/operator/audit", url.Values{"target": {"\x00"}}},
			{"/api/operator/audit", url.Values{"action": {"tenant.\x00"}}},
		} {
			status, code := get(t, c.path, c.q, false)
			require.Equal(t, http.StatusBadRequest, status, "%s %q", c.path, c.q.Encode())
			require.Equal(t, httpx.CodeInvalidQuery, code, "%s %q", c.path, c.q.Encode())
		}
		// the same routes answer a clean value
		status, _ := get(t, "/api/operator/tenants", url.Values{"q": {"ab"}}, false)
		require.Equal(t, http.StatusOK, status)
	})

	t.Run("operator tenant cursor", func(t *testing.T) {
		b, err := json.Marshal(tenantCursor{R: tenantsRoute, S: "created_at", O: "desc", K: "2026\x00", I: uuid.New()})
		require.NoError(t, err)
		status, code := get(t, "/api/operator/tenants", url.Values{"cursor": {base64.RawURLEncoding.EncodeToString(b)}}, false)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, httpx.CodeInvalidCursor, code)
	})

	t.Run("machine cursors a key holder edited", func(t *testing.T) {
		mk := machineKey{ID: keyID}
		now := time.Now()
		day := utcDay(now).Format(time.DateOnly)
		hour := now.UTC().Truncate(time.Hour).Format(hourLayout)
		for _, c := range []struct {
			name, path, route string
			k                 []string
			from, to          string
			want              int
		}{
			{"urn uuid", "/tenants", "machine-tenants", []string{"urn:uuid:" + uuid.NewString()}, "", "", 400},
			{"nul in text", "/onchain/ids", "machine-onchain-ids", []string{uuid.NewString(), "G\x00"}, "", "", 400},
			{"year 0 date", "/web/daily", "machine-web-daily",
				[]string{uuid.NewString(), "0000-01-01", "primary"}, day, day, 400},
			{"year 0 timestamp", "/api/daily", "machine-api-daily",
				[]string{uuid.NewString(), "0000-01-01T00:00:00Z"}, day, day, 400},
			// an offset postgresql does not take is the same instant in utc
			{"offset postgresql refuses", "/api/hourly", "machine-api-hourly",
				[]string{uuid.NewString(), "2026-01-01T00:00:00+23:00"}, hour, hour, 200},
		} {
			p, err := parseMachinePage(url.Values{}, c.route, mk)
			require.NoError(t, err)
			cur := *p.next(c.k, c.from, c.to)
			status, code := get(t, MachinePath+c.path, url.Values{"cursor": {cur}}, true)
			require.Equal(t, c.want, status, c.name)
			if c.want == http.StatusBadRequest {
				require.Equal(t, httpx.CodeInvalidCursor, code, c.name)
			}
		}
	})
}
