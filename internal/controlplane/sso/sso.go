// Package sso serves GET /api/sso/umami (docs/reference/control-plane-api.md
// 4.4.1, the design): it checks that the signed-in user is a member of the
// site's tenant, logs in to umami on the private network as that user's
// umami user with the derived password of the day, and sends the browser to
// umami's /sso page with the token. it never sets a password, so a second
// sso click (a second tab) does not log the first one out.
package sso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/umami"
)

// failure codes of contract 4.4.1, sent to /sso-error
const (
	CodeNotFound            = "not_found"
	CodeCrossSite           = "cross_site"
	CodeProvisioningPending = "provisioning_pending"
	CodeUmamiUnavailable    = "umami_unavailable"
	CodeInternal            = "internal"
	CodeRateLimited         = "rate_limited"
)

// Path is the route of the handler.
const Path = "/api/sso/umami"

// rate limit of contract 4.4.1: 30 sso redirects per user per minute
const (
	rateLimit  = 30
	rateWindow = time.Minute
)

// SystemTx runs fn in a cp_system transaction (db.WithSystem).
type SystemTx func(ctx context.Context, fn func(pgx.Tx) error) error

// Handler is the sso route. all fields are required.
type Handler struct {
	// Tx reads the site, the membership and the password day as cp_system.
	Tx SystemTx
	// Umami is the client on the private network; login needs no admin.
	Umami *umami.Client
	Key   umami.PasswordKey
	// WebOrigin is https://web.<BASE_DOMAIN>, without a trailing slash.
	WebOrigin string
	// UserID returns the signed-in user of the request.
	UserID func(*http.Request) (uuid.UUID, bool)
	Now    func() time.Time

	mu     sync.Mutex
	counts map[uuid.UUID]*window
}

type window struct {
	start time.Time
	n     int
}

// Check reports a missing field, so a misconfigured server stops at startup.
func (h *Handler) Check() error {
	if h.Tx == nil || h.Umami == nil || h.Key.Empty() || h.UserID == nil || h.Now == nil {
		return errors.New("sso: handler needs Tx, Umami, Key, UserID and Now")
	}
	u, err := url.Parse(h.WebOrigin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return errors.New("sso: WebOrigin must be a bare origin such as https://web.example.org")
	}
	return nil
}

// allow counts one request of user against the per-minute limit.
func (h *Handler) allow(user uuid.UUID, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts == nil {
		h.counts = map[uuid.UUID]*window{}
	}
	// drop finished windows, so the map holds only users active in the
	// last minute
	for id, w := range h.counts {
		if now.Sub(w.start) >= rateWindow {
			delete(h.counts, id)
		}
	}
	w := h.counts[user]
	if w == nil {
		w = &window{start: now}
		h.counts[user] = w
	}
	w.n++
	return w.n <= rateLimit
}

// failure is an sso outcome that sends the browser to the console's
// /sso-error page.
type failure struct {
	code string
	err  error
}

func (f *failure) Error() string {
	if f.err != nil {
		return f.code + ": " + f.err.Error()
	}
	return f.code
}

func fail(code string, err error) error { return &failure{code: code, err: err} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpx.MethodNotAllowedHandler(w, r)
		return
	}
	user, ok := h.UserID(r)
	if !ok {
		httpx.WriteError(w, r, httpx.Unauthenticated())
		return
	}
	siteParam := r.URL.Query().Get("site")
	target, err := h.redirect(r, user, siteParam)
	if err != nil {
		var f *failure
		if !errors.As(err, &f) {
			f = &failure{code: CodeInternal, err: err}
		}
		if f.code == CodeInternal || f.code == CodeUmamiUnavailable {
			httpx.Logger(r.Context()).Error("sso failed", "code", f.code, "error", f.Error())
		}
		q := url.Values{"code": {f.code}}
		if id, err := uuid.Parse(siteParam); err == nil {
			q.Set("site", id.String())
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, "/sso-error?"+q.Encode(), http.StatusSeeOther)
		return
	}
	// the token sits in the query of this redirect: no cache may keep it
	// and no referer may carry it on
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // target is WebOrigin + /sso, built in redirect from a checked site id and umami's token
}

// redirect checks the request and returns the umami /sso url.
func (h *Handler) redirect(r *http.Request, user uuid.UUID, siteParam string) (string, error) {
	ctx := r.Context()
	// a browser sends Sec-Fetch-Site on navigations; the console link is
	// same-origin and a typed address is none. a link from another site is
	// refused, so it cannot open a dashboard in the user's name.
	if v := r.Header.Values("Sec-Fetch-Site"); len(v) > 1 || (len(v) == 1 && v[0] != "same-origin" && v[0] != "none") {
		return "", fail(CodeCrossSite, nil)
	}
	siteID, err := uuid.Parse(siteParam)
	if err != nil || siteID.String() != siteParam {
		return "", fail(CodeNotFound, nil)
	}
	view := r.URL.Query().Get("view")
	if view != "" && view != "primary" && view != "quarantine" {
		return "", fail(CodeNotFound, nil)
	}
	now := h.Now()
	if !h.allow(user, now) {
		return "", fail(CodeRateLimited, nil)
	}
	site, sess, err := h.siteSession(ctx, user, siteID, now)
	if err != nil {
		return "", err
	}
	websiteID := site.ID.String()
	if view == "quarantine" {
		if !site.QuarantineWebsiteID.Valid {
			return "", fail(CodeProvisioningPending, nil)
		}
		websiteID = site.QuarantineWebsiteID.UUID.String()
	}

	q := url.Values{}
	q.Set("token", sess.Token.Reveal())
	q.Set("url", "/websites/"+websiteID)
	return h.WebOrigin + "/sso?" + q.Encode(), nil
}

