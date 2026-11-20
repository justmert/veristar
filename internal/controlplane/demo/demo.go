// Package demo serves the public demo of the console
// (docs/reference/control-plane-api.md section 21): an anonymous visitor
// reads the projects an operator marked as demo (cp.tenants.demo), and the
// operator views over those projects only, without signing in.
//
// the demo is not a second implementation of the console's reads. every
// route it serves is one of the console's own handlers, registered here a
// second time, and a request reaches it only when
//
//   - its method is GET and its route pattern is on Allowlist; anything
//     else answers 403 demo_read_only before any handler or database work;
//   - the per-client limit lets it through;
//
// and then it runs with the demo viewer as its user (session.WithDemo) in a
// demo context (db.WithDemo). from there the console's own checks apply:
// the tenant scope lets the viewer read live demo projects only and answers
// 404 for every other id, every transaction is read only, a tenant
// transaction checks the demo flag again under row-level security, and the
// operator views run as the database role cp_demo, whose policies keep demo
// projects only (migration 00053).
//
// no session is loaded or written: the handler is mounted beside /api, so
// the session middleware never runs, and it sets no cookie.
package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// Path is where the demo is mounted, beside the console's /api routes it
// mirrors: /api/demo/tenants/{tenant_id} is the demo's
// /api/tenants/{tenant_id}.
const Path = "/api/demo"

// OperatorPrefix is the path of the operator views inside the demo.
const OperatorPrefix = "/operator"

// Allowlist is every route pattern the demo serves, all GET. a pattern here
// must match a route of the registered groups, or New fails, so the list
// cannot name a route that does not exist. what is left out stays out on
// purpose: every write; members, invites, otlp tokens, erasure, site
// verification and mcp keys; the account, step-up and passkeys; every csv
// export; and the operator alerts, audit log, roster, settings and access
// keys, which hold real people or cannot be limited to demo projects.
var Allowlist = []string{
	// the demo viewer and its projects
	"/me",
	"/tenants",
	"/tenants/{tenant_id}",
	"/tenants/{tenant_id}/integrity",
	"/tenants/{tenant_id}/sites",
	"/tenants/{tenant_id}/sites/{site_id}",
	"/sites/{site_id}/ingest",
	"/sites/{site_id}/snippet",
	"/tenants/{tenant_id}/api-metrics/overview",
	"/tenants/{tenant_id}/api-metrics/timeseries",
	"/tenants/{tenant_id}/api-metrics/routes",
	"/tenants/{tenant_id}/api-metrics/status",
	"/tenants/{tenant_id}/api-metrics/services",
	"/tenants/{tenant_id}/api-metrics/warnings",
	"/tenants/{tenant_id}/web/summary",
	"/tenants/{tenant_id}/web/breakdown",
	"/tenants/{tenant_id}/web/hourly",
	"/tenants/{tenant_id}/web/realtime",
	"/tenants/{tenant_id}/web/sessions",
	"/tenants/{tenant_id}/onchain",
	"/tenants/{tenant_id}/onchain/ids/{chain_id}/functions",
	"/tenants/{tenant_id}/scf-view",
	"/tenants/{tenant_id}/cohort",
	"/tenants/{tenant_id}/reports",
	"/tenants/{tenant_id}/reports/{report_id}",
	// the operator views over demo projects
	"/operator/tenants",
	"/operator/tenants/{tenant_id}",
	"/operator/web/projects",
	"/operator/web/compare",
	"/operator/web/integrity",
	"/operator/api/projects",
	"/operator/api/compare",
	"/operator/tenants/{tenant_id}/onchain",
	"/operator/tenants/{tenant_id}/onchain/ids/{chain_id}/functions",
	"/operator/cohorts",
	"/operator/tenants/{tenant_id}/cohort",
	"/operator/rules",
	"/operator/rules/{rule_id}",
	"/operator/rules/{rule_id}/matches",
}

// DefaultLimits are the limits of anonymous demo traffic per instance. a console page fires up to
// about twenty reads at once, so a client may burst 60 and then read 5 per
// second; the global ceiling bounds what many clients together cost the
// database and umami (the web routes keep their own per-user engine limit,
// which all demo visitors share).
var DefaultLimits = auth.ClientLimits{
	ClientRate: 5, ClientBurst: 60,
	GlobalRate: 100, GlobalBurst: 400,
	ReserveRate: 10, ReserveBurst: 40,
}

// Routes is a group of the console's routes, as the feature packages hand
// them to the router.
type Routes interface {
	Routes(r chi.Router)
}

// RoutesFunc adapts a function to Routes.
type RoutesFunc func(r chi.Router)

// Routes calls f.
func (f RoutesFunc) Routes(r chi.Router) { f(r) }

