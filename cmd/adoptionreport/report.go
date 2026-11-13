package main

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"
)

// the shapes below follow docs/reference/openapi/metrics-v1.yaml field for
// field; the client decodes with unknown fields refused

type tenant struct {
	ID                   string `json:"id"`
	Slug                 string `json:"slug"`
	Name                 string `json:"name"`
	Status               string `json:"status"`
	CreatedAt            string `json:"created_at"`
	Sites                int64  `json:"sites"`
	SitesVerified        int64  `json:"sites_verified"`
	SitesWithStellarToml int64  `json:"sites_with_stellar_toml"`
	Verified             bool   `json:"verified"`
	StellarToml          bool   `json:"stellar_toml"`
}

type tenantList struct {
	Items      []tenant `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

type webDaily struct {
	TenantID  string `json:"tenant_id"`
	Day       string `json:"day"`
	Band      string `json:"band"`
	Sites     int64  `json:"sites"`
	Pageviews int64  `json:"pageviews"`
	Visitors  int64  `json:"visitors"`
	Visits    int64  `json:"visits"`
	Bounces   int64  `json:"bounces"`
	Totaltime int64  `json:"totaltime"`
}

type webDailyList struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Items      []webDaily `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type webDimension struct {
	TenantID  string `json:"tenant_id"`
	Day       string `json:"day"`
	Band      string `json:"band"`
	Dimension string `json:"dimension"`
	Key       string `json:"key"`
	Value     int64  `json:"value"`
}

type webDimensionList struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Items      []webDimension `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type apiNumbers struct {
	Requests    int64    `json:"requests"`
	Requests4xx int64    `json:"requests_4xx"`
	Requests5xx int64    `json:"requests_5xx"`
	Rate4xx     *float64 `json:"rate_4xx"`
	Rate5xx     *float64 `json:"rate_5xx"`
	P50Seconds  *float64 `json:"p50_seconds"`
	P50Over10s  bool     `json:"p50_over_10s"`
	P95Seconds  *float64 `json:"p95_seconds"`
	P95Over10s  bool     `json:"p95_over_10s"`
	P99Seconds  *float64 `json:"p99_seconds"`
	P99Over10s  bool     `json:"p99_over_10s"`
	Lossy       bool     `json:"lossy"`
}

type apiDaily struct {
	TenantID     string `json:"tenant_id"`
	Day          string `json:"day"`
	Services     int64  `json:"services"`
	SpanServices int64  `json:"span_services"`
	Source       string `json:"source"`
	apiNumbers
}

type apiDailyList struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Items      []apiDaily `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type apiRouteDaily struct {
	TenantID string `json:"tenant_id"`
	Day      string `json:"day"`
	Service  string `json:"service"`
	Method   string `json:"method"`
	Route    string `json:"route"`
	Source   string `json:"source"`
	apiNumbers
}

type apiRouteDailyList struct {
	From       string          `json:"from"`
	To         string          `json:"to"`
	Items      []apiRouteDaily `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// dimensions in the order the report shows them, with the rfp metric each
// one stands for
var dimensions = []struct{ Key, Title string }{
	{"referrer", "Referrers"},
	{"country", "Countries"},
	{"device", "Devices"},
	{"browser", "Browsers"},
	{"os", "Operating systems"},
}

// topN is how many keys of each breakdown the report lists
const topN = 10

// webDay is one utc day of the range. Present is false when the aggregation
// task never read the day: the api leaves such a day out, which is not the
// same as a day without traffic.
type webDay struct {
	Day        time.Time
	Present    bool
	Primary    webDaily
	Quarantine webDaily
}

// accepted is the evidence rule: the gateway accepts an event only after its
// challenge proof verified, and the replayer stores it in
// the primary or the quarantine band. a day counts when the primary band has
// page views; quarantined traffic is reported beside it but never counted.
func (d webDay) accepted() bool { return d.Present && d.Primary.Pageviews > 0 }

type dayRun struct {
	Start, End time.Time
	Days       int
}

type keyValue struct {
	Key   string
	Value int64
}

type tenantEvidence struct {
	Tenant tenant
	Days   []webDay
	// Longest is the longest run of consecutive accepted days in the range;
	// Current is the run that ends on the last day of the range
	Longest, Current dayRun
	FirstDay         *time.Time
	DaysAccepted     int
	DaysMissing      int
	Primary          webDaily
	QuarantineViews  int64
	Breakdowns       map[string][]keyValue
	BreakdownKeys    map[string]int

	APIDays        []apiDaily
	APIRun         dayRun
	APITotal       apiNumbers
	RoutesDay      string
	Routes         []apiRouteDaily
	RoutesTruncate int
}

type evidence struct {
	GeneratedAt time.Time
	Today       time.Time
	API         string
	From, To    time.Time
	MinDays     int
	Tenants     []tenantEvidence
	Sources     []Source
}

func (e evidence) rangeDays() int {
	return int(e.To.Sub(e.From)/(24*time.Hour)) + 1
}

func (t tenantEvidence) passes(minDays int) bool { return t.Longest.Days >= minDays }

func (e evidence) summaryLines() []string {
	var out []string
	for _, t := range e.Tenants {
		verdict := "below"
		if t.passes(e.MinDays) {
			verdict = "meets"
		}
		out = append(out, fmt.Sprintf("%s %s: longest run %s, %s the %d-day threshold; api days with requests %d of %d",
			t.Tenant.ID, t.Tenant.Name, runText(t.Longest), verdict, e.MinDays, len(t.APIDays), e.rangeDays()))
	}
	return out
}

func collect(ctx context.Context, c *client, ids []string, from, to time.Time) (evidence, error) {
	ev := evidence{From: from, To: to}
	known := map[string]tenant{}
	err := pages(ctx, c, "/tenants", url.Values{}, func(p tenantList) *string {
		for _, t := range p.Items {
			known[t.ID] = t
		}
		return p.NextCursor
	})
	if err != nil {
		return ev, err
	}
	for _, id := range ids {
		t, ok := known[id]
		if !ok {
			return ev, fmt.Errorf("tenant %s is not listed by /tenants: unknown, internal, or outside the key's scope", id)
		}
		te, err := collectTenant(ctx, c, t, from, to)
		if err != nil {
			return ev, fmt.Errorf("tenant %s: %w", id, err)
		}
		ev.Tenants = append(ev.Tenants, te)
	}
	return ev, nil
}

func dayRange(from, to time.Time) url.Values {
	q := url.Values{}
	q.Set("from", from.Format(dayLayout))
	q.Set("to", to.Format(dayLayout))
	return q
}

// checkEcho makes sure the api answered for the range that was asked; the
// contract fills in defaults, so a silently different range would change
// every number
func checkEcho(route, gotFrom, gotTo string, from, to time.Time) error {
	if gotFrom != from.Format(dayLayout) || gotTo != to.Format(dayLayout) {
		return fmt.Errorf("%s answered for %s to %s, asked for %s to %s", route, gotFrom, gotTo,
			from.Format(dayLayout), to.Format(dayLayout))
	}
	return nil
}

func collectTenant(ctx context.Context, c *client, t tenant, from, to time.Time) (tenantEvidence, error) {
	te := tenantEvidence{Tenant: t, Breakdowns: map[string][]keyValue{}, BreakdownKeys: map[string]int{}}
	q := dayRange(from, to)
	q.Set("tenant", t.ID)

	var daily []webDaily
	var echoErr error
	err := pages(ctx, c, "/web/daily", q, func(p webDailyList) *string {
		if echoErr == nil {
			echoErr = checkEcho("/web/daily", p.From, p.To, from, to)
		}
		daily = append(daily, p.Items...)
		return p.NextCursor
	})
	if err == nil {
		err = echoErr
	}
	if err != nil {
		return te, err
	}
	var dims []webDimension
	err = pages(ctx, c, "/web/dimensions", q, func(p webDimensionList) *string {
		if echoErr == nil {
			echoErr = checkEcho("/web/dimensions", p.From, p.To, from, to)
		}
		dims = append(dims, p.Items...)
		return p.NextCursor
	})
	if err == nil {
		err = echoErr
	}
	if err != nil {
		return te, err
	}
	var api []apiDaily
	err = pages(ctx, c, "/api/daily", q, func(p apiDailyList) *string {
		if echoErr == nil {
			echoErr = checkEcho("/api/daily", p.From, p.To, from, to)
		}
		api = append(api, p.Items...)
		return p.NextCursor
	})
	if err == nil {
		err = echoErr
	}
	if err != nil {
		return te, err
	}
	if err := analyseWeb(&te, daily, dims, from, to); err != nil {
		return te, err
	}
	if err := analyseAPI(&te, api, from, to); err != nil {
		return te, err
	}
	if len(te.APIDays) == 0 {
		return te, nil
	}
	// routes for the newest day with requests: percentiles of one day are
	// exact, and the contract never lets them be combined across days
	last := te.APIDays[len(te.APIDays)-1].Day
	day, err := parseDay(last)
	if err != nil {
		return te, err
	}
	rq := dayRange(day, day)
	rq.Set("tenant", t.ID)
	var routes []apiRouteDaily
	err = pages(ctx, c, "/api/routes/daily", rq, func(p apiRouteDailyList) *string {
		if echoErr == nil {
			echoErr = checkEcho("/api/routes/daily", p.From, p.To, day, day)
		}
		routes = append(routes, p.Items...)
		return p.NextCursor
	})
	if err == nil {
		err = echoErr
	}
	if err != nil {
		return te, err
	}
	for _, r := range routes {
		if r.TenantID != t.ID || r.Day != last {
			return te, fmt.Errorf("/api/routes/daily returned a row of tenant %s day %s", r.TenantID, r.Day)
		}
	}
	slices.SortStableFunc(routes, func(a, b apiRouteDaily) int {
		return cmp.Or(cmp.Compare(b.Requests, a.Requests), cmp.Compare(a.Service, b.Service),
			cmp.Compare(a.Method, b.Method), cmp.Compare(a.Route, b.Route))
	})
	const maxRoutes = 25
	if len(routes) > maxRoutes {
		te.RoutesTruncate = len(routes) - maxRoutes
		routes = routes[:maxRoutes]
	}
	te.RoutesDay, te.Routes = last, routes
	return te, nil
}

func analyseWeb(te *tenantEvidence, daily []webDaily, dims []webDimension, from, to time.Time) error {
	byDay := map[string]*webDay{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		te.Days = append(te.Days, webDay{Day: d})
	}
	for i := range te.Days {
		byDay[te.Days[i].Day.Format(dayLayout)] = &te.Days[i]
	}
	for _, r := range daily {
		if r.TenantID != te.Tenant.ID {
			return fmt.Errorf("/web/daily returned a row of tenant %s", r.TenantID)
		}
		d, ok := byDay[r.Day]
		if !ok {
			return fmt.Errorf("/web/daily returned day %s outside the range", r.Day)
		}
		d.Present = true
		switch r.Band {
		case "primary":
			d.Primary = r
		case "quarantine":
			d.Quarantine = r
		default:
			return fmt.Errorf("/web/daily returned band %q", r.Band)
		}
	}
	var cur dayRun
	for _, d := range te.Days {
		if !d.Present {
			te.DaysMissing++
		}
		if !d.accepted() {
			cur = dayRun{}
			continue
		}
		te.DaysAccepted++
		if te.FirstDay == nil {
			day := d.Day
			te.FirstDay = &day
		}
		if cur.Days == 0 {
			cur.Start = d.Day
		}
		cur.End, cur.Days = d.Day, cur.Days+1
		if cur.Days > te.Longest.Days {
			te.Longest = cur
		}
		te.Primary.Pageviews += d.Primary.Pageviews
		te.Primary.Visitors += d.Primary.Visitors
		te.Primary.Visits += d.Primary.Visits
		te.Primary.Bounces += d.Primary.Bounces
		te.Primary.Totaltime += d.Primary.Totaltime
	}
	// the loop above leaves cur at the run that ends on the last day, or
	// empty when the last day has no accepted page views
	te.Current = cur
	for _, d := range te.Days {
		te.QuarantineViews += d.Quarantine.Pageviews
	}

	sums := map[string]map[string]int64{}
	for _, r := range dims {
		if r.TenantID != te.Tenant.ID {
			return fmt.Errorf("/web/dimensions returned a row of tenant %s", r.TenantID)
		}
		if _, ok := byDay[r.Day]; !ok {
			return fmt.Errorf("/web/dimensions returned day %s outside the range", r.Day)
		}
		if r.Band != "primary" {
			continue
		}
		if sums[r.Dimension] == nil {
			sums[r.Dimension] = map[string]int64{}
		}
		sums[r.Dimension][r.Key] += r.Value
	}
	for _, dim := range dimensions {
		var kv []keyValue
		for k, v := range sums[dim.Key] {
			kv = append(kv, keyValue{k, v})
		}
		slices.SortFunc(kv, func(a, b keyValue) int {
			return cmp.Or(cmp.Compare(b.Value, a.Value), cmp.Compare(a.Key, b.Key))
		})
		te.BreakdownKeys[dim.Key] = len(kv)
		if len(kv) > topN {
			kv = kv[:topN]
		}
		te.Breakdowns[dim.Key] = kv
	}
	return nil
}

func analyseAPI(te *tenantEvidence, rows []apiDaily, from, to time.Time) error {
	slices.SortFunc(rows, func(a, b apiDaily) int { return cmp.Compare(a.Day, b.Day) })
	var cur dayRun
	var prev time.Time
	for _, r := range rows {
		if r.TenantID != te.Tenant.ID {
			return fmt.Errorf("/api/daily returned a row of tenant %s", r.TenantID)
		}
		d, err := parseDay(r.Day)
		if err != nil {
			return fmt.Errorf("/api/daily: %w", err)
		}
		if d.Before(from) || d.After(to) {
			return fmt.Errorf("/api/daily returned day %s outside the range", r.Day)
		}
		if r.Requests == 0 {
			continue
		}
		te.APIDays = append(te.APIDays, r)
		if cur.Days > 0 && d.Equal(prev.AddDate(0, 0, 1)) {
			cur.End, cur.Days = d, cur.Days+1
		} else {
			cur = dayRun{Start: d, End: d, Days: 1}
		}
		prev = d
		if cur.Days > te.APIRun.Days {
			te.APIRun = cur
		}
		te.APITotal.Requests += r.Requests
		te.APITotal.Requests4xx += r.Requests4xx
		te.APITotal.Requests5xx += r.Requests5xx
		te.APITotal.Lossy = te.APITotal.Lossy || r.Lossy
	}
	return nil
}
