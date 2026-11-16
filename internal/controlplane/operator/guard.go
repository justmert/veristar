// Package operator serves the operator routes
// (docs/reference/control-plane-api.md section 5). every route lives under
// /api/operator and passes the guard of this file first.
package operator

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/stepup"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// Prefix is the path of the operator area under /api.
const Prefix = "/operator"

// roles of the roster (00036)
const (
	RoleOperator      = "operator"
	RoleOperatorAdmin = "operator_admin"
)

// Routes is a group of operator routes. Routes is called with a router
// mounted at /api/operator behind the guard, so a group registers paths such
// as "/settings/enrollment-mode".
type Routes interface {
	Routes(r chi.Router)
}

// Guard decides who is an operator (contract 5.1): the account's
// cp.users.is_operator, which the database derives from the active rows of
// the roster (cp.operators, 00036) in the transaction that changes them, and
// read on every request by session.RequireUser. a suspended or removed
// operator is refused at the next request.
type Guard struct {
	db     *db.DB
	stepUp *stepup.Service
}

// NewGuard needs the cp_system pool, for the roster role of a request, and
// the step-up every console request into the area needs.
func NewGuard(d *db.DB, s *stepup.Service) (*Guard, error) {
	if d == nil || s == nil {
		return nil, errors.New("operator: guard needs the database and the step-up")
	}
	return &Guard{db: d, stepUp: s}, nil
}

// Allowed reports whether u may use the operator routes.
func (g *Guard) Allowed(u session.User) bool {
	return u.IsOperator
}

// SteppedUp reports whether the request's session holds a fresh passkey
// step-up of u, for an operator action outside the operator area (the mcp
// consent of an operator credential).
func (g *Guard) SteppedUp(ctx context.Context, u session.User) bool {
	ok, _ := g.stepUp.Fresh(ctx, u.ID)
	return ok
}

// Admin reports whether u holds an active operator admin row of the roster.
func (g *Guard) Admin(ctx context.Context, u session.User) (bool, error) {
	if !u.IsOperator {
		return false, nil
	}
	var admin bool
	err := g.db.WithSystem(ctx, func(tx pgx.Tx) error {
		rows, err := systemq.New(tx).RosterEntriesForUser(ctx, systemq.RosterEntriesForUserParams{
			GithubID: u.GitHubID, StellarAddress: u.StellarAddress,
		})
		for _, o := range rows {
			admin = admin || (o.Status == "active" && o.Role == RoleOperatorAdmin)
		}
		return err
	})
	return admin, err
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

// RequireAdmin answers 403 operator_admin_required unless the operator holds
// the operator admin role. it goes behind Require.
func (g *Guard) RequireAdmin(next http.Handler) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := session.MustUser(r.Context())
		if err != nil {
			return err
		}
		ok, err := g.Admin(r.Context(), u)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.Forbidden(httpx.CodeOperatorAdminRequired, "only operator admins can change the roster")
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// Mount returns a function that registers groups under /operator behind the
// guard and the step-up. the step-up and passkey routes sit behind the guard
// only: they are how a session gets its step-up. unknown paths and methods
// inside the area get the guard too, so a non-operator sees 403 for every
// request there and learns nothing about which operator routes exist.
func (g *Guard) Mount(groups ...Routes) func(r chi.Router) {
	return func(r chi.Router) {
		r.Route(Prefix, func(sub chi.Router) {
			sub.Use(g.Require)
			sub.NotFound(httpx.NotFoundHandler)
			sub.MethodNotAllowed(httpx.MethodNotAllowedHandler)
			g.stepUp.Routes(sub)
			sub.Group(func(s chi.Router) {
				s.Use(g.stepUp.Require)
				for _, grp := range groups {
					grp.Routes(s)
				}
			})
		})
	}
}
