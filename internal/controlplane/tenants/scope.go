// Package tenants serves enrollment and the tenant routes
// (control-plane-api 4.2.1 to 4.2.4 and 4.2.6), and holds the tenant
// scoping every tenant-scoped route shares (contract 1.3): membership is
// checked under cp_system first, then the handler runs under cp_tenant
// through db.WithTenant.
package tenants

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// roles of cp.tenant_members
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// StatusRemoved tenants answer 404 to every member (contract 1.3).
const StatusRemoved = "removed"

// Reconciler brings umami and the registry in line with the cp tables for
// one tenant. *provision.Reconciler implements it; the routes call it after
// every change that umami or the registry must follow.
type Reconciler interface {
	Reconcile(ctx context.Context, tenantID uuid.UUID) error
}

// ReconcileFunc adapts a function to Reconciler, for a caller that wraps
// the reconciler (a queue that coalesces runs, for example).
type ReconcileFunc func(ctx context.Context, tenantID uuid.UUID) error

// Reconcile calls f.
func (f ReconcileFunc) Reconcile(ctx context.Context, tenantID uuid.UUID) error {
	return f(ctx, tenantID)
}

// Scope resolves the tenant of a request and the caller's access to it.
type Scope struct {
	db           *db.DB
	termsVersion string
	reconciler   Reconciler
	log          *slog.Logger
	running      sync.WaitGroup
}

// reconcileTimeout bounds one triggered run; the jobs task reconcile runs
// every tenant every 10 minutes, so a run cut short here is repeated there.
const reconcileTimeout = 2 * time.Minute

// NewScope checks its inputs: without the current terms version or a
// reconciler the routes cannot do what the contract says, so they are not
// built at all.
func NewScope(d *db.DB, termsVersion string, r Reconciler, log *slog.Logger) (*Scope, error) {
	if d == nil || r == nil || log == nil {
		return nil, errors.New("tenants: database, reconciler and logger are required")
	}
	if termsVersion == "" {
		return nil, errors.New("tenants: current terms version is required")
	}
	return &Scope{db: d, termsVersion: termsVersion, reconciler: r, log: log}, nil
}

// DB returns the database the scope checks against.
func (s *Scope) DB() *db.DB { return s.db }

// TermsVersion is the current terms version.
func (s *Scope) TermsVersion() string { return s.termsVersion }

// Access is the caller's verified membership of one tenant.
type Access struct {
	TenantID     uuid.UUID
	User         session.User
	Role         string
	TenantStatus string
	TermsCurrent bool
}

// RequireOwner answers 403 owner_required for a member without role owner.
// members already know the tenant exists, so 403 reveals nothing.
func (a Access) RequireOwner() error {
	if a.Role != RoleOwner {
		return httpx.Forbidden(httpx.CodeOwnerRequired, "this action needs role owner")
	}
	return nil
}

// RequireTerms answers 403 terms_not_accepted while the tenant has not
// accepted the current terms version (contract 4.2.6).
func (a Access) RequireTerms() error {
	if !a.TermsCurrent {
		return httpx.Forbidden(httpx.CodeTermsNotAccepted, "the project must accept the current terms first")
	}
	return nil
}

// Tenant checks that the signed-in user is a member of the tenant named by
// the path parameter. a malformed id, an unknown id, a tenant the user is
// not a member of and a removed tenant all answer the same 404, so ids of
// other tenants cannot be probed. membership is read on every request, so a
// removed member loses access on the next one.
func (s *Scope) Tenant(r *http.Request, param string) (Access, error) {
	id, err := httpx.ParseID(chiParam(r, param))
	if err != nil {
		return Access{}, err
	}
	return s.tenantByID(r.Context(), id)
}

func (s *Scope) tenantByID(ctx context.Context, tenantID uuid.UUID) (Access, error) {
	u, err := session.MustUser(ctx)
	if err != nil {
		return Access{}, err
	}
	var m systemq.GetMembershipRow
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		m, qerr = systemq.New(tx).GetMembership(ctx, systemq.GetMembershipParams{TenantID: tenantID, UserID: u.ID})
		return qerr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, httpx.NotFound()
	}
	if err != nil {
		return Access{}, fmt.Errorf("tenants: membership: %w", err)
	}
	if m.Status == StatusRemoved {
		return Access{}, httpx.NotFound()
	}
	return Access{
		TenantID:     tenantID,
		User:         u,
		Role:         m.Role,
		TenantStatus: m.Status,
		TermsCurrent: m.AcceptedTermsVersion != nil && *m.AcceptedTermsVersion == s.termsVersion,
	}, nil
}

// Site resolves the tenant of a site named by the path parameter under
// cp_system, then applies the tenant rule. the caller still reads the site
// itself under cp_tenant.
func (s *Scope) Site(r *http.Request, param string) (Access, uuid.UUID, error) {
	siteID, err := httpx.ParseID(chiParam(r, param))
	if err != nil {
		return Access{}, uuid.Nil, err
	}
	return s.SiteByID(r.Context(), siteID)
}

// SiteByID is Site for an id that did not come from a path parameter (the
// sso route takes it from the query).
func (s *Scope) SiteByID(ctx context.Context, siteID uuid.UUID) (Access, uuid.UUID, error) {
	if _, err := session.MustUser(ctx); err != nil {
		return Access{}, uuid.Nil, err
	}
	var tenantID uuid.UUID
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		tenantID, qerr = systemq.New(tx).GetSiteTenantID(ctx, siteID)
		return qerr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, uuid.Nil, httpx.NotFound()
	}
	if err != nil {
		return Access{}, uuid.Nil, fmt.Errorf("tenants: site tenant: %w", err)
	}
	a, err := s.tenantByID(ctx, tenantID)
	if err != nil {
		return Access{}, uuid.Nil, err
	}
	return a, siteID, nil
}

// InTenant runs fn under cp_tenant for the verified tenant.
func (s *Scope) InTenant(ctx context.Context, a Access, fn func(pgx.Tx) error) error {
	return s.db.WithTenant(ctx, a.TenantID, fn)
}

// Reconcile starts the reconciler for a tenant after a committed change.
// it runs detached from the request, so the answer does not wait for umami;
// the contract reports progress through the tenant's provisioning field. a
// failed run is logged and repeated by the jobs task reconcile.
func (s *Scope) Reconcile(ctx context.Context, tenantID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer cancel()
		if err := s.reconciler.Reconcile(ctx, tenantID); err != nil {
			s.log.Error("reconcile after change failed; the periodic reconcile task retries it",
				"tenant_id", tenantID.String(), "error", err.Error())
		}
	}()
}

// Wait blocks until every reconcile started by Reconcile has returned. the
// server calls it on shutdown, after the listener stopped taking requests,
// so a run is not cut off halfway through its umami calls.
func (s *Scope) Wait() { s.running.Wait() }
