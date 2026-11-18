package domaincheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// console checks allowed per site and window (control-plane-api 5.8)
const (
	verifyLimit  = 6
	verifyWindow = time.Hour
)

// afterCreateTimeout bounds the stellar.toml fetch started after a site is
// created: one guarded fetch (10 s) plus two short transactions.
const afterCreateTimeout = time.Minute

// Routes serves GET /api/sites/{site_id}/verification and
// POST /api/sites/{site_id}/verify, and fetches stellar.toml after a site
// is created.
type Routes struct {
	scope   *tenants.Scope
	checker *Checker
	now     func() time.Time
	log     *slog.Logger
	running sync.WaitGroup
	// checks running in this process, counted by the rate limit
	inflight tenants.Inflight
}

// NewRoutes checks its inputs.
func NewRoutes(s *tenants.Scope, c *Checker, now func() time.Time, log *slog.Logger) (*Routes, error) {
	if s == nil || now == nil || log == nil {
		return nil, errors.New("domaincheck: scope, clock and logger are required")
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	return &Routes{scope: s, checker: c, now: now, log: log}, nil
}

// Routes registers the paths under a router mounted at /api, behind
// RequireUser.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/sites/{site_id}/verification", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPost, "/sites/{site_id}/verify", httpx.HandlerFunc(h.verify))
}

// DNSRecord and FileTarget are the instructions shown to the tenant.
type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// FileTarget is the file the tenant publishes.
type FileTarget struct {
	URL  string `json:"url"`
	Body string `json:"body"`
}

// VerificationView is the object of control-plane-api 5.8. it is the only
// place verify_token is returned.
type VerificationView struct {
	SiteID          uuid.UUID        `json:"site_id"`
	Domain          string           `json:"domain"`
	VerifyToken     string           `json:"verify_token"`
	DNS             DNSRecord        `json:"dns"`
	File            FileTarget       `json:"file"`
	Verification    string           `json:"verification"`
	VerifyMethod    *string          `json:"verify_method"`
	VerifiedAt      *httpx.Timestamp `json:"verified_at"`
	LastVerifyAt    *httpx.Timestamp `json:"last_verify_at"`
	LastVerifyError *string          `json:"last_verify_error"`
}

func view(s tenantq.CpSite) VerificationView {
	return VerificationView{
		SiteID: s.ID, Domain: s.Domain, VerifyToken: s.VerifyToken,
		DNS:             DNSRecord{Name: DNSName(s.Domain), Type: "TXT", Value: DNSValue(s.VerifyToken)},
		File:            FileTarget{URL: FileURL(s.Domain), Body: s.VerifyToken},
		Verification:    Verification(s.VerifiedAt, s.LastVerifyAt, s.LastVerifyError),
		VerifyMethod:    s.VerifyMethod,
		VerifiedAt:      httpx.TimestampPtr(s.VerifiedAt),
		LastVerifyAt:    httpx.TimestampPtr(s.LastVerifyAt),
		LastVerifyError: s.LastVerifyError,
	}
}

func (h *Routes) load(r *http.Request) (tenants.Access, tenantq.CpSite, error) {
	a, id, err := h.scope.Site(r, "site_id")
	if err != nil {
		return a, tenantq.CpSite{}, err
	}
	ctx := r.Context()
	var s tenantq.CpSite
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		s, qerr = tenantq.New(tx).GetSite(ctx, id)
		return qerr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, s, httpx.NotFound()
	}
	return a, s, err
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	_, s, err := h.load(r)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, view(s))
	return nil
}

type verifyBody struct {
	Method *string `json:"method"`
}

func (h *Routes) verify(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	// membership first, so a non-member learns nothing from body errors
	a, s, err := h.load(r)
	if err != nil {
		return err
	}
	var b verifyBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	method := ""
	if b.Method != nil {
		method = *b.Method
		if method != MethodDNS && method != MethodFile {
			return httpx.Validation("method must be dns or file", map[string]string{"method": httpx.FieldInvalidValue})
		}
	}
	if s.Status == "removed" {
		return httpx.Conflict(httpx.CodeSiteRemoved, "the site is removed")
	}
	release, err := h.limit(ctx, s.ID)
	if err != nil {
		return err
	}
	defer release()
	// the network checks run outside any transaction
	o, err := h.checker.Verify(ctx, s.Domain, s.VerifyToken, method)
	if err != nil {
		return err
	}
	// the outcome is written under cp_system, next to the check that
	// decided it: cp_tenant cannot write the verification columns, so no
	// other tenant handler can mark a domain verified (migration
	// 00028). the site id was read under the member's tenant scope.
	out := s
	err = h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		p := systemq.SystemSetSiteVerifyResultParams{Ok: o.OK, ID: s.ID}
		if o.OK {
			p.Method = &o.Method
		} else {
			p.Error = &o.Error
		}
		row, qerr := systemq.New(tx).SystemSetSiteVerifyResult(ctx, p)
		if qerr != nil {
			return qerr
		}
		if row.TenantID != a.TenantID {
			return fmt.Errorf("domaincheck: site %s moved to another tenant", s.ID)
		}
		out.VerifyMethod, out.VerifiedAt = row.VerifyMethod, row.VerifiedAt
		out.LastVerifyAt, out.LastVerifyError = row.LastVerifyAt, row.LastVerifyError
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionVerify, Target: audit.SiteTarget(s.ID),
			Detail: o.auditDetail(a.TenantID.String()),
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// removed while the check ran
		return httpx.Conflict(httpx.CodeSiteRemoved, "the site is removed")
	}
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, view(out))
	return nil
}

