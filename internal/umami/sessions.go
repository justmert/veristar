package umami

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// SessionDetail is the part of GET /api/websites/<id>/sessions/<sessionId>
// the erasure workflow reads (src/queries/sql/sessions/getWebsiteSession.ts):
// the first and last event time and the event counts of the session.
type SessionDetail struct {
	ID        string    `json:"id"`
	WebsiteID string    `json:"websiteId"`
	FirstAt   time.Time `json:"firstAt"`
	LastAt    time.Time `json:"lastAt"`
	// Views counts page views, Events custom events. umami returns both as
	// numbers or numeric strings depending on the driver, so they are read
	// through flexInt.
	Views  flexInt `json:"views"`
	Events flexInt `json:"events"`
}

// flexInt decodes a json number or a string holding one. postgresql sums
// come back from umami's raw queries as strings.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("umami: %q is not an integer", s)
		}
		*f = flexInt(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("umami: %s is not an integer", b)
	}
	*f = flexInt(n)
	return nil
}

// Int64 returns the value.
func (f flexInt) Int64() int64 { return int64(f) }

// GetSession calls GET /api/websites/<id>/sessions/<sessionId> as the admin.
// ok is false when umami answers 404, which it does for an unknown session
// and for a session that has no event left: the route joins session rows
// with their events.
func (a *Admin) GetSession(ctx context.Context, websiteID, sessionID string) (d SessionDetail, ok bool, err error) {
	if websiteID == "" || sessionID == "" {
		return d, false, errors.New("umami get session: website id and session id are required")
	}
	err = a.call(ctx, "get session", http.MethodGet,
		"/api/websites/"+url.PathEscape(websiteID)+"/sessions/"+url.PathEscape(sessionID), nil, &d)
	if errors.Is(err, ErrNotFound) {
		return SessionDetail{}, false, nil
	}
	if err != nil {
		return SessionDetail{}, false, err
	}
	if d.ID != sessionID || d.WebsiteID != websiteID {
		return SessionDetail{}, false, errors.New("umami get session: response is for another session")
	}
	return d, true, nil
}

// SessionTotals is the answer of GET /api/websites/<id>/sessions/stats
// (src/queries/sql/sessions/getWebsiteSessionStats.ts): pageviews, distinct
// sessions with an event (visitors), visits, countries and custom events in
// the range, each wrapped as {"value": n}.
type SessionTotals struct {
	Pageviews int64
	Visitors  int64
	Visits    int64
	Events    int64
}

// SessionStats calls GET /api/websites/<id>/sessions/stats as the admin.
func (a *Admin) SessionStats(ctx context.Context, websiteID string, r Range) (SessionTotals, error) {
	q, err := r.values()
	if err != nil {
		return SessionTotals{}, err
	}
	if websiteID == "" {
		return SessionTotals{}, errors.New("umami session stats: no website id")
	}
	var out map[string]struct {
		Value int64 `json:"value"`
	}
	path := "/api/websites/" + url.PathEscape(websiteID) + "/sessions/stats?" + q.Encode()
	if err := a.call(ctx, "session stats", http.MethodGet, path, nil, &out); err != nil {
		return SessionTotals{}, err
	}
	for _, k := range []string{"pageviews", "visitors", "visits", "events"} {
		if _, ok := out[k]; !ok {
			return SessionTotals{}, fmt.Errorf("umami session stats: answer has no %q", k)
		}
	}
	return SessionTotals{
		Pageviews: out["pageviews"].Value, Visitors: out["visitors"].Value,
		Visits: out["visits"].Value, Events: out["events"].Value,
	}, nil
}
