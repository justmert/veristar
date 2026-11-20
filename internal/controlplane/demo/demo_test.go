package demo

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// stub registers every allowlisted pattern but /me (the demo's own) as a
// GET that reports what the request carries, plus routes the demo must
// refuse: writes on allowlisted paths and reads that are not allowlisted.
type stub struct{ operator bool }

func (s stub) Routes(r chi.Router) {
	for _, p := range Allowlist {
		inOperator := strings.HasPrefix(p, OperatorPrefix+"/")
		if p == "/me" || inOperator != s.operator {
			continue
		}
		p = strings.TrimPrefix(p, OperatorPrefix)
		r.Get(p, report)
		r.Post(p, report)
	}
	if s.operator {
		r.Get("/audit", report)
		r.Get("/web/projects.csv", report)
		return
	}
	r.Get("/tenants/{tenant_id}/members", report)
	r.Delete("/tenants/{tenant_id}", report)
}

func report(w http.ResponseWriter, r *http.Request) {
	u, _ := session.CurrentUser(r.Context())
	_ = json.NewEncoder(w).Encode(map[string]any{
		"demo": session.Demo(r.Context()), "db_demo": db.InDemo(r.Context()), "user": u.ID.String(),
		"operator": u.IsOperator, "tenant": chi.URLParam(r, "tenant_id"),
	})
}

func newHandler(t *testing.T, limits *auth.ClientLimits, now func() time.Time) *Handler {
	t.Helper()
	h, err := New(Config{
		Log: slog.New(slog.DiscardHandler), DB: &db.DB{}, Now: now, Limits: limits,
		Project: []Routes{stub{}}, Operator: []Routes{stub{operator: true}},
	})
	require.NoError(t, err)
	return h
}

var unlimited = &auth.ClientLimits{ClientRate: 1e6, ClientBurst: 1e6, GlobalRate: 1e6, GlobalBurst: 1e6, ReserveRate: 1, ReserveBurst: 1}

func serve(h http.Handler, method, path string, addr string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if addr != "" {
		r.Header.Set(auth.ClientAddrHeader, addr)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func code(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	b, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &e), string(b))
	return e.Error.Code
}

func TestAllowlistedReadRunsAsTheDemoViewer(t *testing.T) {
	h := newHandler(t, unlimited, time.Now)
	for _, p := range []string{"/tenants/abc", "/operator/rules/r1/matches", "/tenants/abc/web/summary"} {
		w := serve(h, http.MethodGet, Path+p, "")
		require.Equal(t, http.StatusOK, w.Code, p)
		require.Empty(t, w.Header().Values("Set-Cookie"), p)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var got map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		require.Equal(t, true, got["demo"], p)
		require.Equal(t, true, got["db_demo"], p)
		require.Equal(t, session.DemoUserID.String(), got["user"], p)
		// the viewer is never an operator to the handlers it reaches
		require.Equal(t, false, got["operator"], p)
	}
	w := serve(h, http.MethodGet, Path+"/tenants/t-1", "")
	var got map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "t-1", got["tenant"], "the handler reads its own path parameters")
}

func TestEverythingElseIsRefused(t *testing.T) {
	h := newHandler(t, unlimited, time.Now)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/tenants/abc"},
		{http.MethodPost, "/operator/rules"},
		{http.MethodDelete, "/tenants/abc"},
		{http.MethodPut, "/tenants/abc"},
		{http.MethodPatch, "/tenants/abc"},
		{http.MethodHead, "/tenants/abc"},
		{http.MethodOptions, "/tenants/abc"},
		{http.MethodGet, "/tenants/abc/members"},
		{http.MethodGet, "/operator/audit"},
		{http.MethodGet, "/operator/web/projects.csv"},
		{http.MethodGet, "/nothing"},
		{http.MethodGet, ""},
		{http.MethodGet, "/operator"},
	} {
		w := serve(h, r.method, Path+r.path, "")
		require.Equal(t, http.StatusForbidden, w.Code, "%s %s", r.method, r.path)
		require.Equal(t, "demo_read_only", code(t, w), "%s %s", r.method, r.path)
		require.Empty(t, w.Header().Values("Set-Cookie"))
	}
}

func TestNewRefusesAnAllowlistOfMissingRoutes(t *testing.T) {
	_, err := New(Config{Log: slog.New(slog.DiscardHandler), DB: &db.DB{}, Now: time.Now, Project: []Routes{stub{}}})
	require.ErrorContains(t, err, "/operator/")
}

func TestPatternsListEveryRegisteredRoute(t *testing.T) {
	h := newHandler(t, unlimited, time.Now)
	ps := h.Patterns()
	require.Contains(t, ps, "GET /me")
	require.Contains(t, ps, "POST /tenants/{tenant_id}")
	require.Contains(t, ps, "GET /operator/audit")
	for _, p := range Allowlist {
		require.True(t, h.Allowed(p), p)
	}
	require.False(t, h.Allowed("/operator/audit"))
}

// a client may burst DefaultLimits.ClientBurst requests and is then held
// to the rate; another client keeps its own bucket
func TestDefaultLimits(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	h := newHandler(t, nil, func() time.Time { return now })
	burst := int(DefaultLimits.ClientBurst)
	for i := range burst {
		w := serve(h, http.MethodGet, Path+"/tenants", "203.0.113.7")
		require.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}
	w := serve(h, http.MethodGet, Path+"/tenants", "203.0.113.7")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "rate_limited", code(t, w))
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	// refused requests are limited too, so writes cannot be used to probe
	w = serve(h, http.MethodPost, Path+"/tenants", "203.0.113.7")
	require.Equal(t, http.StatusTooManyRequests, w.Code)

	w = serve(h, http.MethodGet, Path+"/tenants", "198.51.100.9")
	require.Equal(t, http.StatusOK, w.Code, "another client has its own bucket")

	now = now.Add(time.Second)
	w = serve(h, http.MethodGet, Path+"/tenants", "203.0.113.7")
	require.Equal(t, http.StatusOK, w.Code, "the bucket refills at the client rate")
}
