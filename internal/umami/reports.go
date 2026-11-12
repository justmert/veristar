package umami

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Range is a closed time range for the read routes. umami takes startAt and
// endAt as unix milliseconds (src/lib/schema.ts dateRangeParams).
type Range struct {
	Start time.Time
	End   time.Time
}

func (r Range) values() (url.Values, error) {
	if r.Start.IsZero() || r.End.IsZero() || r.End.Before(r.Start) {
		return nil, errors.New("umami: range needs a start not after its end")
	}
	v := url.Values{}
	v.Set("startAt", strconv.FormatInt(r.Start.UnixMilli(), 10))
	v.Set("endAt", strconv.FormatInt(r.End.UnixMilli(), 10))
	return v, nil
}

// StatsValues are the totals umami reports for a range. totaltime is seconds
// summed over visits; average visit duration is totaltime / visits. bounce
// rate is bounces / visits.
type StatsValues struct {
	Pageviews int64 `json:"pageviews"`
	Visitors  int64 `json:"visitors"`
	Visits    int64 `json:"visits"`
	Bounces   int64 `json:"bounces"`
	Totaltime int64 `json:"totaltime"`
}

// Stats is the answer of /stats: the range and the previous range of equal length.
type Stats struct {
	StatsValues
	Comparison StatsValues `json:"comparison"`
}

// metric types accepted by /metrics (src/lib/constants.ts EVENT_COLUMNS and
// SESSION_COLUMNS plus channel). anything else is a 400.
var metricTypes = map[string]bool{
	"path": true, "fullPath": true, "entry": true, "exit": true, "referrer": true,
	"domain": true, "title": true, "query": true, "event": true, "tag": true,
	"hostname": true, "utmSource": true, "utmMedium": true, "utmCampaign": true,
	"utmContent": true, "utmTerm": true,
	"browser": true, "os": true, "device": true, "screen": true, "language": true,
	"country": true, "city": true, "region": true, "distinctId": true,
	"channel": true,
}

// MetricsQuery selects one ranked dimension. Limit 0 keeps umami's default
// of 500 rows. Offset skips that many rows of the ranking, for reading a
// dimension with more values than one page holds.
type MetricsQuery struct {
	Range  Range
	Type   string
	Limit  int
	Offset int
}

// MetricRow is one ranked value: x is the dimension value (null in umami
// becomes ""), y the count of views or visitors.
type MetricRow struct {
	X string `json:"x"`
	Y int64  `json:"y"`
}

// time series units accepted by /pageviews (src/lib/constants.ts UNIT_TYPES)
const (
	UnitMinute = "minute"
	UnitHour   = "hour"
	UnitDay    = "day"
	UnitMonth  = "month"
	UnitYear   = "year"
)

// PageviewsQuery selects a time series. Unit and Timezone are required here so
// buckets never depend on umami's defaults; Timezone is an iana name.
type PageviewsQuery struct {
	Range    Range
	Unit     string
	Timezone string
}

// SeriesPoint is one bucket of a time series. X is the bucket start.
type SeriesPoint struct {
	X string `json:"x"`
	Y int64  `json:"y"`
}

// Pageviews is the answer of /pageviews: views and sessions per bucket.
type Pageviews struct {
	Pageviews []SeriesPoint `json:"pageviews"`
	Sessions  []SeriesPoint `json:"sessions"`
}

func websitePath(id, sub string) (string, error) {
	if id == "" {
		return "", errors.New("umami: no website id")
	}
	return "/api/websites/" + url.PathEscape(id) + sub, nil
}

func statsPath(websiteID string, r Range) (string, error) {
	p, err := websitePath(websiteID, "/stats")
	if err != nil {
		return "", err
	}
	v, err := r.values()
	if err != nil {
		return "", err
	}
	return p + "?" + v.Encode(), nil
}

func metricsPath(websiteID string, q MetricsQuery) (string, error) {
	p, err := websitePath(websiteID, "/metrics")
	if err != nil {
		return "", err
	}
	if !metricTypes[q.Type] {
		return "", fmt.Errorf("umami metrics: type %q is not one umami accepts", q.Type)
	}
	v, err := q.Range.values()
	if err != nil {
		return "", err
	}
	v.Set("type", q.Type)
	if q.Limit < 0 {
		return "", errors.New("umami metrics: negative limit")
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset < 0 {
		return "", errors.New("umami metrics: negative offset")
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	return p + "?" + v.Encode(), nil
}

func pageviewsPath(websiteID string, q PageviewsQuery) (string, error) {
	p, err := websitePath(websiteID, "/pageviews")
	if err != nil {
		return "", err
	}
	switch q.Unit {
	case UnitMinute, UnitHour, UnitDay, UnitMonth, UnitYear:
	default:
		return "", fmt.Errorf("umami pageviews: unit %q is not one umami accepts", q.Unit)
	}
	if q.Timezone == "" {
		return "", errors.New("umami pageviews: no timezone")
	}
	if _, err := time.LoadLocation(q.Timezone); err != nil {
		return "", fmt.Errorf("umami pageviews: timezone: %w", err)
	}
	v, err := q.Range.values()
	if err != nil {
		return "", err
	}
	v.Set("unit", q.Unit)
	v.Set("timezone", q.Timezone)
	return p + "?" + v.Encode(), nil
}

// the read routes also exist on Client with an explicit token, for callers
// that act as a tenant's view-only user rather than the admin (isolation
// tests, the sso flow).

// Stats calls GET /api/websites/<id>/stats with token.
func (c *Client) Stats(ctx context.Context, token Token, websiteID string, r Range) (Stats, error) {
	var out Stats
	path, err := statsPath(websiteID, r)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, "stats", http.MethodGet, path, token, nil, &out)
	return out, err
}

// Metrics calls GET /api/websites/<id>/metrics with token.
func (c *Client) Metrics(ctx context.Context, token Token, websiteID string, q MetricsQuery) ([]MetricRow, error) {
	var out []MetricRow
	path, err := metricsPath(websiteID, q)
	if err != nil {
		return nil, err
	}
	err = c.do(ctx, "metrics", http.MethodGet, path, token, nil, &out)
	return out, err
}

// Pageviews calls GET /api/websites/<id>/pageviews with token.
func (c *Client) Pageviews(ctx context.Context, token Token, websiteID string, q PageviewsQuery) (Pageviews, error) {
	var out Pageviews
	path, err := pageviewsPath(websiteID, q)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, "pageviews", http.MethodGet, path, token, nil, &out)
	return out, err
}
