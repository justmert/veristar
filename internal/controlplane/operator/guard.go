// Package operator serves the operator routes
// (docs/reference/control-plane-api.md section 5). every route lives under
// /api/operator and passes the guard of this file first.
package operator

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
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
	ops identity.Operators
}

// NewGuard takes the loaded OPERATOR_GITHUB_IDS and
// OPERATOR_STELLAR_ADDRESSES. an empty github list would make the operator
// area unreachable without saying why, so it is refused; the address list
// may be empty.
func NewGuard(ops identity.Operators) (*Guard, error) {
	if len(ops.GitHubIDs()) == 0 {
		return nil, errors.New("operator: OPERATOR_GITHUB_IDS is empty")
	}
	cp := identity.Operators{GitHub: map[int64]bool{}, Stellar: map[string]bool{}}
	for _, id := range ops.GitHubIDs() {
		cp.GitHub[id] = true
	}
	for _, a := range ops.StellarAddresses() {
		cp.Stellar[a] = true
	}
	return &Guard{ops: cp}, nil
}

// Allowed reports whether u may use the operator routes: cp.users.is_operator
// (read on every request by session.RequireUser) and a github id, stellar
// address or passkey address of the account in the lists this process loaded. both are needed,
// so an entry removed from a list stops working at the next start even
// before that person signs in again, and a stale flag alone never grants
// access.
func (g *Guard) Allowed(u session.User) bool {
	return u.IsOperator && g.ops.Is(u.GitHubID, u.StellarAddress, u.PasskeyAddress)
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
