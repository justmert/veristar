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

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// roles of cp.tenant_members
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// RoleViewer is the role of the public demo's anonymous viewer on a demo
// project (contract 21). it is no row of cp.tenant_members: the scope gives
// it for a request of the demo, which reads only.
const RoleViewer = "viewer"

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

	// the reconcile queue: a tenant is in queued while it is
	// waiting for a worker or running, and ready lists the waiting ones in
	// trigger order
	mu      sync.Mutex
	queued  map[uuid.UUID]*queueEntry
	ready   []uuid.UUID
	workers int
	running sync.WaitGroup
	// stopped is set by Stop: no run starts any more
	stopped bool
}

type queueEntry struct {
	running bool
	// pending is set when a change arrives while the tenant's run is in
	// flight: that run may have read the cp tables before the change, so
	// one more run follows it
	pending bool
}

// ReconcileWorkers bounds how many reconciles the control plane runs at
// once. each run holds one connection of the reconcile pool and the tenant
// row lock across its umami calls, so the reconcile pool is sized to this.
const ReconcileWorkers = 4

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
	return &Scope{db: d, termsVersion: termsVersion, reconciler: r, log: log, queued: map[uuid.UUID]*queueEntry{}}, nil
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
	// ReadAs is the user whose umami user reads the tenant's web
	// analytics: the member itself, or for the demo viewer the project's
	// first owner (contract 21.3)
	ReadAs uuid.UUID
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
	if session.Demo(ctx) {
		return s.demoTenant(ctx, u, tenantID)
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
		ReadAs:       u.ID,
	}, nil
}

// demoTenant is the access of the public demo's viewer: a live tenant
// flagged demo (cp.tenants.demo, set only by platformctl) answers with role
// viewer; any other id answers the same 404 as a tenant the caller is not a
// member of. the flag is read on every request, so clearing it ends the
// demo's access at once.
func (s *Scope) demoTenant(ctx context.Context, u session.User, tenantID uuid.UUID) (Access, error) {
	var status string
	var accepted *string
	var owner uuid.NullUUID
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			select t.status, t.accepted_terms_version,
			       (select m.user_id from cp.tenant_members m
			        where m.tenant_id = t.id and m.role = 'owner'
			        order by m.created_at, m.user_id limit 1)
			from cp.tenants t
			where t.id = $1 and t.demo and t.status <> 'removed'`, tenantID).Scan(&status, &accepted, &owner)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, httpx.NotFound()
	}
	if err != nil {
		return Access{}, fmt.Errorf("tenants: demo tenant: %w", err)
	}
	a := Access{
		TenantID:     tenantID,
		User:         u,
		Role:         RoleViewer,
		TenantStatus: status,
		TermsCurrent: accepted != nil && *accepted == s.termsVersion,
	}
	if owner.Valid {
		a.ReadAs = owner.UUID
	}
	return a, nil
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

// SiteByID is Site for an id that did not come from a path parameter.
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

// LockTenant takes the tenant row lock inside a cp_tenant transaction and
// refreshes a.TenantStatus from the locked row. the status in Access was
// read before the transaction, so an operator removal, approval or
// suspension that committed while the request waited for the lock would
// otherwise go unseen. a tenant removed meanwhile answers 404,
// as every route does for a removed tenant (contract 1.3).
func LockTenant(ctx context.Context, tx pgx.Tx, a *Access) error {
	status, err := tenantq.New(tx).LockTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if err != nil {
		return err
	}
	if status == StatusRemoved {
		return httpx.NotFound()
	}
	a.TenantStatus = status
	return nil
}

// lockTimeout bounds how long a tenant request waits for a row lock. the
// reconciler holds the tenant row lock across its umami calls (minutes
// when umami is slow); a request that needs that lock answers 503
// unavailable with Retry-After instead of holding a pooled connection
// until the run ends.
const lockTimeout = "5s"

// InTenant runs fn under cp_tenant for the verified tenant.
// in a demo request the database checks the demo flag once more inside the
// transaction; a tenant that lost it meanwhile answers 404.
func (s *Scope) InTenant(ctx context.Context, a Access, fn func(pgx.Tx) error) error {
	err := s.inTenant(ctx, a, fn)
	if errors.Is(err, db.ErrNotDemo) {
		return httpx.NotFound()
	}
	return err
}

func (s *Scope) inTenant(ctx context.Context, a Access, fn func(pgx.Tx) error) error {
	return s.db.WithTenant(ctx, a.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select set_config('lock_timeout', $1, true)`, lockTimeout); err != nil {
			return fmt.Errorf("tenants: set lock timeout: %w", err)
		}
		// the session user, so the database holds tenant writes to what
		// that user may do: audit rows name exactly this user, and member
		// changes need an owner (migration 00028)
		if _, err := tx.Exec(ctx, `select set_config('app.user_id', $1, true)`, a.User.ID.String()); err != nil {
			return fmt.Errorf("tenants: set session user: %w", err)
		}
		return fn(tx)
	})
}

