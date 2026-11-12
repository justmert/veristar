package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// Trigger starts a reconcile of one tenant after a committed action, without
// waiting for it (tenants.Scope.Reconcile). the jobs task reconcile repeats
// any run that fails.
type Trigger func(ctx context.Context, tenantID uuid.UUID)

// Actions serves the tenant lifecycle routes of contract 5.5 and the site
// review routes of 5.6. it is mounted behind the operator guard
// (operator.Guard.Mount), so its routes see only operators.
type Actions struct {
	db      *db.DB
	trigger Trigger
}

// NewActions needs the cp_system pool and the reconcile trigger: without
// the trigger an action would change the status but never reach umami or
// the registry, so it is refused rather than left out.
func NewActions(d *db.DB, t Trigger) (*Actions, error) {
	if d == nil || t == nil {
		return nil, errors.New("lifecycle: database and reconcile trigger are required")
	}
	return &Actions{db: d, trigger: t}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (a *Actions) Routes(r chi.Router) {
	for _, act := range actionOrder {
		r.Method(http.MethodPost, "/tenants/{tenant_id}/"+string(act), a.tenantHandler(act))
	}
	r.Method(http.MethodPost, "/sites/{site_id}/approve", a.siteHandler(Approve))
	r.Method(http.MethodPost, "/sites/{site_id}/reject", a.siteHandler(Reject))
}

// TenantSummary is OperatorTenantSummary of contract 5.2.
type TenantSummary struct {
	ID                   uuid.UUID       `json:"id"`
	Slug                 string          `json:"slug"`
	Name                 string          `json:"name"`
	Status               string          `json:"status"`
	StatusReason         *string         `json:"status_reason"`
	Website              string          `json:"website"`
	CreatedAt            httpx.Timestamp `json:"created_at"`
	StatusChangedAt      httpx.Timestamp `json:"status_changed_at"`
	SiteCount            int64           `json:"site_count"`
	SitesInReview        int64           `json:"sites_in_review"`
	SitesVerified        int64           `json:"sites_verified"`
	SitesWithStellarToml int64           `json:"sites_with_stellar_toml"`
}

func toSummary(t systemq.LifecycleTenantSummaryRow) TenantSummary {
	return TenantSummary{
		ID: t.ID, Slug: t.Slug, Name: t.Name, Status: t.Status, StatusReason: t.StatusReason,
		Website: t.Website, CreatedAt: httpx.Timestamp(t.CreatedAt), StatusChangedAt: httpx.Timestamp(t.StatusChangedAt),
		SiteCount: t.SiteCount, SitesInReview: t.SitesInReview, SitesVerified: t.SitesVerified,
		SitesWithStellarToml: t.SitesWithStellarToml,
	}
}

func (a *Actions) tenantHandler(act Action) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := session.MustUser(r.Context())
		if err != nil {
			return err
		}
		id, err := httpx.ParseID(chi.URLParam(r, "tenant_id"))
		if err != nil {
			return err
		}
		reason, err := decodeReason(r, tenantTransitions[act].reasonRequired)
		if err != nil {
			return err
		}
		sum, err := a.TenantAction(r.Context(), u.ID, id, act, reason)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, sum)
		return nil
	})
}

