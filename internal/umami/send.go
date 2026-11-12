package umami

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// EdgeClientIPHeader is the header umami reads the client address from. it
// must equal CLIENT_IP_HEADER in umami's environment.
const EdgeClientIPHeader = "x-edge-client-ip"

// EdgeClientIP is the only address ever sent to umami. it is TEST-NET-1
// (rfc 5737), so no real or derived address reaches umami. umami needs a
// non-local address taken from a request header before it honours the
// country header (src/lib/detect.ts getLocation), and the bundled geolite
// database holds no entry for it, so without a country header the session
// gets no country (docs/reference/umami-contract.md).
const EdgeClientIP = "192.0.2.1"

// CountryHeader carries the iso 3166-1 alpha-2 country the gateway resolved.
// umami reads cf-ipcountry first among the provider headers it knows.
const CountryHeader = "cf-ipcountry"

// CacheHeader carries the cache token of the previous answer, which keeps the
// visit id (src/app/api/send/route.ts).
const CacheHeader = "x-umami-cache"

// Event is the payload of an umami "event" collection call. Name empty means
// a pageview; set means a custom event.
type Event struct {
	Website  string         `json:"website"`
	Hostname string         `json:"hostname,omitempty"`
	URL      string         `json:"url,omitempty"`
	Referrer string         `json:"referrer,omitempty"`
	Title    string         `json:"title,omitempty"`
	Screen   string         `json:"screen,omitempty"`
	Language string         `json:"language,omitempty"`
	Name     string         `json:"name,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	// Timestamp is unix seconds. umami stores the event and a new session at
	// this time and skips its 30 minute visit expiry when it is set.
	Timestamp int64 `json:"timestamp"`
	// Salt is the visitor token. with ENABLE_CLIENT_SALT=1 umami mixes it into
	// the session salt. umami caps it at 100 characters.
	Salt string `json:"salt,omitempty"`
}

// SendRequest is one call to POST /api/send.
type SendRequest struct {
	Event Event
	// UserAgent is the browser's original user agent. umami runs its bot check
	// and browser, os and device detection on it.
	UserAgent string
	// Country is an iso alpha-2 code, empty when unknown.
	Country string
	// Cache is the cache token from the previous answer of the same visit,
	// empty to let umami derive the visit from the session and the utc hour.
	Cache string
}

// SendResult is umami's answer to a collection call.
type SendResult struct {
	// Bot is true when umami classified the user agent as a bot. it answers
	// 200 {"beep":"boop"} and stores nothing.
	Bot       bool
	Cache     string
	SessionID string
	VisitID   string
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// Send posts one event to umami. it needs no session token: /api/send is
// unauthenticated, which is why it must only be reachable on the private
// network. a 4xx or 5xx answer is returned as *APIError so the caller can
// tell permanent rejections from transient failures.
func (c *Client) Send(ctx context.Context, r SendRequest) (SendResult, error) {
	e := r.Event
	switch {
	case e.Website == "":
		return SendResult{}, errors.New("umami send: event has no website id")
	case e.Timestamp <= 0:
		// without a timestamp umami uses its own clock, which would put a
		// replayed backlog into the wrong time buckets
		return SendResult{}, errors.New("umami send: event has no timestamp")
	case r.UserAgent == "":
		// go's default user agent would be classified by umami's bot check
		return SendResult{}, errors.New("umami send: no user agent")
	case len(e.Salt) > 100:
		return SendResult{}, errors.New("umami send: salt longer than 100 characters")
	case r.Country != "" && !countryRe.MatchString(r.Country):
		return SendResult{}, fmt.Errorf("umami send: country %q is not an upper-case iso alpha-2 code", r.Country)
	}

	hdr := http.Header{}
	hdr.Set("User-Agent", r.UserAgent)
	hdr.Set(EdgeClientIPHeader, EdgeClientIP)
	if r.Country != "" {
		hdr.Set(CountryHeader, r.Country)
	}
	if r.Cache != "" {
		hdr.Set(CacheHeader, r.Cache)
	}
	in := struct {
		Type    string `json:"type"`
		Payload Event  `json:"payload"`
	}{Type: "event", Payload: e}

	var raw json.RawMessage
	if err := c.doHeaders(ctx, "send", http.MethodPost, "/api/send", Token{}, hdr, in, &raw); err != nil {
		return SendResult{}, err
	}
	var out struct {
		Beep      string `json:"beep"`
		Cache     string `json:"cache"`
		SessionID string `json:"sessionId"`
		VisitID   string `json:"visitId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return SendResult{}, fmt.Errorf("umami send: decode response: %w", err)
	}
	if out.Beep != "" {
		return SendResult{Bot: true}, nil
	}
	if out.Cache == "" || out.SessionID == "" || out.VisitID == "" {
		return SendResult{}, errors.New("umami send: 200 answer without cache, sessionId or visitId")
	}
	return SendResult{Cache: out.Cache, SessionID: out.SessionID, VisitID: out.VisitID}, nil
}
