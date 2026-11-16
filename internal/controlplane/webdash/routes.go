// Package webdash serves a project's web dashboard in the console
// (docs/reference/control-plane-api.md section 18): totals, daily series,
// breakdowns, the hours of one day and the visitors right now, for the whole
// project or one site, under /api/tenants/{tenant_id}/web/.
//
// every number has one source. totals, daily series and the referrer,
// country, browser, os and device breakdowns are read from agg.web_daily and
// agg.web_daily_dim under cp_tenant, the rows the operator views read, so a
// member and an operator never see different numbers for the same days.
// what the aggregates do not hold (pages, entry and exit pages, custom
// events, utm values, hours and the last minutes) is read from umami on the
// private network as the member's own umami user (package umamiread), so
// umami's team check guards those reads a second time. one more engine
// route lists a site's sessions of one day, so a member can find the session
// id the erasure of one session takes (contract 10.2).
package webdash

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/umamiread"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// the two bands of a site (docs/reference/umami-contract.md item 14)
const (
	BandPrimary    = "primary"
	BandQuarantine = "quarantine"
)

// Reader reads a website route as a member's umami user. *umamiread.Reader
// implements it.
type Reader interface {
	Read(ctx context.Context, user uuid.UUID, w umamiread.Website, sub string, q url.Values, out any) error
}

// Routes are the web dashboard routes.
type Routes struct {
	s   *tenants.Scope
	um  Reader
	now func() time.Time
	lim *limiter
}

// NewRoutes needs the tenant scope, the umami reader and a clock.
func NewRoutes(s *tenants.Scope, um Reader, now func() time.Time) (*Routes, error) {
	if s == nil || um == nil || now == nil {
		return nil, errors.New("webdash: scope, umami reader and clock are required")
	}
	return &Routes{s: s, um: um, now: now, lim: &limiter{counts: map[uuid.UUID]*window{}}}, nil
}

const base = "/tenants/{tenant_id}/web"

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodGet, base+"/summary", httpx.HandlerFunc(h.summary))
	r.Method(http.MethodGet, base+"/breakdown", httpx.HandlerFunc(h.breakdown))
	r.Method(http.MethodGet, base+"/hourly", httpx.HandlerFunc(h.hourly))
	r.Method(http.MethodGet, base+"/realtime", httpx.HandlerFunc(h.realtime))
	r.Method(http.MethodGet, base+"/sessions", httpx.HandlerFunc(h.sessions))
}

// engine rate limit: umami reads cost umami a query per site, so a member
// gets EngineLimit of them per minute across the engine routes
const (
	EngineLimit  = 120
	engineWindow = time.Minute
)

type window struct {
	start time.Time
	n     int
}

type limiter struct {
	mu     sync.Mutex
	counts map[uuid.UUID]*window
}

// allow counts one request of user and reports whether it is within the
// limit, with the time until the window ends.
func (l *limiter) allow(user uuid.UUID, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// drop finished windows, so the map holds only users active in the last minute
	for id, w := range l.counts {
		if now.Sub(w.start) >= engineWindow {
			delete(l.counts, id)
		}
	}
	w := l.counts[user]
	if w == nil {
		w = &window{start: now}
		l.counts[user] = w
	}
	w.n++
	return w.n <= EngineLimit, engineWindow - now.Sub(w.start)
}

// site is one site of the project as the dashboard needs it.
type site struct {
	ID          uuid.UUID
	Name        string
	Domain      string
	Status      string
	Quarantine  uuid.NullUUID
	CollectsUTM bool
}

// live reports whether umami has the site's websites: a removed site's
// websites are gone and a site in review never got any (contract 5.6).
func (s site) live() bool { return s.Status != "removed" && s.Status != "pending" }

// website returns the umami website of the band, false when the site has
// none for it.
func (s site) website(band string) (string, bool) {
	if band == BandQuarantine {
		if !s.Quarantine.Valid {
			return "", false
		}
		return s.Quarantine.UUID.String(), true
	}
	return s.ID.String(), true
}

// request is a parsed request: the verified membership, the query, the
// selected site (nil for the whole project) and the band.
type request struct {
	access tenants.Access
	q      url.Values
	sites  []site
	site   *site
	band   string
}

// allowedParams refuses any parameter not in allowed, and a repeated one
func allowedParams(q url.Values, allowed ...string) error {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for k, v := range q {
		if !ok[k] {
			return httpx.BadQuery("unknown parameter " + k)
		}
		if len(v) != 1 {
			return httpx.BadQuery(k + " must be given once")
		}
	}
	return nil
}

