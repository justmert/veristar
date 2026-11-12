// Package controlplane wires the control plane api (the design,
// docs/reference/control-plane-api.md): one chi router under /api with the
// shared middleware, and the route groups of each feature package.
package controlplane

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
)

// Routes is what a feature package hands to the router. Routes is called
// with a router already mounted at /api, so a package registers paths such
// as "/tenants/{tenant_id}".
type Routes interface {
	Routes(r chi.Router)
}

// RoutesFunc adapts a function to Routes.
type RoutesFunc func(r chi.Router)

// Routes calls f.
func (f RoutesFunc) Routes(r chi.Router) { f(r) }

// Options are the parts of the api handler.
type Options struct {
	Log *slog.Logger
	// ConsoleOrigin is the exact Origin accepted on state changes.
	ConsoleOrigin string
	Sessions      *session.Manager
	// Public route groups need no session (sign-in, terms).
	Public []Routes
	// Private route groups answer 401 without a signed-in session; their
	// handlers read the user with session.MustUser.
	Private []Routes
}

// NewHandler returns the api handler. middleware order, outermost first:
// request id, no-store, recovery and the access log; body size and media
// type; the csrf header and origin checks; session loading. so a refused
// request never touches the session store.
func NewHandler(o Options) http.Handler {
	api := chi.NewRouter()
	api.Use(httpx.Base(o.Log), httpx.LimitBody, httpx.CSRF(o.ConsoleOrigin), o.Sessions.Load)
	api.NotFound(httpx.NotFoundHandler)
	api.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	for _, g := range o.Public {
		g.Routes(api)
	}
	api.Group(func(r chi.Router) {
		r.Use(o.Sessions.RequireUser)
		for _, g := range o.Private {
			g.Routes(r)
		}
	})

	root := chi.NewRouter()
	root.Mount("/api", api)
	// outside /api the control plane serves nothing; caddy serves the console
	root.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Base(o.Log)(http.HandlerFunc(httpx.NotFoundHandler)).ServeHTTP(w, r)
	})
	return root
}
