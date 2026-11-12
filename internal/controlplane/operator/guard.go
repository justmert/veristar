// Package operator serves the operator routes of the design
// (docs/reference/control-plane-api.md section 5). every route lives under
// /api/operator and passes the guard of this file first.
package operator

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
)

// Prefix is the path of the operator area under /api.
const Prefix = "/operator"

// Routes is a group of operator routes. Routes is called with a router
// mounted at /api/operator behind the guard, so a group registers paths such
// as "/settings/enrollment-mode".
type Routes interface {
	Routes(r chi.Router)
}

// Guard decides who is an operator (contract 5.1).
type Guard struct {
	ids map[int64]bool
}

// NewGuard takes the loaded OPERATOR_GITHUB_IDS. an empty list would make
// the operator area unreachable without saying why, so it is refused.
func NewGuard(ids map[int64]bool) (*Guard, error) {
	if len(ids) == 0 {
		return nil, errors.New("operator: OPERATOR_GITHUB_IDS is empty")
	}
	cp := make(map[int64]bool, len(ids))
	for id, ok := range ids {
		if ok {
			cp[id] = true
		}
	}
	return &Guard{ids: cp}, nil
}

// Allowed reports whether u may use the operator routes: cp.users.is_operator
// (read on every request by session.RequireUser) and a github id in the list
// this process loaded. both are needed, so an id removed from the list stops
// working at the next start even before that person signs in again, and a
// stale flag alone never grants access.
func (g *Guard) Allowed(u session.User) bool {
	return u.IsOperator && g.ids[u.GitHubID]
}

// Require answers 403 operator_required unless the signed-in user is an
// operator. it goes behind session.RequireUser, so an anonymous request
// still gets 401.
func (g *Guard) Require(next http.Handler) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := session.MustUser(r.Context())
		if err != nil {
			return err
		}
		if !g.Allowed(u) {
			return httpx.Forbidden(httpx.CodeOperatorRequired, "this route is for operators")
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// Mount returns a function that registers groups under /operator behind the
// guard. unknown paths and methods inside the area get the guard too, so a
// non-operator sees 403 for every request there and learns nothing about
// which operator routes exist.
func (g *Guard) Mount(groups ...Routes) func(r chi.Router) {
	return func(r chi.Router) {
		r.Route(Prefix, func(sub chi.Router) {
			sub.Use(g.Require)
			sub.NotFound(httpx.NotFoundHandler)
			sub.MethodNotAllowed(httpx.MethodNotAllowedHandler)
			for _, grp := range groups {
				grp.Routes(sub)
			}
		})
	}
}
