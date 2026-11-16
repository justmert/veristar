package umamiread

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/umami"
)

// SiteSessions opens a umami session as a user for a site of a tenant the
// user is a member of. *Sessions implements it.
type SiteSessions interface {
	SiteSession(ctx context.Context, user, siteID uuid.UUID) (Site, umami.Session, error)
}

// WebsiteReader reads a website route with a session token.
// *umami.Client implements it.
type WebsiteReader interface {
	ReadWebsite(ctx context.Context, token umami.Token, websiteID, sub string, q url.Values, out any) error
}

// sessionTTL is how long a umami login is reused for one user and site.
// logging in costs umami a password hash, and the daily password rotation
// ends a token anyway; a 401 drops the entry at once.
const sessionTTL = 5 * time.Minute

type cacheKey struct{ user, site uuid.UUID }

type cacheEntry struct {
	site    Site
	session umami.Session
	at      time.Time
}

// Reader reads website routes as a member's umami user and reuses a login
// for sessionTTL.
type Reader struct {
	sessions SiteSessions
	umami    WebsiteReader
	now      func() time.Time

	mu      sync.Mutex
	entries map[cacheKey]cacheEntry
}

// NewReader needs the sessions, the website reader and a clock.
func NewReader(s SiteSessions, um WebsiteReader, now func() time.Time) (*Reader, error) {
	if s == nil || um == nil || now == nil {
		return nil, errors.New("umamiread: a reader needs sessions, a website reader and a clock")
	}
	return &Reader{sessions: s, umami: um, now: now, entries: map[cacheKey]cacheEntry{}}, nil
}

func (r *Reader) get(k cacheKey) (cacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for key, e := range r.entries {
		if now.Sub(e.at) >= sessionTTL {
			delete(r.entries, key)
		}
	}
	e, ok := r.entries[k]
	return e, ok
}

func (r *Reader) put(k cacheKey, e cacheEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[k] = e
}

func (r *Reader) drop(k cacheKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, k)
}

// Website is one checked website of a site: the site's own id for the
// primary band, its quarantine website for the quarantine band.
type Website struct {
	Tenant uuid.UUID
	Site   uuid.UUID
	ID     string
}

// Read reads sub of website w as user's umami user. the session must have
// been opened for w's site of w's tenant, or the read is refused before
// umami is called. a 401 from umami (the daily rotation or a membership
// change ended the token) logs in once more. errors are failures
// (FailureCode), *UmamiError for umami's answers and transport, or internal.
func (r *Reader) Read(ctx context.Context, user uuid.UUID, w Website, sub string, q url.Values, out any) error {
	if w.ID == "" {
		return errors.New("umamiread: no website id")
	}
	k := cacheKey{user: user, site: w.Site}
	for attempt := 0; ; attempt++ {
		e, ok := r.get(k)
		if !ok {
			site, sess, err := r.sessions.SiteSession(ctx, user, w.Site)
			if err != nil {
				return err
			}
			if site.ID != w.Site || site.TenantID != w.Tenant {
				return fmt.Errorf("umamiread: umami session for site %s of tenant %s, asked for site %s of tenant %s",
					site.ID, site.TenantID, w.Site, w.Tenant)
			}
			e = cacheEntry{site: site, session: sess, at: r.now()}
			r.put(k, e)
		}
		if w.ID != e.site.ID.String() && (!e.site.QuarantineWebsiteID.Valid || w.ID != e.site.QuarantineWebsiteID.UUID.String()) {
			if ok && attempt == 0 {
				// the cached site may predate its quarantine website
				r.drop(k)
				continue
			}
			// a website id that is neither of the site's two websites never
			// reaches umami
			return fmt.Errorf("umamiread: website %s is not a website of site %s", w.ID, w.Site)
		}
		err := r.umami.ReadWebsite(ctx, e.session.Token, w.ID, sub, q, out)
		if errors.Is(err, umami.ErrUnauthorized) && attempt == 0 {
			r.drop(k)
			continue
		}
		if err != nil {
			return &UmamiError{Err: err}
		}
		return nil
	}
}

// UmamiError is a read that umami refused or that did not reach umami. any
// other error of Read is a failure (FailureCode) or internal.
type UmamiError struct{ Err error }

func (e *UmamiError) Error() string { return e.Err.Error() }

func (e *UmamiError) Unwrap() error { return e.Err }

// Status is umami's http status, or 0 when umami gave no answer.
func (e *UmamiError) Status() int {
	var ae *umami.APIError
	if errors.As(e.Err, &ae) {
		return ae.Status
	}
	return 0
}
