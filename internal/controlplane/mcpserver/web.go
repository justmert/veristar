package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane/sso"
	"github.com/justmert/veristar/internal/umami"
)

// web analytics tools read umami's rest api on the private network as the
// principal's own umami user, the one the console's sso opens. a site id
// from the client is first read through the console's site route as the
// principal, which answers only for a site of the principal's tenant; only
// then is the umami website id derived from it, so no website id a client
// sends ever reaches umami.

// sessionTTL is how long a umami login is reused for one user and site.
// logging in costs umami a password hash, and the daily password rotation
// ends a token anyway; a 401 drops the entry at once.
const sessionTTL = 5 * time.Minute

type webKey struct{ user, site uuid.UUID }

type webEntry struct {
	site    sso.Site
	session umami.Session
	at      time.Time
}

type webCache struct {
	mu      sync.Mutex
	entries map[webKey]webEntry
}

func newWebCache() *webCache { return &webCache{entries: map[webKey]webEntry{}} }

func (c *webCache) get(k webKey, now time.Time) (webEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if now.Sub(e.at) >= sessionTTL {
			delete(c.entries, key)
		}
	}
	e, ok := c.entries[k]
	return e, ok
}

func (c *webCache) put(k webKey, e webEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = e
}

func (c *webCache) drop(k webKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, k)
}

// siteRef is the part of the console's site object the web tools need.
type siteRef struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	Name                string     `json:"name"`
	Domain              string     `json:"domain"`
	Status              string     `json:"status"`
	QuarantineWebsiteID *uuid.UUID `json:"quarantine_website_id"`
	CollectUTM          bool       `json:"collect_utm"`
}

// website is one checked umami website of a site.
type website struct {
	site siteRef
	view string
	id   string
}

// resolveSite checks a client's site id through the console's site route.
func (s *Server) resolveSite(ctx context.Context, p Principal, rawSite, view string) (website, error) {
	siteID, err := parseUUID("site_id", rawSite)
	if err != nil {
		return website{}, err
	}
	switch view {
	case "":
		view = "primary"
	case "primary", "quarantine":
	default:
		return website{}, invalid("view must be primary or quarantine")
	}
	var site siteRef
	if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/sites/"+siteID.String(), nil, &site); err != nil {
		return website{}, err
	}
	if site.ID != siteID || site.TenantID != p.TenantID {
		return website{}, fmt.Errorf("mcpserver: site route answered site %s of tenant %s for %s", site.ID, site.TenantID, siteID)
	}
	switch site.Status {
	case "removed":
		return website{}, &toolError{Code: outcomeNotFound, Message: "this site was removed"}
	case "pending":
		return website{}, &toolError{Code: "provisioning_pending", Message: "this site is in review and has no analytics yet"}
	}
	w := website{site: site, view: view, id: site.ID.String()}
	if view == "quarantine" {
		if site.QuarantineWebsiteID == nil {
			return website{}, &toolError{Code: "provisioning_pending", Message: "the quarantine view of this site is not set up yet"}
		}
		w.id = site.QuarantineWebsiteID.String()
	}
	return w, nil
}

// umamiRead reads a website route of w as the principal's umami user.
func (s *Server) umamiRead(ctx context.Context, p Principal, w website, sub string, q url.Values, out any) error {
	k := webKey{user: p.User.ID, site: w.site.ID}
	for attempt := 0; ; attempt++ {
		e, ok := s.webCache.get(k, s.now())
		if !ok {
			site, sess, err := s.sessions.SiteSession(ctx, p.User.ID, w.site.ID)
			if err != nil {
				return umamiSessionError(err)
			}
			if site.ID != w.site.ID || site.TenantID != p.TenantID {
				return fmt.Errorf("mcpserver: umami session for site %s of tenant %s", site.ID, site.TenantID)
			}
			e = webEntry{site: site, session: sess, at: s.now()}
			s.webCache.put(k, e)
		}
		err := s.umami.ReadWebsite(ctx, e.session.Token, w.id, sub, q, out)
		if errors.Is(err, umami.ErrUnauthorized) && attempt == 0 {
			// the daily rotation or a membership change ended the token:
			// log in again once
			s.webCache.drop(k)
			continue
		}
		if err != nil {
			var ae *umami.APIError
			if errors.As(err, &ae) {
				switch {
				case ae.Status == 404:
					return &toolError{Code: outcomeNotFound, Message: "umami does not know this report for the site"}
				case ae.Status == 400:
					return &toolError{Code: outcomeInvalid, Message: "umami refused the parameters: " + ae.Message}
				case ae.Status == 401 || ae.Status == 403:
					return &toolError{Code: outcomeForbidden, Message: "umami refused access to this site for this user"}
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &toolError{Code: outcomeUnavailable, Message: "web analytics are unreachable right now; try again shortly"}
		}
		return nil
	}
}

// umamiSessionError maps the sso failure codes to tool errors.
func umamiSessionError(err error) error {
	code, ok := sso.FailureCode(err)
	if !ok {
		return err
	}
	switch code {
	case sso.CodeNotFound:
		return &toolError{Code: outcomeNotFound, Message: "not found: the site is unknown or not part of this project"}
	case sso.CodeProvisioningPending:
		return &toolError{Code: "provisioning_pending", Message: "web analytics for this site are still being set up; try again in a few minutes"}
	case sso.CodeUmamiUnavailable:
		return &toolError{Code: outcomeUnavailable, Message: "web analytics are unreachable right now; try again shortly"}
	}
	return fmt.Errorf("mcpserver: umami session: %s", code)
}