// Reconcile asks for a reconcile of a tenant after a committed change. it
// never waits: the answer does not wait for umami, and the contract reports
// progress through the tenant's provisioning field. requests coalesce per
// tenant: a tenant waiting for a worker is queued once,
// and a change that arrives while its run is in flight makes exactly one
// more run follow, so a burst of edits costs at most two runs. at most
// ReconcileWorkers runs are in flight, whatever the number of tenants. a
// failed run is logged and repeated by the jobs task reconcile.
func (s *Scope) Reconcile(ctx context.Context, tenantID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		// shutting down; the periodic reconcile task covers the change
		return
	}
	if e, ok := s.queued[tenantID]; ok {
		if e.running {
			e.pending = true
		}
		return
	}
	s.queued[tenantID] = &queueEntry{}
	s.ready = append(s.ready, tenantID)
	if s.workers < ReconcileWorkers {
		s.workers++
		s.running.Add(1)
		// detached from the request that asked for it, which answers now;
		// the request's values (its logger) stay with the worker
		go s.work(context.WithoutCancel(ctx))
	}
}

// work runs queued tenants until none is left. workers start on demand and
// stop when the queue is empty, so an idle control plane holds none.
func (s *Scope) work(base context.Context) {
	defer s.running.Done()
	for {
		s.mu.Lock()
		if len(s.ready) == 0 || s.stopped {
			s.workers--
			s.mu.Unlock()
			return
		}
		id := s.ready[0]
		s.ready = s.ready[1:]
		e := s.queued[id]
		e.running = true
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(base, reconcileTimeout)
		if err := s.reconciler.Reconcile(ctx, id); err != nil {
			s.log.Error("reconcile after change failed; the periodic reconcile task retries it",
				"tenant_id", id.String(), "error", err.Error())
		}
		cancel()

		s.mu.Lock()
		if e.pending {
			e.running, e.pending = false, false
			s.ready = append(s.ready, id)
		} else {
			delete(s.queued, id)
		}
		s.mu.Unlock()
	}
}

// Wait blocks until every requested reconcile has run, the pending ones
// included. tests use it to settle the queue.
func (s *Scope) Wait() { s.running.Wait() }

// Stop is the shutdown of the reconcile queue. the server calls
// it after the listener stopped taking requests. runs in flight finish, so
// none is cut off halfway through its umami calls; queued and pending runs
// are dropped, because draining them could take far longer than the
// container's stop grace (each run up to reconcileTimeout, ReconcileWorkers
// at a time), and the process would then be killed mid-run anyway. the
// jobs task reconcile repeats every tenant within its period, so nothing
// dropped here is lost. it returns how many tenants were dropped.
func (s *Scope) Stop() int {
	s.mu.Lock()
	s.stopped = true
	dropped := len(s.ready)
	for _, id := range s.ready {
		delete(s.queued, id)
	}
	s.ready = nil
	for _, e := range s.queued {
		if e.pending {
			dropped++
			e.pending = false
		}
	}
	s.mu.Unlock()
	s.running.Wait()
	if dropped > 0 {
		s.log.Info("reconciles dropped at shutdown; the periodic reconcile task runs them", "tenants", dropped)
	}
	return dropped
}