// Site is the part of a site a umami session was opened for.
type Site struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Status              string
	QuarantineWebsiteID uuid.NullUUID
}

// SiteSession logs in to umami as the umami user of user, for a site of a
// tenant user is a member of, exactly as the sso redirect does but without
// its per-minute limit: the mcp tools call umami as that user and keep
// their own limits. the error is a failure whose code FailureCode reads, or
// an internal error.
func (h *Handler) SiteSession(ctx context.Context, user, siteID uuid.UUID) (Site, umami.Session, error) {
	site, sess, err := h.siteSession(ctx, user, siteID, h.Now())
	if err != nil {
		return Site{}, umami.Session{}, err
	}
	return Site{ID: site.ID, TenantID: site.TenantID, Status: site.Status, QuarantineWebsiteID: site.QuarantineWebsiteID}, sess, nil
}

// FailureCode returns the failure code of an error from SiteSession, and
// false for an internal error.
func FailureCode(err error) (string, bool) {
	var f *failure
	if errors.As(err, &f) && f.code != CodeInternal {
		return f.code, true
	}
	return "", false
}

// siteSession checks the site and the membership and logs in.
func (h *Handler) siteSession(ctx context.Context, user, siteID uuid.UUID, now time.Time) (systemq.SSOSiteRow, umami.Session, error) {
	var site systemq.SSOSiteRow
	var githubID int64
	var rotatedDay int64
	err := h.Tx(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var err error
		if site, err = q.SSOSite(ctx, siteID); err != nil {
			return err
		}
		if githubID, err = q.SSOMember(ctx, systemq.SSOMemberParams{TenantID: site.TenantID, UserID: user}); err != nil {
			return err
		}
		rotatedDay, err = q.UmamiPasswordDay(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			// no rotation has completed yet
			rotatedDay, err = 0, nil
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// unknown site or not a member: the same answer, so ids reveal nothing
		return site, umami.Session{}, fail(CodeNotFound, nil)
	}
	if err != nil {
		return site, umami.Session{}, fmt.Errorf("sso: read site: %w", err)
	}
	if site.TenantStatus == "removed" {
		return site, umami.Session{}, fail(CodeNotFound, nil)
	}
	// a site in review (contract 5.6) is treated like a pending tenant: the
	// reconciler made no umami website for it, so there is nothing to open
	if site.Status == "removed" || site.Status == "pending" || !site.UmamiTeamID.Valid {
		return site, umami.Session{}, fail(CodeProvisioningPending, nil)
	}
	sess, err := h.login(ctx, user.String(), provision.Username(githubID), now, rotatedDay)
	if err != nil {
		return site, umami.Session{}, err
	}
	if sess.User.ID != user.String() {
		return site, umami.Session{}, fmt.Errorf("sso: umami user %s belongs to id %s", provision.Username(githubID), sess.User.ID)
	}
	// the reconciler adds the membership; until it has, the dashboard
	// would only show refusals
	team := site.UmamiTeamID.UUID.String()
	member := false
	for _, t := range sess.User.Teams {
		member = member || t.ID == team
	}
	if !member {
		return site, umami.Session{}, fail(CodeProvisioningPending, nil)
	}
	return site, sess, nil
}

// login logs in with the derived password of today. until the umami-rotate
// task has finished today's rotation, users made before midnight still have
// yesterday's password, so then, and only then, yesterday's is tried after
// today's is refused. a user whose password is older than that is refused,
// which surfaces a rotation that stopped running.
func (h *Handler) login(ctx context.Context, userID, username string, now time.Time, rotatedDay int64) (umami.Session, error) {
	today := umami.EpochDay(now)
	days := []int64{today}
	if rotatedDay < today {
		days = append(days, today-1)
	}
	for _, day := range days {
		pw, err := h.Key.Password(userID, day)
		if err != nil {
			return umami.Session{}, err
		}
		sess, err := h.Umami.Login(ctx, username, pw)
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, umami.ErrUnauthorized) {
			return umami.Session{}, fail(CodeUmamiUnavailable, err)
		}
	}
	// no such user yet, or a password the reconciler and rotation have not
	// set: provisioning has not caught up with this user
	return umami.Session{}, fail(CodeProvisioningPending, nil)
}