// limit answers 429 after verifyLimit console checks of a site within the
// window. the count comes from the audit rows the route writes, so every
// control plane instance sees the same number; two requests that race may
// both pass at the limit, which costs at most a few extra checks.
func (h *Routes) limit(ctx context.Context, siteID uuid.UUID) (func(), error) {
	now := h.now()
	var row systemq.RecentUserVerifiesRow
	// checks of this site still running in this process count too, so a
	// burst of requests cannot all pass before the first records itself
	ok, release, err := h.inflight.Reserve(siteID, verifyLimit, func() (int, error) {
		err := h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
			var qerr error
			row, qerr = systemq.New(tx).RecentUserVerifies(ctx, systemq.RecentUserVerifiesParams{
				Target: audit.SiteTarget(siteID), Since: now.Add(-verifyWindow),
			})
			return qerr
		})
		return int(row.Checks), err
	})
	if err != nil {
		return nil, fmt.Errorf("domaincheck: rate count: %w", err)
	}
	if ok {
		return release, nil
	}
	wait := time.Second
	if row.Checks >= verifyLimit {
		wait = max(row.Oldest.Add(verifyWindow).Sub(now), time.Second)
	}
	return nil, httpx.RateLimited(wait)
}

// AfterSiteCreate fetches the new site's stellar.toml in the background
// and stores it under cp_system, as the daily task does (control-plane-api
// 5.12: asynchronously, after the response; cp_tenant cannot write the
// column). actor is the user who created the site. a failure
// is logged; the daily verify-domains task fetches again.
func (h *Routes) AfterSiteCreate(ctx context.Context, tenantID, siteID uuid.UUID, domain, actor string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), afterCreateTimeout)
	h.running.Add(1)
	go func() {
		defer h.running.Done()
		defer cancel()
		if err := h.fetchAndStore(ctx, tenantID, siteID, domain, actor); err != nil {
			h.log.Error("stellar.toml fetch after site create failed; verify-domains retries it",
				"site_id", siteID.String(), "error", err.Error())
		}
	}()
}

// Wait blocks until every fetch started by AfterSiteCreate returned; the
// server calls it on shutdown.
func (h *Routes) Wait() { h.running.Wait() }

func (h *Routes) fetchAndStore(ctx context.Context, tenantID, siteID uuid.UUID, domain, actor string) error {
	st := FetchStellarToml(ctx, h.checker.Guard, domain)
	return h.scope.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		return StoreSystem(ctx, tx, tenantID, siteID, st, actor)
	})
}

// StoreSystem writes a stellar.toml signal under cp_system (the jobs task),
// with the audit row when status or network changed. tx is the fenced
// transaction of the caller.
func StoreSystem(ctx context.Context, tx pgx.Tx, tenantID, siteID uuid.UUID, st StellarToml, actor string) error {
	q := systemq.New(tx)
	prev, err := q.SystemLockSiteStellarToml(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := q.SystemSetSiteStellarToml(ctx, systemq.SystemSetSiteStellarTomlParams{Doc: doc, ID: siteID}); err != nil {
		return err
	}
	change, changed, err := tomlChange(prev, st)
	if err != nil || !changed {
		return err
	}
	change["tenant_id"] = tenantID.String()
	return audit.InSystem(ctx, tx, audit.Entry{
		Actor: actor, Action: ActionStellarToml, Target: audit.SiteTarget(siteID), Detail: change,
	})
}

// VerifyState is the stored verification of a site before a check.
type VerifyState struct {
	VerifiedAt      *time.Time
	LastVerifyAt    *time.Time
	LastVerifyError *string
}

// StoreVerifySystem writes a verification outcome under cp_system. the
// audit row is written only when the verification state or the error code
// changed, so the daily task does not add one row per site per day.
func StoreVerifySystem(ctx context.Context, tx pgx.Tx, tenantID, siteID uuid.UUID, prev VerifyState, o Outcome, actor string) error {
	p := systemq.SystemSetSiteVerifyResultParams{Ok: o.OK, ID: siteID}
	if o.OK {
		p.Method = &o.Method
	} else {
		p.Error = &o.Error
	}
	row, err := systemq.New(tx).SystemSetSiteVerifyResult(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	before := Verification(prev.VerifiedAt, prev.LastVerifyAt, prev.LastVerifyError)
	after := Verification(row.VerifiedAt, row.LastVerifyAt, row.LastVerifyError)
	if before == after && eq(prev.LastVerifyError, row.LastVerifyError) {
		return nil
	}
	d := o.auditDetail(tenantID.String())
	d["verification"] = audit.Change(before, after)
	return audit.InSystem(ctx, tx, audit.Entry{
		Actor: actor, Action: ActionVerify, Target: audit.SiteTarget(siteID), Detail: d,
	})
}
