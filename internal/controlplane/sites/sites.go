// Package sites serves the site routes (control-plane-api 4.3.1 to 4.3.5).
// a site's id is also its primary umami website id; the reconciler creates
// the umami websites and the registry entry after each change.
package sites

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/db/tenantq"
)

// site statuses of the cp.sites check
const (
	StatusActive  = "active"
	StatusPending = "pending"
	StatusRemoved = "removed"
)

const maxSites = 50

// tenant statuses the site routes branch on
const (
	tenantActive    = "active"
	tenantSuspended = "suspended"
)

// ActionReviewQueue is the audit action of a site created into review.
const ActionReviewQueue = "site.review_queue"

// refuseSuspended answers 409 tenant_suspended for site create and update
// (contract 5.11); delete stays allowed.
func refuseSuspended(a tenants.Access) error {
	if a.TenantStatus == tenantSuspended {
		return httpx.Conflict(httpx.CodeTenantSuspended, "the project is suspended")
	}
	return nil
}

// Site is the site object of contract 3.1 with the this stage fields of 5.10.
// verify_token is never part of it (5.8 only).
type Site struct {
	ID                   uuid.UUID        `json:"id"`
	TenantID             uuid.UUID        `json:"tenant_id"`
	Name                 string           `json:"name"`
	Domain               string           `json:"domain"`
	Origins              []string         `json:"origins"`
	QuarantineWebsiteID  *uuid.UUID       `json:"quarantine_website_id"`
	Status               string           `json:"status"`
	StatusReason         *string          `json:"status_reason"`
	InReview             bool             `json:"in_review"`
	Verification         string           `json:"verification"`
	VerifyMethod         *string          `json:"verify_method"`
	VerifiedAt           *httpx.Timestamp `json:"verified_at"`
	LastVerifyAt         *httpx.Timestamp `json:"last_verify_at"`
	LastVerifyError      *string          `json:"last_verify_error"`
	StellarToml          json.RawMessage  `json:"stellar_toml"`
	StellarTomlCheckedAt *httpx.Timestamp `json:"stellar_toml_checked_at"`
	// collect_utm keeps the utm_* and ref query parameters of page urls;
	// off unless the operator turns it on (contract 4.3)
	CollectUTM bool            `json:"collect_utm"`
	CreatedAt  httpx.Timestamp `json:"created_at"`
	UpdatedAt  httpx.Timestamp `json:"updated_at"`
}

// verification is the value of contract 5.8, as lifecycle.Verification.
func verification(verifiedAt, lastVerifyAt *time.Time, lastVerifyError *string) string {
	switch {
	case lastVerifyAt == nil:
		return "unchecked"
	case verifiedAt != nil && lastVerifyError == nil:
		return "verified"
	default:
		return "unverified"
	}
}

// ToSite converts a cp.sites row; tenantStatus decides in_review (a pending
// site of an active tenant, contract 5.6).
func ToSite(s tenantq.CpSite, tenantStatus string) Site {
	var q *uuid.UUID
	if s.QuarantineWebsiteID.Valid {
		id := s.QuarantineWebsiteID.UUID
		q = &id
	}
	origins := s.Origins
	if origins == nil {
		origins = []string{}
	}
	toml := json.RawMessage("null")
	if len(s.StellarToml) > 0 {
		toml = json.RawMessage(s.StellarToml)
	}
	return Site{
		ID: s.ID, TenantID: s.TenantID, Name: s.Name, Domain: s.Domain, Origins: origins,
		QuarantineWebsiteID: q, Status: s.Status, StatusReason: s.StatusReason,
		InReview:     s.Status == StatusPending && tenantStatus == tenantActive,
		Verification: verification(s.VerifiedAt, s.LastVerifyAt, s.LastVerifyError),
		VerifyMethod: s.VerifyMethod, VerifiedAt: httpx.TimestampPtr(s.VerifiedAt),
		LastVerifyAt: httpx.TimestampPtr(s.LastVerifyAt), LastVerifyError: s.LastVerifyError,
		StellarToml: toml, StellarTomlCheckedAt: httpx.TimestampPtr(s.StellarTomlCheckedAt),
		CollectUTM: s.CollectUtm,
		CreatedAt:  httpx.Timestamp(s.CreatedAt), UpdatedAt: httpx.Timestamp(s.UpdatedAt),
	}
}

// AfterCreate is told about every created site, after its transaction
// committed (domaincheck.Routes fetches the domain's stellar.toml, contract
// 5.12). it must not block: it returns at once and works in the background.
type AfterCreate interface {
	AfterSiteCreate(ctx context.Context, tenantID, siteID uuid.UUID, domain, actor string)
}

// Routes are the site routes.
type Routes struct {
	s           *tenants.Scope
	afterCreate []AfterCreate
}

// NewRoutes returns the routes over a scope.
func NewRoutes(s *tenants.Scope) *Routes { return &Routes{s: s} }

