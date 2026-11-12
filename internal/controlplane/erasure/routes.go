package erasure

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/db/tenantq"
)

// maxUserAgent bounds the user agent of a visitor_today request. browsers
// send a few hundred bytes; the limit only keeps the body sane.
const maxUserAgent = 2048

// Request is the erasure request object of control-plane-api 10.1.
type Request struct {
	ID                 uuid.UUID        `json:"id"`
	TenantID           uuid.UUID        `json:"tenant_id"`
	SiteID             uuid.UUID        `json:"site_id"`
	Kind               string           `json:"kind"`
	Status             string           `json:"status"`
	Outcome            *string          `json:"outcome"`
	Message            *string          `json:"message"`
	Day                *string          `json:"day"`
	SessionID          *uuid.UUID       `json:"session_id"`
	RequestedBy        uuid.UUID        `json:"requested_by"`
	RequestedAt        httpx.Timestamp  `json:"requested_at"`
	CompletedAt        *httpx.Timestamp `json:"completed_at"`
	SessionsDeleted    int64            `json:"sessions_deleted"`
	EventsDeleted      int64            `json:"events_deleted"`
	DLQMessagesDeleted int64            `json:"dlq_messages_deleted"`
	AffectedFrom       *string          `json:"affected_from"`
	AffectedTo         *string          `json:"affected_to"`
	ReaggregatedAt     *httpx.Timestamp `json:"reaggregated_at"`
	Error              *string          `json:"error"`
}

func dateText(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.DateOnly)
	return &s
}

func toRequest(r tenantq.CpErasureRequest) Request {
	out := Request{
		ID: r.ID, TenantID: r.TenantID, SiteID: r.SiteID, Kind: r.Kind, Status: r.Status,
		Outcome: r.Outcome, Message: r.Message, Day: dateText(r.Day), RequestedBy: r.RequestedBy,
		RequestedAt: httpx.Timestamp(r.RequestedAt), CompletedAt: httpx.TimestampPtr(r.CompletedAt),
		SessionsDeleted: r.SessionsDeleted, EventsDeleted: r.EventsDeleted, DLQMessagesDeleted: r.DlqMessagesDeleted,
		AffectedFrom: dateText(r.AffectedFrom), AffectedTo: dateText(r.AffectedTo),
		ReaggregatedAt: httpx.TimestampPtr(r.ReaggregatedAt), Error: r.Error,
	}
	if r.SessionID.Valid {
		id := r.SessionID.UUID
		out.SessionID = &id
	}
	return out
}

// Routes are the erasure routes.
type Routes struct {
	s   *tenants.Scope
	svc *Service
}

// NewRoutes returns the routes on a scope and a service.
func NewRoutes(s *tenants.Scope, svc *Service) (*Routes, error) {
	if s == nil || svc == nil {
		return nil, errors.New("erasure: scope and service are required")
	}
	return &Routes{s: s, svc: svc}, nil
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodPost, "/tenants/{tenant_id}/erasure", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/erasure", httpx.HandlerFunc(h.list))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/erasure/{request_id}", httpx.HandlerFunc(h.get))
}

// createBody is the POST body. ip and user_agent belong to visitor_today,
// session_id to session, confirm_domain to site_reset.
type createBody struct {
	Kind          *string `json:"kind"`
	SiteID        *string `json:"site_id"`
	IP            *string `json:"ip"`
	UserAgent     *string `json:"user_agent"`
	SessionID     *string `json:"session_id"`
	ConfirmDomain *string `json:"confirm_domain"`
}

// validate checks the body for its kind. it never echoes ip or user_agent.
func (b createBody) validate() (kind string, site, session uuid.UUID, err error) {
	var f tenants.Fields
	if b.Kind == nil {
		f.Add("kind", httpx.FieldRequired)
	} else {
		kind = *b.Kind
		if kind != KindVisitorToday && kind != KindSession && kind != KindSiteReset {
			f.Add("kind", httpx.FieldInvalidValue)
		}
	}
	if b.SiteID == nil {
		f.Add("site_id", httpx.FieldRequired)
	} else if id, perr := uuid.Parse(*b.SiteID); perr != nil || id.String() != *b.SiteID {
		f.Add("site_id", httpx.FieldInvalidFormat)
	} else {
		site = id
	}
	// a field of another kind is refused, so a request never carries an
	// address it does not need
	forbid := func(path string, v *string, allowed bool) {
		if v != nil && !allowed {
			f.Add(path, httpx.FieldInvalidValue)
		}
	}
	forbid("ip", b.IP, kind == KindVisitorToday)
	forbid("user_agent", b.UserAgent, kind == KindVisitorToday)
	forbid("session_id", b.SessionID, kind == KindSession)
	forbid("confirm_domain", b.ConfirmDomain, kind == KindSiteReset)
	switch kind {
	case KindVisitorToday:
		if b.IP == nil {
			f.Add("ip", httpx.FieldRequired)
		} else if a, perr := netip.ParseAddr(strings.TrimSpace(*b.IP)); perr != nil || a.Zone() != "" {
			f.Add("ip", httpx.FieldInvalidFormat)
		}
		switch {
		case b.UserAgent == nil || *b.UserAgent == "":
			f.Add("user_agent", httpx.FieldRequired)
		case len(*b.UserAgent) > maxUserAgent:
			f.Add("user_agent", httpx.FieldTooLong)
		case strings.ContainsFunc(*b.UserAgent, unicode.IsControl):
			f.Add("user_agent", httpx.FieldInvalidFormat)
		}
	case KindSession:
		if b.SessionID == nil {
			f.Add("session_id", httpx.FieldRequired)
		} else if id, perr := uuid.Parse(*b.SessionID); perr != nil || id.String() != *b.SessionID {
			f.Add("session_id", httpx.FieldInvalidFormat)
		} else {
			session = id
		}
	case KindSiteReset:
		if b.ConfirmDomain == nil {
			f.Add("confirm_domain", httpx.FieldRequired)
		}
	}
	return kind, site, session, f.Err()
}

