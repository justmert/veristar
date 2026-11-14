package umami

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// read routes of one website that the control plane's mcp tools call as a
// tenant's view-only user (umami v3.5.0, src/app/api/websites/[websiteId]).
// every one is a GET without side effects; anything not listed here is
// refused before a request is made, so a tool can never reach a write route
// or a route outside the website it checked.
var websiteReads = map[string]bool{
	"/stats":                 true, // route.ts of stats
	"/pageviews":             true,
	"/metrics":               true,
	"/active":                true,
	"/daterange":             true,
	"/events/stats":          true,
	"/events/series":         true,
	"/event-data/properties": true,
	"/event-data/stats":      true,
	"/event-data/values":     true,
	"/sessions":              true,
	"/sessions/stats":        true,
	"/funnels":               true,
	"/funnels/stats":         true,
	"/journeys":              true,
	"/retention":             true,
	"/goals":                 true,
}

// savedReports are the saved-report reads whose path carries a report id:
// /funnels/<id>/stats and /goals/<id>/stats. umami answers 404 unless the
// report belongs to the website in the path (getReport and the websiteId
// check in each route).
var savedReports = map[string]bool{"funnels": true, "goals": true}

// maxReadBody bounds a website read. session lists and journeys are the
// largest answers; the mcp tools cap their page sizes well below this.
const maxReadBody = 4 << 20

// ReadWebsite calls GET /api/websites/<websiteID><sub>?<q> with token and
// decodes the json answer into out. sub must be one of the read routes
// above, or /funnels/<uuid>/stats or /goals/<uuid>/stats.
func (c *Client) ReadWebsite(ctx context.Context, token Token, websiteID, sub string, q url.Values, out any) error {
	if token.Empty() {
		return errors.New("umami read: no session token")
	}
	if !readable(sub) {
		return fmt.Errorf("umami read: %q is not a website read route", sub)
	}
	p, err := websitePath(websiteID, sub)
	if err != nil {
		return err
	}
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return c.doLimit(ctx, "website read "+sub, http.MethodGet, p, token, maxReadBody, out)
}

func readable(sub string) bool {
	if websiteReads[sub] {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(sub, "/"), "/")
	return len(parts) == 3 && savedReports[parts[0]] && canonicalID(parts[1]) && parts[2] == "stats"
}

// canonicalID is a lowercase uuid, the only form a report id is passed in.
func canonicalID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}