// OnCreate adds hooks that run after each site creation.
func (h *Routes) OnCreate(hooks ...AfterCreate) *Routes {
	h.afterCreate = append(h.afterCreate, hooks...)
	return h
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodPost, "/tenants/{tenant_id}/sites", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/sites", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/sites/{site_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPatch, "/tenants/{tenant_id}/sites/{site_id}", httpx.HandlerFunc(h.patch))
	r.Method(http.MethodDelete, "/tenants/{tenant_id}/sites/{site_id}", httpx.HandlerFunc(h.remove))
}

type createBody struct {
	Name    *string  `json:"name"`
	Domain  *string  `json:"domain"`
	Origins []string `json:"origins"`
}

func verifyToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (h *Routes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	if err := refuseSuspended(a); err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	var name, domain string
	if b.Name == nil {
		f.Add("name", httpx.FieldRequired)
	} else {
		name = tenants.Text(&f, "name", *b.Name, 1, 100)
	}
	if b.Domain == nil {
		f.Add("domain", httpx.FieldRequired)
	} else if domain = *b.Domain; !ValidDomain(domain) {
		f.Add("domain", httpx.FieldInvalidDomain)
	}
	if b.Domain != nil && ValidDomain(domain) {
		CheckOrigins(&f, b.Origins, domain)
	} else if len(b.Origins) == 0 {
		f.Add("origins", httpx.FieldRequired)
	}
	if err := f.Err(); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	qid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	vt, err := verifyToken()
	if err != nil {
		return err
	}
	// a domain of a removed project always goes to an operator (contract
	// 5.6), in either enrollment mode. cp_tenant cannot see other tenants'
	// sites, so the match runs under cp_system before the insert.
	var match *systemq.ReviewMatchRemovedDomainRow
	err = h.s.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		m, err := systemq.New(tx).ReviewMatchRemovedDomain(ctx, systemq.ReviewMatchRemovedDomainParams{
			TenantID: a.TenantID, Domain: domain,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		match = &m
		return nil
	})
	if err != nil {
		return err
	}
	var site tenantq.CpSite
	var status string
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		// the site status follows the tenant status as of the lock, not the
		// one read before it: a site created while an approval
		// commits is active, one created while a suspension commits is
		// refused, one created while a removal commits is 404
		if err := tenants.LockTenant(ctx, tx, &a); err != nil {
			return err
		}
		if err := refuseSuspended(a); err != nil {
			return err
		}
		status = StatusPending
		if a.TenantStatus == tenantActive && match == nil {
			status = StatusActive
		}
		n, err := q.CountLiveSites(ctx)
		if err != nil {
			return err
		}
		if n >= maxSites {
			return httpx.Validation("a project has at most 50 sites", map[string]string{"name": httpx.FieldTooMany})
		}
		site, err = q.InsertSite(ctx, tenantq.InsertSiteParams{
			ID: id, Name: name, Domain: domain, Origins: b.Origins,
			QuarantineWebsiteID: uuid.NullUUID{UUID: qid, Valid: true}, Status: status, VerifyToken: vt,
		})
		if err != nil {
			return err
		}
		if err := audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.SiteCreate, Target: audit.SiteTarget(id),
			Detail: map[string]any{"tenant_id": a.TenantID, "name": name, "domain": domain,
				"origins": b.Origins, "status": status, "quarantine_website_id": qid},
		}); err != nil {
			return err
		}
		if match == nil {
			return nil
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionReviewQueue, Target: audit.SiteTarget(id),
			Detail: map[string]any{"tenant_id": a.TenantID, "domain": domain,
				"matched_domain": match.Domain, "matched_tenant_id": match.TenantID},
		})
	})
	if err != nil {
		return err
	}
	h.s.Reconcile(ctx, a.TenantID)
	for _, hook := range h.afterCreate {
		hook.AfterSiteCreate(ctx, a.TenantID, id, domain, audit.User(a.User.ID))
	}
	httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/sites/"+id.String(), ToSite(site, a.TenantStatus))
	return nil
}

const listRoute = "sites"

func (h *Routes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	p, err := httpx.ParsePage(r, listRoute)
	if err != nil {
		return err
	}
	params := tenantq.ListSitesPageParams{RowLimit: int32(p.Limit + 1)} //nolint:gosec // limit is at most 200
	if v, ok := r.URL.Query()["include_removed"]; ok {
		switch {
		case len(v) == 1 && v[0] == "true":
			params.IncludeRemoved = true
		case len(v) == 1 && v[0] == "false":
		default:
			return httpx.BadQuery("include_removed must be true or false")
		}
	}
	if p.After != nil {
		params.AfterCreatedAt = &p.After.At
		params.AfterID = uuid.NullUUID{UUID: p.After.ID, Valid: true}
	}
	var rows []tenantq.CpSite
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = tenantq.New(tx).ListSitesPage(ctx, params)
		return qerr
	}); err != nil {
		return err
	}
	out := httpx.List[Site]{Items: []Site{}}
	for i, s := range rows {
		if i == p.Limit {
			last := rows[i-1]
			out.NextCursor = httpx.NextCursor(listRoute, httpx.Cursor{At: last.CreatedAt, ID: last.ID})
			break
		}
		out.Items = append(out.Items, ToSite(s, a.TenantStatus))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// load reads a site of the verified tenant. under cp_tenant a site of
// another tenant does not exist, so a guessed id is the same 404.
func (h *Routes) load(r *http.Request) (tenants.Access, uuid.UUID, error) {
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return tenants.Access{}, uuid.Nil, err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "site_id"))
	if err != nil {
		return tenants.Access{}, uuid.Nil, err
	}
	return a, id, nil
}