// Config holds the parts of the demo. Project and Operator are the same
// route groups cmd/controlplane registers for the console, built with the
// same constructors.
type Config struct {
	Log *slog.Logger
	DB  *db.DB
	// Project groups are registered at the demo's root, Operator groups
	// under /operator without the operator guard and step-up: the demo
	// viewer is no operator, and the allowlist and cp_demo take their place
	Project  []Routes
	Operator []Routes
	Now      func() time.Time
	// Limits replaces DefaultLimits when set (tests)
	Limits *auth.ClientLimits
}

// Handler is the demo's http handler.
type Handler struct {
	log     *slog.Logger
	inner   *chi.Mux
	allowed map[string]bool
	lim     *auth.ClientLimiter
}

// New builds the demo and checks that every allowlisted pattern is a GET
// route of the groups.
func New(c Config) (*Handler, error) {
	if c.Log == nil || c.DB == nil || c.Now == nil {
		return nil, errors.New("demo: log, database and clock are required")
	}
	l := DefaultLimits
	if c.Limits != nil {
		l = *c.Limits
	}
	lim, err := auth.NewClientLimiter(l, c.Now)
	if err != nil {
		return nil, err
	}
	inner := chi.NewRouter()
	inner.Method(http.MethodGet, "/me", httpx.HandlerFunc((&me{db: c.DB}).get))
	for _, g := range c.Project {
		g.Routes(inner)
	}
	inner.Route(OperatorPrefix, func(sub chi.Router) {
		for _, g := range c.Operator {
			g.Routes(sub)
		}
	})
	h := &Handler{log: c.Log, inner: inner, allowed: map[string]bool{}, lim: lim}
	for _, p := range Allowlist {
		if got := inner.Find(chi.NewRouteContext(), http.MethodGet, routePath(p)); got != p {
			return nil, fmt.Errorf("demo: allowlisted route GET %s is not registered (found %q)", p, got)
		}
		h.allowed[p] = true
	}
	return h, nil
}

// routePath turns a pattern into a path that matches it, for the startup
// check: every {param} becomes a value.
func routePath(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "{") {
			parts[i] = "01a1f1c7-eba3-7bad-b5aa-53a02e1f781b"
		}
	}
	return strings.Join(parts, "/")
}

// Allowed reports whether GET of the route pattern is served by the demo.
func (h *Handler) Allowed(pattern string) bool { return h.allowed[pattern] }

// Patterns returns "METHOD /pattern" for every route the demo's groups
// register, allowed or not, for the tests that check each of them.
func (h *Handler) Patterns() []string {
	var out []string
	_ = chi.Walk(h.inner, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)
		return nil
	})
	slices.Sort(out)
	return slices.Compact(out)
}

// the handler is a chi.Routes, so chi.Walk over the console's router lists
// the demo's routes like every other mount's and the isolation gate's
// route coverage sees them. serving goes through ServeHTTP only.
var _ chi.Routes = (*Handler)(nil)

// Routes returns the routes of the demo's groups.
func (h *Handler) Routes() []chi.Route { return h.inner.Routes() }

// Middlewares returns none: ServeHTTP applies the demo's checks itself.
func (h *Handler) Middlewares() chi.Middlewares { return nil }

// Match reports whether the groups register method and path.
func (h *Handler) Match(rctx *chi.Context, method, path string) bool {
	return h.inner.Match(rctx, method, path)
}

// Find returns the pattern of method and path among the groups' routes.
func (h *Handler) Find(rctx *chi.Context, method, path string) string {
	return h.inner.Find(rctx, method, path)
}

// ServeHTTP answers one demo request; see the package comment for the
// order of the checks.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httpx.Base(h.log)(httpx.HandlerFunc(h.serve)).ServeHTTP(w, r)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) error {
	if ok, wait := h.lim.Take(r); !ok {
		return httpx.RateLimited(wait)
	}
	if r.Method != http.MethodGet {
		return readOnly()
	}
	path := innerPath(r)
	if !h.allowed[h.inner.Find(chi.NewRouteContext(), http.MethodGet, path)] {
		return readOnly()
	}
	ctx := db.WithDemo(session.WithDemo(r.Context()))
	// a fresh route context, so the inner router routes the demo path
	// alone and its handlers read only their own path parameters
	ctx = context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	r2 := r.Clone(ctx)
	r2.URL.Path = path
	r2.URL.RawPath = ""
	h.inner.ServeHTTP(w, r2)
	return nil
}

// innerPath is the request path below Path.
func innerPath(r *http.Request) string {
	p := strings.TrimPrefix(r.URL.Path, Path)
	if p == "" {
		return "/"
	}
	return p
}

func readOnly() error {
	return httpx.Forbidden(httpx.CodeDemoReadOnly, "the demo is read-only and does not include this")
}
