package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/db/systemq"
)

// a Purger deletes one data store's share of a removed tenant. Purge must be
// idempotent: it runs again after a crash and on every reconcile until done.
// otlp_tokens, api and agg are purgers too, listed in Purgers; a
// purger added later runs for tenants removed before it existed, because
// the runner keys finished stores by Name.
type Purger interface {
	// Name is stable: it is the key in cp.tenant_removals.steps
	Name() string
	// Purge returns a json detail for the audit row and the step record;
	// it must never hold a secret
	Purge(ctx context.Context, r Removal) (detail map[string]any, err error)
}

// Removal is what a purger knows about the tenant it purges.
type Removal struct {
	TenantID    uuid.UUID
	UmamiTeamID uuid.NullUUID
	// every site the tenant ever had, removed ones included
	Sites []RemovedSite
	// copied from removal_keep_aggregates when the tenant was removed;
	// purgers of aggregate stores skip their work when it is true
	KeepAggregates bool
	// Tx is the cp_system transaction of the run, which holds the tenant
	// row lock. purgers of cp tables use it, so their deletes commit with
	// the step record.
	Tx pgx.Tx
}

// RemovedSite is one site of a removed tenant.
type RemovedSite struct {
	ID                  uuid.UUID
	QuarantineWebsiteID uuid.NullUUID
	Domain              string
}

// Runner runs the purgers of a removed tenant in order. ingestion stops
// first (the registry), data goes after, so the order of Purgers matters.
type Runner struct {
	Purgers []Purger
}

// NewRunner checks that every purger has a distinct, non-empty name: the
// name is the progress key, so a clash would skip a store.
func NewRunner(purgers ...Purger) (*Runner, error) {
	if len(purgers) == 0 {
		return nil, errors.New("lifecycle: a purge runner needs purgers")
	}
	seen := map[string]bool{}
	for _, p := range purgers {
		if p == nil || p.Name() == "" || seen[p.Name()] {
			return nil, fmt.Errorf("lifecycle: purger names must be set and distinct (%v)", p)
		}
		seen[p.Name()] = true
	}
	return &Runner{Purgers: purgers}, nil
}

// RecordError is a failed write of the runner's own progress or audit rows.
// the transaction is unusable after it, so the caller must not commit.
type RecordError struct{ Err error }

func (e *RecordError) Error() string { return e.Err.Error() }
func (e *RecordError) Unwrap() error { return e.Err }

// ErrNoRemoval is returned for a removed tenant without a cp.tenant_removals
// row: removal goes through the operator api, which writes the row, so a
// status set by hand is reported instead of purged with guessed settings.
var ErrNoRemoval = errors.New("lifecycle: removed tenant has no cp.tenant_removals row; remove it through the operator api")

// step is the value of one store in cp.tenant_removals.steps.
type step struct {
	DoneAt time.Time      `json:"done_at"`
	Detail map[string]any `json:"detail"`
}

// Run purges what is left of a removed tenant inside tx, the caller's
// cp_system transaction that already holds the tenant row lock. finished
// stores are skipped; a failing store stops the run (later stores wait for
// it, so data is never deleted while ingestion could still write it) and
// is retried on the next reconcile. each finished store writes its step
// and a purge.step audit row; when every store is done, completed_at is
// set and purge.complete is written.
func (r *Runner) Run(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, teamID uuid.NullUUID) error {
	q := systemq.New(tx)
	rm, err := q.PurgeLockRemoval(ctx, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w (tenant %s)", ErrNoRemoval, tenantID)
	}
	if err != nil {
		return &RecordError{fmt.Errorf("lifecycle: lock removal: %w", err)}
	}
	done := map[string]json.RawMessage{}
	if err := json.Unmarshal(rm.Steps, &done); err != nil {
		return fmt.Errorf("lifecycle: steps of tenant %s: %w", tenantID, err)
	}
	pending := 0
	for _, p := range r.Purgers {
		if _, ok := done[p.Name()]; !ok {
			pending++
		}
	}
	if pending == 0 && rm.CompletedAt != nil {
		return nil
	}

	sites, err := q.PurgeSites(ctx, tenantID)
	if err != nil {
		return &RecordError{fmt.Errorf("lifecycle: sites: %w", err)}
	}
	removal := Removal{TenantID: tenantID, UmamiTeamID: teamID, KeepAggregates: rm.KeepAggregates, Tx: tx}
	for _, s := range sites {
		removal.Sites = append(removal.Sites, RemovedSite{ID: s.ID, QuarantineWebsiteID: s.QuarantineWebsiteID, Domain: s.Domain})
	}

	target := audit.TenantTarget(tenantID)
	for _, p := range r.Purgers {
		if _, ok := done[p.Name()]; ok {
			continue
		}
		detail, err := p.Purge(ctx, removal)
		if err != nil {
			return fmt.Errorf("lifecycle: purge %s of tenant %s: %w", p.Name(), tenantID, err)
		}
		if detail == nil {
			detail = map[string]any{}
		}
		b, err := json.Marshal(step{DoneAt: time.Now().UTC(), Detail: detail})
		if err != nil {
			return &RecordError{err}
		}
		if err := q.PurgeSetStep(ctx, systemq.PurgeSetStepParams{Name: p.Name(), Step: b, TenantID: tenantID}); err != nil {
			return &RecordError{fmt.Errorf("lifecycle: record step %s: %w", p.Name(), err)}
		}
		if err := audit.InSystem(ctx, tx, audit.Entry{Actor: ActorPurge, Action: AuditPurgeStep, Target: target,
			Detail: map[string]any{"tenant_id": tenantID.String(), "step": p.Name(), "detail": detail}}); err != nil {
			return &RecordError{err}
		}
	}

	if err := q.PurgeComplete(ctx, tenantID); err != nil {
		return &RecordError{fmt.Errorf("lifecycle: complete removal: %w", err)}
	}
	names := make([]string, len(r.Purgers))
	for i, p := range r.Purgers {
		names[i] = p.Name()
	}
	if err := audit.InSystem(ctx, tx, audit.Entry{Actor: ActorPurge, Action: AuditPurgeComplete, Target: target,
		Detail: map[string]any{"tenant_id": tenantID.String(), "steps": names}}); err != nil {
		return &RecordError{err}
	}
	return nil
}