func getSite(r *http.Request, tx pgx.Tx, id uuid.UUID) (tenantq.CpSite, error) {
	s, err := tenantq.New(tx).GetSite(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, httpx.NotFound()
	}
	return s, err
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	a, id, err := h.load(r)
	if err != nil {
		return err
	}
	var s tenantq.CpSite
	if err := h.s.InTenant(r.Context(), a, func(tx pgx.Tx) error {
		var qerr error
		s, qerr = getSite(r, tx, id)
		return qerr
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, ToSite(s, a.TenantStatus))
	return nil
}

type patchBody struct {
	Name       *string  `json:"name"`
	Origins    []string `json:"origins"`
	CollectUTM *bool    `json:"collect_utm"`
	// domain is listed only to answer 422 instead of 400 unknown field: it
	// is immutable after creation
	Domain *string `json:"domain"`
}

func (h *Routes) patch(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, id, err := h.load(r)
	if err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	if err := refuseSuspended(a); err != nil {
		return err
	}
	var b patchBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var out tenantq.CpSite
	changed := false
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		old, err := getSite(r, tx, id)
		if err != nil {
			return err
		}
		var f tenants.Fields
		if b.Domain != nil {
			f.Add("domain", httpx.FieldInvalidFormat)
		}
		var name *string
		if b.Name != nil {
			v := tenants.Text(&f, "name", *b.Name, 1, 100)
			name = &v
		}
		if b.Origins != nil {
			CheckOrigins(&f, b.Origins, old.Domain)
		}
		if err := f.Err(); err != nil {
			return err
		}
		if old.Status == StatusRemoved {
			return httpx.Conflict(httpx.CodeSiteRemoved, "the site is removed")
		}
		// keeping campaign parameters changes the site's privacy position
		// (cnil's consent exemption), so it is the operator's decision (audit
		// N-7); members may still send the value unchanged with other fields
		if b.CollectUTM != nil && *b.CollectUTM != old.CollectUtm {
			if err := a.RequireOwner(); err != nil {
				return err
			}
		}
		detail := map[string]any{"tenant_id": a.TenantID}
		if name != nil && *name != old.Name {
			detail["name"] = audit.Change(old.Name, *name)
		}
		if b.Origins != nil && !slices.Equal(b.Origins, old.Origins) {
			detail["origins"] = audit.Change(old.Origins, b.Origins)
		}
		if b.CollectUTM != nil && *b.CollectUTM != old.CollectUtm {
			detail["collect_utm"] = audit.Change(old.CollectUtm, *b.CollectUTM)
		}
		if len(detail) == 1 {
			out = old
			return nil
		}
		out, err = q.UpdateSite(ctx, tenantq.UpdateSiteParams{Name: name, Origins: b.Origins, CollectUtm: b.CollectUTM, ID: id})
		if err != nil {
			return err
		}
		changed = true
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.SiteUpdate, Target: audit.SiteTarget(id), Detail: detail,
		})
	})
	if err != nil {
		return err
	}
	if changed {
		h.s.Reconcile(ctx, a.TenantID)
	}
	httpx.WriteJSON(w, http.StatusOK, ToSite(out, a.TenantStatus))
	return nil
}

func (h *Routes) remove(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, id, err := h.load(r)
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	var out tenantq.CpSite
	changed := false
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		old, err := getSite(r, tx, id)
		if err != nil {
			return err
		}
		if old.Status == StatusRemoved {
			// repeating the call changes nothing (contract 4.3.5)
			out = old
			return nil
		}
		out, err = tenantq.New(tx).RemoveSite(ctx, id)
		if err != nil {
			return err
		}
		changed = true
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.SiteDelete, Target: audit.SiteTarget(id),
			Detail: map[string]any{"tenant_id": a.TenantID, "domain": old.Domain, "status": audit.Change(old.Status, StatusRemoved)},
		})
	})
	if err != nil {
		return err
	}
	if changed {
		h.s.Reconcile(ctx, a.TenantID)
	}
	httpx.WriteJSON(w, http.StatusAccepted, ToSite(out, a.TenantStatus))
	return nil
}