// parse checks membership first, so a non-member gets 404 whatever the
// query holds and cannot probe for validation answers; then it reads the
// project's sites under cp_tenant and resolves the site parameter among
// them only.
func (h *Routes) parse(r *http.Request, allowed ...string) (request, error) {
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return request{}, err
	}
	// r.URL.Query drops pairs it cannot parse without a word; a dropped
	// site filter would widen the answer, so refuse
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return request{}, httpx.BadQuery("the query string is malformed")
	}
	if err := allowedParams(q, allowed...); err != nil {
		return request{}, err
	}
	req := request{access: a, q: q, band: BandPrimary}
	if v, ok := q["band"]; ok {
		switch v[0] {
		case BandPrimary, BandQuarantine:
			req.band = v[0]
		default:
			return request{}, httpx.BadQuery("band must be primary or quarantine")
		}
	}
	var siteID *uuid.UUID
	if v, ok := q["site"]; ok {
		id, err := uuid.Parse(v[0])
		if err != nil || id.String() != v[0] {
			return request{}, httpx.BadQuery("site must be a site id")
		}
		siteID = &id
	}
	if err := h.s.InTenant(r.Context(), a, func(tx pgx.Tx) error {
		rows, err := tenantq.New(tx).WebDashSites(r.Context())
		if err != nil {
			return fmt.Errorf("webdash: sites: %w", err)
		}
		for _, row := range rows {
			req.sites = append(req.sites, site{
				ID: row.ID, Name: row.Name, Domain: row.Domain, Status: row.Status,
				Quarantine: row.QuarantineWebsiteID, CollectsUTM: row.CollectUtm,
			})
		}
		return nil
	}); err != nil {
		return request{}, err
	}
	if siteID != nil {
		for i := range req.sites {
			if req.sites[i].ID == *siteID {
				req.site = &req.sites[i]
			}
		}
		// a site of another tenant is invisible under cp_tenant: the same
		// 404 as an unknown id
		if req.site == nil {
			return request{}, httpx.NotFound()
		}
	}
	return req, nil
}

// siteFilter is the site parameter of the aggregate queries.
func (req request) siteFilter() uuid.NullUUID {
	if req.site == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: req.site.ID, Valid: true}
}

// engineSites are the live sites an engine read covers: the selected site,
// or every live site of the project.
func (req request) engineSites() []site {
	if req.site != nil {
		if !req.site.live() {
			return nil
		}
		return []site{*req.site}
	}
	var out []site
	for _, s := range req.sites {
		if s.live() {
			out = append(out, s)
		}
	}
	return out
}

// engine checks the per-user limit of the umami-backed routes.
func (h *Routes) engine(req request) error {
	ok, wait := h.lim.allow(req.access.User.ID, h.now())
	if !ok {
		return httpx.RateLimited(wait)
	}
	return nil
}

// read reads one website route of s as the member's umami user and maps
// its failures to answers.
func (h *Routes) read(ctx context.Context, req request, s site, websiteID, sub string, q url.Values, out any) error {
	err := h.um.Read(ctx, req.access.User.ID, umamiread.Website{Tenant: req.access.TenantID, Site: s.ID, ID: websiteID}, sub, q, out)
	if err == nil {
		return nil
	}
	if code, ok := umamiread.FailureCode(err); ok {
		switch code {
		case umamiread.CodeNotFound:
			// the site or the membership went away between the checks
			return httpx.NotFound()
		case umamiread.CodeProvisioningPending:
			return httpx.Conflict(CodeProvisioningPending, "web analytics for this site are still being set up; try again in a few minutes")
		}
		return httpx.Unavailable("web analytics are unreachable right now; try again shortly")
	}
	var ue *umamiread.UmamiError
	if errors.As(err, &ue) && ue.Status() == 0 && ctx.Err() == nil {
		return httpx.Unavailable("web analytics are unreachable right now; try again shortly")
	}
	if errors.As(err, &ue) && ue.Status() >= 500 {
		return httpx.Unavailable("web analytics are unreachable right now; try again shortly")
	}
	// a 4xx from umami for a checked site and fixed parameters is a fault
	// here, not the member's
	return fmt.Errorf("webdash: umami %s of site %s: %w", sub, s.ID, err)
}

// CodeProvisioningPending is the 409 code while umami does not yet hold the
// member's user or team for a site.
const CodeProvisioningPending = "provisioning_pending"

// SiteRef is a site in an answer.
type SiteRef struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Domain string    `json:"domain"`
	Status string    `json:"status"`
}

func (s site) ref() SiteRef {
	return SiteRef{ID: s.ID, Name: s.Name, Domain: s.Domain, Status: s.Status}
}

func refs(ss []site) []SiteRef {
	out := make([]SiteRef, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ref())
	}
	return out
}
