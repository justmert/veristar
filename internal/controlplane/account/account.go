// Package account serves DELETE /api/me (control-plane-api 4.1.6): a
// signed-in person deletes their own console account.
//
// the deletion removes every membership of the account, which revokes its
// mcp credentials for those tenants and, through the reconciler, its web
// dashboard access; deletes its mcp credentials, oauth codes, sessions and
// the invites addressed to it; deletes its umami user; and empties its
// cp.users row (migration 00031), so its github id and login are gone. the
// audit log is append-only and keeps its rows, which name the account by its
// user id only.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/umami"
)

// UmamiUsers is the part of the umami admin client the deletion needs
// (umami.Admin).
type UmamiUsers interface {
	GetUser(ctx context.Context, id string) (umami.User, bool, error)
	DeleteUser(ctx context.Context, id string) error
}

// Config is what the deletion needs. all fields are required.
type Config struct {
	DB       *db.DB
	Sessions *session.Manager
	Umami    UmamiUsers
	// Reconcile asks for a reconcile run of a tenant without waiting
	// (tenants.Scope.Reconcile), so each former tenant's umami team is
	// brought in line with its members.
	Reconcile func(ctx context.Context, tenantID uuid.UUID)
}

// Routes serves the account route.
type Routes struct{ c Config }

// NewRoutes checks c.
func NewRoutes(c Config) (*Routes, error) {
	if c.DB == nil || c.Sessions == nil || c.Umami == nil || c.Reconcile == nil {
		return nil, errors.New("account: db, sessions, umami and reconcile are required")
	}
	return &Routes{c: c}, nil
}

// Routes registers the path under a router mounted at /api, behind
// RequireUser.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodDelete, "/me", httpx.HandlerFunc(h.delete))
}

// Result is what one deletion removed, for the audit row and the tests.
type Result struct {
	// Deleted is false when the account was already deleted and nothing
	// changed.
	Deleted            bool
	Tenants            []uuid.UUID
	InvitesDeleted     int64
	CredentialsDeleted int64
	SessionsEnded      int64
	WasOperator        bool
	UmamiUserDeleted   bool
}

func (h *Routes) delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	res, err := Delete(ctx, h.c.DB, h.c.Umami, u.ID)
	if err != nil {
		return err
	}
	// the session rows are gone already; this clears the request's session
	// and expires the cookie
	if err := h.c.Sessions.Destroy(ctx); err != nil {
		return err
	}
	for _, t := range res.Tenants {
		h.c.Reconcile(ctx, t)
	}
	httpx.Logger(ctx).Info("account deleted", "user_id", u.ID.String(), "tenants", len(res.Tenants))
	httpx.NoContent(w)
	return nil
}

// Delete deletes the account with id userID in one cp_system transaction.
// it answers 409 sole_owner, changing nothing, while the account is the only
// owner of a tenant that is not removed. an account that is already deleted
// is left as it is (Result.Deleted false).
//
// the umami user is deleted inside the transaction, after every row change
// and before the commit, while the tenant rows are locked: a failure there
// rolls the rows back and the request can be repeated, and a reconcile run
// waiting on a tenant row lock reads the memberships only after the commit,
// so it cannot create the user again.
func Delete(ctx context.Context, d *db.DB, um UmamiUsers, userID uuid.UUID) (Result, error) {
	var res Result
	err := d.WithSystem(ctx, func(tx pgx.Tx) error {
		res = Result{}
		q := systemq.New(tx)
		u, err := q.AccountLockUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return fmt.Errorf("account: lock user: %w", err)
		}
		if u.DeletedAt != nil {
			return nil
		}
		if _, err := q.AccountLockTenants(ctx, userID); err != nil {
			return fmt.Errorf("account: lock tenants: %w", err)
		}
		if err := q.AccountLockOwners(ctx, userID); err != nil {
			return fmt.Errorf("account: lock owners: %w", err)
		}
		sole, err := q.AccountSoleOwnedTenants(ctx, userID)
		if err != nil {
			return fmt.Errorf("account: sole owner check: %w", err)
		}
		if len(sole) > 0 {
			refs := make([]httpx.TenantRef, 0, len(sole))
			for _, t := range sole {
				refs = append(refs, httpx.TenantRef{ID: t.ID.String(), Name: t.Name})
			}
			e := httpx.Conflict(httpx.CodeSoleOwner,
				"you are the only owner of a project; make another member an owner, or ask the operators to remove the project, first")
			e.Tenants = refs
			return e
		}

		memberships, err := q.AccountDeleteMemberships(ctx, userID)
		if err != nil {
			return fmt.Errorf("account: memberships: %w", err)
		}
		roles := make([]map[string]any, 0, len(memberships))
		for _, m := range memberships {
			res.Tenants = append(res.Tenants, m.TenantID)
			roles = append(roles, map[string]any{"tenant_id": m.TenantID.String(), "role": m.Role})
		}
		if res.InvitesDeleted, err = q.AccountDeleteInvites(ctx, u.GithubID); err != nil {
			return fmt.Errorf("account: invites: %w", err)
		}
		if _, err := q.AccountDeleteMCPCodes(ctx, userID); err != nil {
			return fmt.Errorf("account: mcp codes: %w", err)
		}
		if res.CredentialsDeleted, err = q.AccountDeleteMCPCredentials(ctx, userID); err != nil {
			return fmt.Errorf("account: mcp credentials: %w", err)
		}
		if res.SessionsEnded, err = q.AccountDeleteSessions(ctx, userID); err != nil {
			return fmt.Errorf("account: sessions: %w", err)
		}
		if err := q.AccountEmpty(ctx, userID); err != nil {
			return fmt.Errorf("account: empty user row: %w", err)
		}
		res.WasOperator = u.IsOperator

		// the umami user is named gh-<github id>; deleting it removes that
		// name and ends every umami token the person holds
		id := userID.String()
		_, ok, err := um.GetUser(ctx, id)
		if err != nil {
			return umamiUnavailable(ctx, err)
		}
		if ok {
			if err := um.DeleteUser(ctx, id); err != nil {
				return umamiUnavailable(ctx, err)
			}
			res.UmamiUserDeleted = true
		}

		// the detail names no github id or login: the row it describes no
		// longer holds them
		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(userID), Action: audit.AccountDelete, Target: audit.UserTarget(userID),
			Detail: map[string]any{
				"memberships":         roles,
				"invites_deleted":     res.InvitesDeleted,
				"credentials_deleted": res.CredentialsDeleted,
				"sessions_ended":      res.SessionsEnded,
				"was_operator":        res.WasOperator,
				"umami_user_deleted":  res.UmamiUserDeleted,
			},
		}); err != nil {
			return err
		}
		res.Deleted = true
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func umamiUnavailable(ctx context.Context, err error) error {
	httpx.Logger(ctx).Warn("account deletion: umami user", "error", err.Error())
	return httpx.Unavailable("the web dashboards could not be reached to remove your access; nothing was deleted, try again")
}