// TenantAction applies one lifecycle action as the operator, in one
// cp_system transaction with the tenant row locked, writes its audit row and
// then triggers a reconcile. reason must already be validated (nil when
// absent). an unknown or internal tenant is 404.
func (a *Actions) TenantAction(ctx context.Context, operatorID, tenantID uuid.UUID, act Action, reason *string) (TenantSummary, error) {
	t, ok := tenantTransitions[act]
	if !ok {
		return TenantSummary{}, httpx.NotFound()
	}
	if t.reasonRequired && reason == nil {
		return TenantSummary{}, fmt.Errorf("lifecycle: %s needs a reason", act)
	}
	var sum TenantSummary
	err := a.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		cur, err := q.LifecycleLockTenant(ctx, tenantID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.IsInternal) {
			return httpx.NotFound()
		}
		if err != nil {
			return fmt.Errorf("lifecycle: lock tenant: %w", err)
		}
		next, err := Next(cur.Status, act)
		if err != nil {
			return err
		}
		detail := map[string]any{
			"tenant_id": tenantID.String(),
			"status":    audit.Change(cur.Status, next),
		}
		if reason != nil {
			detail["reason"] = *reason
		}

		params := systemq.LifecycleSetTenantStatusParams{Status: next, ID: tenantID}
		switch act {
		case Approve, Resume:
			// the note goes to the audit row; status_reason is cleared
			// because the tenant has nothing left to act on
		default:
			params.StatusReason = reason
		}
		if act == Approve {
			params.ApprovedBy = uuid.NullUUID{UUID: operatorID, Valid: true}
		}
		if err := q.LifecycleSetTenantStatus(ctx, params); err != nil {
			return fmt.Errorf("lifecycle: set status: %w", err)
		}

		switch act {
		case Approve:
			ids, err := q.LifecycleActivatePendingSites(ctx, tenantID)
			if err != nil {
				return fmt.Errorf("lifecycle: activate sites: %w", err)
			}
			detail["sites_activated"] = idStrings(ids)
		case Remove:
			if err := a.remove(ctx, q, operatorID, tenantID, *reason, detail); err != nil {
				return err
			}
		}

		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(operatorID), Action: t.audit, Target: audit.TenantTarget(tenantID), Detail: detail,
		}); err != nil {
			return err
		}
		row, err := q.LifecycleTenantSummary(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("lifecycle: summary: %w", err)
		}
		sum = toSummary(row)
		return nil
	})
	if err != nil {
		return TenantSummary{}, err
	}
	a.trigger(ctx, tenantID)
	return sum, nil
}

// remove is the transactional part of a removal (contract 5.5 steps 1 to
// 4): sites removed, open invites deleted, and the cp.tenant_removals row
// that drives the purge, with removal_keep_aggregates copied as it is now.
func (a *Actions) remove(ctx context.Context, q *systemq.Queries, operatorID, tenantID uuid.UUID, reason string, detail map[string]any) error {
	sites, err := q.LifecycleRemoveSites(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("lifecycle: remove sites: %w", err)
	}
	invites, err := q.LifecycleDeleteInvites(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("lifecycle: delete invites: %w", err)
	}
	raw, err := q.LifecycleKeepAggregates(ctx)
	if err != nil {
		return fmt.Errorf("lifecycle: read removal_keep_aggregates: %w", err)
	}
	keep, err := decodeKeep(raw)
	if err != nil {
		return err
	}
	if err := q.LifecycleInsertRemoval(ctx, systemq.LifecycleInsertRemovalParams{
		TenantID: tenantID, RequestedBy: uuid.NullUUID{UUID: operatorID, Valid: true},
		Reason: reason, KeepAggregates: keep,
	}); err != nil {
		return fmt.Errorf("lifecycle: insert removal: %w", err)
	}
	detail["sites_removed"] = idStrings(sites)
	detail["invites_deleted"] = invites
	detail["keep_aggregates"] = keep
	return nil
}

// decodeKeep reads the removal_keep_aggregates setting, which must be a
// json boolean. anything else is a broken setting, not a default.
func decodeKeep(raw []byte) (bool, error) {
	var keep *bool
	if err := json.Unmarshal(raw, &keep); err != nil || keep == nil {
		return false, fmt.Errorf("lifecycle: setting removal_keep_aggregates is %s, not a boolean", raw)
	}
	return *keep, nil
}

func idStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// OperatorSite is OperatorSite of contract 5.2; verify_token is never part
// of it.
type OperatorSite struct {
	ID                      uuid.UUID        `json:"id"`
	Name                    string           `json:"name"`
	Domain                  string           `json:"domain"`
	Origins                 []string         `json:"origins"`
	Status                  string           `json:"status"`
	StatusReason            *string          `json:"status_reason"`
	InReview                bool             `json:"in_review"`
	DomainPreviouslyRemoved bool             `json:"domain_previously_removed"`
	Verification            string           `json:"verification"`
	VerifyMethod            *string          `json:"verify_method"`
	VerifiedAt              *httpx.Timestamp `json:"verified_at"`
	LastVerifyAt            *httpx.Timestamp `json:"last_verify_at"`
	LastVerifyError         *string          `json:"last_verify_error"`
	StellarToml             json.RawMessage  `json:"stellar_toml"`
	StellarTomlCheckedAt    *httpx.Timestamp `json:"stellar_toml_checked_at"`
	CreatedAt               httpx.Timestamp  `json:"created_at"`
	UpdatedAt               httpx.Timestamp  `json:"updated_at"`
}