func (h *Routes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	// erasure is never blocked by unaccepted terms or a suspension: data
	// subject rights do not wait for either
	if err := a.RequireOwner(); err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	kind, siteID, sessionID, err := b.validate()
	if err != nil {
		return err
	}

	var site tenantq.CpSite
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		site, qerr = tenantq.New(tx).GetSite(ctx, siteID)
		if errors.Is(qerr, pgx.ErrNoRows) {
			return httpx.Validation("site_id is not a site of this project", map[string]string{"site_id": httpx.FieldInvalidValue})
		}
		return qerr
	}); err != nil {
		return err
	}
	if site.Status == "removed" {
		return httpx.Conflict(httpx.CodeSiteRemoved, "the site is removed; its web data was deleted with it")
	}
	if !site.QuarantineWebsiteID.Valid {
		return &httpx.Error{Status: http.StatusServiceUnavailable, Code: httpx.CodeProvisioningPending,
			Message: "the site's umami websites are not created yet, so it holds no web data", RetryAfter: 10 * time.Second}
	}
	if kind == KindSiteReset && *b.ConfirmDomain != site.Domain {
		return httpx.Validation("confirm_domain must equal the site's domain", map[string]string{"confirm_domain": httpx.FieldInvalidValue})
	}

	j := job{quarantine: site.QuarantineWebsiteID.UUID.String(), siteFrom: site.CreatedAt}
	var day *time.Time
	if kind == KindVisitorToday {
		vt, d, terr := visitorToken(ctx, h.svc.cfg.NC, siteID.String(), *b.IP, *b.UserAgent)
		// the address and user agent are not needed past this point
		b.IP, b.UserAgent = nil, nil
		if errors.Is(terr, errNoGateway) {
			return httpx.Unavailable("no ingestion gateway answered; try again shortly")
		}
		if terr != nil {
			httpx.Logger(ctx).Error("erasure visitor token unavailable", "error", terr.Error())
			return httpx.Unavailable("the ingestion gateways could not compute the visitor token; try again shortly")
		}
		j.vt, day = vt, &d
	}
	// every event accepted before this point is at or below snap
	st, err := h.svc.bind(ctx)
	if err == nil {
		j.snap, err = lastSeq(ctx, st.events)
	}
	if err != nil {
		httpx.Logger(ctx).Error("erasure queue position unavailable", "error", err.Error())
		return httpx.Unavailable("the event queue is unreachable; try again shortly")
	}

	var row tenantq.CpErasureRequest
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		p := tenantq.InsertErasureRequestParams{SiteID: siteID, Kind: kind, RequestedBy: a.User.ID, Day: day}
		if kind == KindSession {
			p.SessionID = uuid.NullUUID{UUID: sessionID, Valid: true}
		}
		var ierr error
		row, ierr = tenantq.New(tx).InsertErasureRequest(ctx, p)
		var pgErr *pgconn.PgError
		if errors.As(ierr, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "erasure_requests_one_active_idx" {
			return httpx.Conflict(httpx.CodeErasureInProgress, "another erasure request for this site is running")
		}
		if ierr != nil {
			return ierr
		}
		detail := map[string]any{"tenant_id": a.TenantID, "site_id": siteID, "kind": kind}
		if kind == KindSession {
			detail["session_id"] = sessionID
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.ErasureRequest, Target: audit.ErasureTarget(row.ID), Detail: detail,
		})
	})
	if err != nil {
		return err
	}
	j.req = systemq.CpErasureRequest(row)
	// the run outlives the request on purpose: it is answered with 202 now
	h.svc.start(j) //nolint:contextcheck // see above
	httpx.WriteJSON(w, http.StatusAccepted, toRequest(row))
	return nil
}

const listRoute = "erasure"

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
	params := tenantq.ListErasureRequestsPageParams{RowLimit: int32(p.Limit + 1)} //nolint:gosec // limit is at most 200
	if v, ok := r.URL.Query()["site_id"]; ok {
		id, perr := uuid.Parse(v[0])
		if len(v) != 1 || perr != nil {
			return httpx.BadQuery("site_id must be one uuid")
		}
		params.SiteID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if p.After != nil {
		params.BeforeRequestedAt = &p.After.At
		params.BeforeID = uuid.NullUUID{UUID: p.After.ID, Valid: true}
	}
	var rows []tenantq.CpErasureRequest
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = tenantq.New(tx).ListErasureRequestsPage(ctx, params)
		return qerr
	}); err != nil {
		return err
	}
	out := httpx.List[Request]{Items: []Request{}}
	for i, e := range rows {
		if i == p.Limit {
			last := rows[i-1]
			out.NextCursor = httpx.NextCursor(listRoute, httpx.Cursor{At: last.RequestedAt, ID: last.ID})
			break
		}
		out.Items = append(out.Items, toRequest(e))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "request_id"))
	if err != nil {
		return err
	}
	var row tenantq.CpErasureRequest
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		row, qerr = tenantq.New(tx).GetErasureRequest(ctx, id)
		if errors.Is(qerr, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		return qerr
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toRequest(row))
	return nil
}