// Verification is the verification value of contract 5.8.
func Verification(verifiedAt, lastVerifyAt *time.Time, lastVerifyError *string) string {
	switch {
	case lastVerifyAt == nil:
		return "unchecked"
	case verifiedAt != nil && lastVerifyError == nil:
		return "verified"
	default:
		return "unverified"
	}
}

func toOperatorSite(s systemq.LifecycleOperatorSiteRow) OperatorSite {
	toml := json.RawMessage("null")
	if len(s.StellarToml) > 0 {
		toml = json.RawMessage(s.StellarToml)
	}
	origins := s.Origins
	if origins == nil {
		origins = []string{}
	}
	return OperatorSite{
		ID: s.ID, Name: s.Name, Domain: s.Domain, Origins: origins, Status: s.Status,
		StatusReason: s.StatusReason, InReview: s.InReview, DomainPreviouslyRemoved: s.DomainPreviouslyRemoved,
		Verification: Verification(s.VerifiedAt, s.LastVerifyAt, s.LastVerifyError), VerifyMethod: s.VerifyMethod,
		VerifiedAt: httpx.TimestampPtr(s.VerifiedAt), LastVerifyAt: httpx.TimestampPtr(s.LastVerifyAt),
		LastVerifyError: s.LastVerifyError, StellarToml: toml,
		StellarTomlCheckedAt: httpx.TimestampPtr(s.StellarTomlCheckedAt),
		CreatedAt:            httpx.Timestamp(s.CreatedAt), UpdatedAt: httpx.Timestamp(s.UpdatedAt),
	}
}

func (a *Actions) siteHandler(act Action) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := session.MustUser(r.Context())
		if err != nil {
			return err
		}
		id, err := httpx.ParseID(chi.URLParam(r, "site_id"))
		if err != nil {
			return err
		}
		reason, err := decodeReason(r, act == Reject)
		if err != nil {
			return err
		}
		s, err := a.SiteAction(r.Context(), u.ID, id, act, reason)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, s)
		return nil
	})
}

// SiteAction approves or rejects a site in review (contract 5.6): a pending
// site of an active tenant. approve makes it active, reject makes it
// removed with the reason. sites of a pending tenant are approved with the
// tenant, so they are refused here.
func (a *Actions) SiteAction(ctx context.Context, operatorID, siteID uuid.UUID, act Action, reason *string) (OperatorSite, error) {
	var to, auditAction string
	switch act {
	case Approve:
		to, auditAction = StatusActive, AuditSiteApprove
	case Reject:
		if reason == nil {
			return OperatorSite{}, errors.New("lifecycle: site reject needs a reason")
		}
		to, auditAction = StatusRemoved, AuditSiteReject
	default:
		return OperatorSite{}, httpx.NotFound()
	}
	var out OperatorSite
	var tenantID uuid.UUID
	err := a.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		cur, err := q.LifecycleLockSite(ctx, siteID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.IsInternal) {
			return httpx.NotFound()
		}
		if err != nil {
			return fmt.Errorf("lifecycle: lock site: %w", err)
		}
		tenantID = cur.TenantID
		if cur.Status != StatusPending || cur.TenantStatus != StatusActive {
			return httpx.Conflict(CodeInvalidTransition, fmt.Sprintf(
				"site %s is only allowed for a pending site of an active project; the site is %s and the project is %s",
				act, cur.Status, cur.TenantStatus))
		}
		p := systemq.LifecycleSetSiteStatusParams{Status: to, ID: siteID}
		if act == Reject {
			p.StatusReason = reason
		}
		if err := q.LifecycleSetSiteStatus(ctx, p); err != nil {
			return fmt.Errorf("lifecycle: set site status: %w", err)
		}
		detail := map[string]any{
			"tenant_id": cur.TenantID.String(),
			"status":    audit.Change(cur.Status, to),
		}
		if reason != nil {
			detail["reason"] = *reason
		}
		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(operatorID), Action: auditAction, Target: audit.SiteTarget(siteID), Detail: detail,
		}); err != nil {
			return err
		}
		row, err := q.LifecycleOperatorSite(ctx, siteID)
		if err != nil {
			return fmt.Errorf("lifecycle: read site: %w", err)
		}
		out = toOperatorSite(row)
		return nil
	})
	if err != nil {
		return OperatorSite{}, err
	}
	a.trigger(ctx, tenantID)
	return out, nil
}
