package mcpserver

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// the web analytics tools of contract 12.4.2. each takes a site id from
// list_sites, an optional view (the quarantine band holds events the
// gateway flagged as likely automated), a range and filters.

type siteIn struct {
	SiteID string `json:"site_id" jsonschema:"site id from list_sites"`
	View   string `json:"view,omitempty" jsonschema:"primary (default): accepted traffic; quarantine: events flagged as likely automated"`
}

type rangeIn struct {
	From string `json:"from,omitempty" jsonschema:"start, ISO 8601 date (2026-11-01) or timestamp with zone (2026-11-01T09:00:00Z); default 7 days before to"`
	To   string `json:"to,omitempty" jsonschema:"end, inclusive; a date means the end of that day; default now"`
}

const (
	defaultMetricLimit = 20
	maxMetricLimit     = 500
	maxMetricOffset    = 100_000
	defaultPageSize    = 20
	maxPageSize        = 100
	maxPage            = 10_000
)

func sitePatch(props map[string]*jsonschema.Schema) {
	uuidFormat(props, "site_id")
	enum(props, "view", "primary", "quarantine")
}

func unitPatch(props map[string]*jsonschema.Schema) {
	enum(props, "unit", "minute", "hour", "day", "month", "year")
}

// webCall is the common start of a web tool: the checked website, the
// range and the base query.
func (s *Server) webCall(ctx context.Context, p Principal, si siteIn, ri rangeIn, f *webFilters, allowEvent bool) (website, url.Values, rangeOut, error) {
	start, end, err := webRange(ri.From, ri.To, s.now())
	if err != nil {
		return website{}, nil, rangeOut{}, err
	}
	q := rangeValues(start, end)
	if err := f.values(q, allowEvent); err != nil {
		return website{}, nil, rangeOut{}, err
	}
	w, err := s.resolveSite(ctx, p, si.SiteID, si.View)
	if err != nil {
		return website{}, nil, rangeOut{}, err
	}
	return w, q, rangeJSON(start, end), nil
}

type siteOut struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	View   string `json:"view"`
}

func (w website) out() siteOut {
	return siteOut{ID: w.site.ID.String(), Name: w.site.Name, Domain: w.site.Domain, View: w.view}
}

// webTotals are umami's /stats numbers with the dashboard's own formulas.
type webTotals struct {
	Pageviews           int64    `json:"pageviews"`
	Visitors            int64    `json:"visitors"`
	Visits              int64    `json:"visits"`
	Bounces             int64    `json:"bounces"`
	TotalTimeSeconds    int64    `json:"totaltime_seconds"`
	BounceRate          *float64 `json:"bounce_rate"`
	AverageVisitSeconds *float64 `json:"average_visit_seconds"`
	PageviewsPerVisit   *float64 `json:"pageviews_per_visit"`
}

type statsRaw struct {
	Pageviews  flexNumber `json:"pageviews"`
	Visitors   flexNumber `json:"visitors"`
	Visits     flexNumber `json:"visits"`
	Bounces    flexNumber `json:"bounces"`
	Totaltime  flexNumber `json:"totaltime"`
	Comparison *statsRaw  `json:"comparison"`
}

func totals(r statsRaw) webTotals {
	t := webTotals{Pageviews: int64(r.Pageviews), Visitors: int64(r.Visitors), Visits: int64(r.Visits),
		Bounces: int64(r.Bounces), TotalTimeSeconds: int64(r.Totaltime)}
	if t.Visits > 0 {
		v := float64(t.Visits)
		br := math.Min(float64(t.Bounces), v) / v
		avg := float64(t.TotalTimeSeconds) / v
		ppv := float64(t.Pageviews) / v
		t.BounceRate, t.AverageVisitSeconds, t.PageviewsPerVisit = &br, &avg, &ppv
	}
	return t
}

// flexNumber decodes a json number or a numeric string: umami returns
// postgresql sums as strings from some queries.
type flexNumber float64

func (f *flexNumber) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return invalid("umami returned %q where a number belongs", s)
		}
		*f = flexNumber(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = flexNumber(v)
	return nil
}

var metricTypes = []string{
	"path", "entry", "exit", "title", "query", "hostname", "referrer", "domain", "channel", "event", "tag",
	"browser", "os", "device", "screen", "language", "country", "region", "city",
	"utmSource", "utmMedium", "utmCampaign", "utmContent", "utmTerm",
}

// visitorMetrics count unique visitors; the others count views or events
var visitorMetrics = map[string]bool{
	"browser": true, "os": true, "device": true, "screen": true, "language": true, "country": true,
	"region": true, "city": true, "referrer": true, "domain": true, "channel": true,
	"utmSource": true, "utmMedium": true, "utmCampaign": true, "utmContent": true, "utmTerm": true,
}

func (s *Server) addWebTools(srv *mcp.Server) {
	type statsIn struct {
		siteIn
		rangeIn
		Compare  string      `json:"compare,omitempty" jsonschema:"prev (default): the previous period of equal length; yoy: the same period a year earlier"`
		Timezone string      `json:"timezone,omitempty" jsonschema:"IANA zone, default UTC"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_web_stats",
		Title: "Web traffic totals",
		Description: "Totals of a site's web traffic over a range: page views, unique visitors, visits, bounces, " +
			"bounce rate, average visit duration and page views per visit, with the same totals for the previous " +
			"period (or the same period a year earlier) to compare. Use it for questions such as \"how many " +
			"visitors did we have last week?\". Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		enum(props, "compare", "prev", "yoy")
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in statsIn) (any, error) {
		tz, err := checkTimezone(in.Timezone)
		if err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, false)
		if err != nil {
			return nil, err
		}
		q.Set("timezone", tz)
		compare := in.Compare
		if compare == "" {
			compare = "prev"
		}
		q.Set("compare", compare)
		var raw statsRaw
		if err := s.umamiRead(ctx, p, w, "/stats", q, &raw); err != nil {
			return nil, err
		}
		prev := statsRaw{}
		if raw.Comparison != nil {
			prev = *raw.Comparison
		}
		return map[string]any{
			"site": w.out(), "range": rng, "compare": compare,
			"current": totals(raw), "previous": totals(prev),
		}, nil
	})

	type trafficIn struct {
		siteIn
		rangeIn
		Unit     string      `json:"unit,omitempty" jsonschema:"bucket: minute (ranges up to a day), hour (up to 31 days), day (default), month or year"`
		Timezone string      `json:"timezone,omitempty" jsonschema:"IANA zone for the buckets, default UTC"`
		Compare  string      `json:"compare,omitempty" jsonschema:"also return the previous period (prev) or the same period a year earlier (yoy)"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_web_traffic",
		Title: "Web traffic over time",
		Description: "A site's page views and visits (sessions) as a time series in minute, hour, day, month or " +
			"year buckets. Use it for trends, peak days or charting. Each point has the bucket start and a count. " +
			"Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		unitPatch(props)
		enum(props, "compare", "prev", "yoy")
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in trafficIn) (any, error) {
		tz, err := checkTimezone(in.Timezone)
		if err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, false)
		if err != nil {
			return nil, err
		}
		unit := in.Unit
		if unit == "" {
			unit = "day"
		}
		if err := checkUnit(unit, mustParse(rng.From), mustParse(rng.To)); err != nil {
			return nil, err
		}
		q.Set("unit", unit)
		q.Set("timezone", tz)
		if in.Compare != "" {
			q.Set("compare", in.Compare)
		}
		var raw json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/pageviews", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "unit": unit, "timezone": tz,
			"note": "pageviews and sessions are lists of {x: bucket start, y: count}; sessions are visits", "series": raw}, nil
	})

	type metricsIn struct {
		siteIn
		rangeIn
		Type     string      `json:"type" jsonschema:"dimension to rank"`
		Limit    int         `json:"limit,omitempty" jsonschema:"rows, 1 to 500, default 20"`
		Offset   int         `json:"offset,omitempty" jsonschema:"rows to skip, for the next page"`
		Search   string      `json:"search,omitempty" jsonschema:"only values containing this text"`
		Timezone string      `json:"timezone,omitempty" jsonschema:"IANA zone, default UTC"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_web_metrics",
		Title: "Ranked web breakdown",
		Description: "Ranks one dimension of a site's traffic over a range: top pages (path), entry and exit " +
			"pages, titles, referrers, channels, countries, regions, cities, browsers, operating systems, devices, " +
			"screens, languages, custom events and UTM values. For page dimensions (path, entry, exit, title, " +
			"query, hostname, event, tag) the value counts views or events; for visitor dimensions (browser, os, " +
			"device, country, referrer, channel, utm*) it counts unique visitors. UTM values exist only for sites " +
			"that collect UTM parameters. Paginate with offset. Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		enum(props, "type", metricTypes...)
		intRange(props, "limit", 1, maxMetricLimit)
		intRange(props, "offset", 0, maxMetricOffset)
		maxLen(props, "search", 200)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in metricsIn) (any, error) {
		tz, err := checkTimezone(in.Timezone)
		if err != nil {
			return nil, err
		}
		limit := orDefault(in.Limit, defaultMetricLimit)
		if err := checkInt("limit", limit, 1, maxMetricLimit); err != nil {
			return nil, err
		}
		if err := checkInt("offset", in.Offset, 0, maxMetricOffset); err != nil {
			return nil, err
		}
		if err := text("search", in.Search, 200); err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, in.Type == "event")
		if err != nil {
			return nil, err
		}
		q.Set("type", in.Type)
		q.Set("limit", strconv.Itoa(limit))
		if in.Offset > 0 {
			q.Set("offset", strconv.Itoa(in.Offset))
		}
		if in.Search != "" {
			q.Set("search", in.Search)
		}
		q.Set("timezone", tz)
		var rows []struct {
			X *string    `json:"x"`
			Y flexNumber `json:"y"`
		}
		if err := s.umamiRead(ctx, p, w, "/metrics", q, &rows); err != nil {
			return nil, err
		}
		type row struct {
			Value string `json:"value"`
			Count int64  `json:"count"`
		}
		out := make([]row, 0, len(rows))
		for _, r := range rows {
			v := "(none)"
			if r.X != nil && *r.X != "" {
				v = *r.X
			}
			out = append(out, row{Value: v, Count: int64(r.Y)})
		}
		measure := "views"
		if visitorMetrics[in.Type] {
			measure = "visitors"
		}
		var next *int
		if len(rows) == limit {
			n := in.Offset + limit
			next = &n
		}
		return map[string]any{"site": w.out(), "range": rng, "type": in.Type, "measure": measure,
			"offset": in.Offset, "next_offset": next, "rows": out}, nil
	})

	addTool(s, srv, &mcp.Tool{
		Name:  "get_web_realtime",
		Title: "Visitors right now",
		Description: "The number of visitors active on a site in the last few minutes. Use it for \"how many " +
			"people are on the site right now?\". Needs a site_id from list_sites.",
	}, sitePatch, func(ctx context.Context, p Principal, in siteIn) (any, error) {
		w, err := s.resolveSite(ctx, p, in.SiteID, in.View)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Visitors flexNumber `json:"visitors"`
		}
		if err := s.umamiRead(ctx, p, w, "/active", nil, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "active_visitors": int64(raw.Visitors), "as_of": rangeJSON(s.now(), s.now()).To}, nil
	})

	type eventStatsIn struct {
		siteIn
		rangeIn
		Compare string      `json:"compare,omitempty" jsonschema:"prev (default) or yoy"`
		Filters *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_event_stats",
		Title: "Custom event totals",
		Description: "Totals of a site's custom events (not page views) over a range: events fired, visitors and " +
			"visits that fired them, and distinct event names, with the previous period to compare. Filter one " +
			"event with filters.event. For a ranking per event use get_web_metrics with type event; for trends use " +
			"get_event_series. Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		enum(props, "compare", "prev", "yoy")
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in eventStatsIn) (any, error) {
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		if in.Compare != "" {
			q.Set("compare", in.Compare)
		}
		var raw json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/events/stats", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "stats": raw}, nil
	})

	type eventSeriesIn struct {
		siteIn
		rangeIn
		Unit     string      `json:"unit,omitempty" jsonschema:"bucket: minute, hour, day (default), month or year"`
		Timezone string      `json:"timezone,omitempty" jsonschema:"IANA zone, default UTC"`
		Limit    int         `json:"limit,omitempty" jsonschema:"event names to include, 1 to 500, default 20"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_event_series",
		Title: "Custom events over time",
		Description: "Counts of a site's custom events over time, per event name, in minute, hour, day, month or " +
			"year buckets: the top event names by volume (default 20). Use it to spot spikes or compare events. " +
			"Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		unitPatch(props)
		intRange(props, "limit", 1, maxMetricLimit)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in eventSeriesIn) (any, error) {
		tz, err := checkTimezone(in.Timezone)
		if err != nil {
			return nil, err
		}
		limit := orDefault(in.Limit, defaultMetricLimit)
		if err := checkInt("limit", limit, 1, maxMetricLimit); err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		unit := in.Unit
		if unit == "" {
			unit = "day"
		}
		if err := checkUnit(unit, mustParse(rng.From), mustParse(rng.To)); err != nil {
			return nil, err
		}
		q.Set("unit", unit)
		q.Set("timezone", tz)
		q.Set("limit", strconv.Itoa(limit))
		var rows []struct {
			X string     `json:"x"`
			T string     `json:"t"`
			Y flexNumber `json:"y"`
		}
		if err := s.umamiRead(ctx, p, w, "/events/series", q, &rows); err != nil {
			return nil, err
		}
		type point struct {
			At    string `json:"at"`
			Count int64  `json:"count"`
		}
		type series struct {
			Event  string  `json:"event"`
			Total  int64   `json:"total"`
			Points []point `json:"points"`
		}
		idx := map[string]int{}
		var out []series
		for _, r := range rows {
			i, ok := idx[r.X]
			if !ok {
				i = len(out)
				idx[r.X] = i
				out = append(out, series{Event: r.X, Points: []point{}})
			}
			out[i].Points = append(out[i].Points, point{At: r.T, Count: int64(r.Y)})
			out[i].Total += int64(r.Y)
		}
		if out == nil {
			out = []series{}
		}
		return map[string]any{"site": w.out(), "range": rng, "unit": unit, "timezone": tz, "events": out}, nil
	})

	type propsIn struct {
		siteIn
		rangeIn
		PropertyName string      `json:"property_name,omitempty" jsonschema:"property to rank the values of; omit to list the properties first"`
		EventName    string      `json:"event_name,omitempty" jsonschema:"only this custom event, for example checkout"`
		Filters      *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_event_properties",
		Title: "Custom event data",
		Description: "Explores the data sent with a site's custom events (for example {plan: \"pro\"} on a " +
			"checkout event). Without property_name it lists the property names seen in the range with their event, " +
			"data type and count, and totals. With property_name it ranks that property's values. Needs a site_id " +
			"from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		maxLen(props, "property_name", 500)
		maxLen(props, "event_name", 50)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in propsIn) (any, error) {
		if err := text("property_name", in.PropertyName, 500); err != nil {
			return nil, err
		}
		if err := text("event_name", in.EventName, 50); err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		base := map[string]any{"site": w.out(), "range": rng}
		if in.PropertyName != "" {
			q.Set("propertyName", in.PropertyName)
			if in.EventName != "" {
				q.Set("eventName", in.EventName)
			}
			var values json.RawMessage
			if err := s.umamiRead(ctx, p, w, "/event-data/values", q, &values); err != nil {
				return nil, err
			}
			base["property_name"], base["values"] = in.PropertyName, values
			return base, nil
		}
		if in.EventName != "" {
			q.Set("event", in.EventName)
		}
		var props, stats json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/event-data/properties", q, &props); err != nil {
			return nil, err
		}
		if err := s.umamiRead(ctx, p, w, "/event-data/stats", q, &stats); err != nil {
			return nil, err
		}
		base["properties"], base["totals"] = props, stats
		base["note"] = "dataType: 1 string, 2 number, 3 boolean, 4 date, 5 array"
		return base, nil
	})

	type sessionsIn struct {
		siteIn
		rangeIn
		Search   string      `json:"search,omitempty" jsonschema:"text to find in browser, os, device or city"`
		Page     int         `json:"page,omitempty" jsonschema:"page, from 1"`
		PageSize int         `json:"page_size,omitempty" jsonschema:"sessions per page, 1 to 100, default 20"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_sessions",
		Title: "Visitor sessions",
		Description: "Lists a site's visitor sessions in a range, newest first, a page at a time: browser, " +
			"operating system, device, country, region, city, visits, views, events and first and last activity. " +
			"Sessions hold no names, emails, IP addresses or user ids; the session id is a daily rotating value. " +
			"Use get_session_stats when counts are enough. Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		maxLen(props, "search", 200)
		intRange(props, "page", 1, maxPage)
		intRange(props, "page_size", 1, maxPageSize)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in sessionsIn) (any, error) {
		page := orDefault(in.Page, 1)
		size := orDefault(in.PageSize, defaultPageSize)
		if err := checkInt("page", page, 1, maxPage); err != nil {
			return nil, err
		}
		if err := checkInt("page_size", size, 1, maxPageSize); err != nil {
			return nil, err
		}
		if err := text("search", in.Search, 200); err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(size))
		if in.Search != "" {
			q.Set("search", in.Search)
		}
		var raw struct {
			Data  json.RawMessage `json:"data"`
			Count flexNumber      `json:"count"`
		}
		if err := s.umamiRead(ctx, p, w, "/sessions", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "page": page, "page_size": size,
			"total": int64(raw.Count), "has_more": int64(page*size) < int64(raw.Count), "sessions": raw.Data}, nil
	})

	type sessionStatsIn struct {
		siteIn
		rangeIn
		Filters *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_session_stats",
		Title: "Session counts",
		Description: "Session-level totals of a site over a range: visitors, visits, page views, events and the " +
			"number of countries. Lighter than get_sessions and works with filters. For bounce rate and visit " +
			"duration use get_web_stats. Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in sessionStatsIn) (any, error) {
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/sessions/stats", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "stats": raw}, nil
	})

	s.addReportTools(srv)
}

// mustParse reads a timestamp rangeJSON wrote.
func mustParse(v string) time.Time {
	t, err := parseInstant("range", v, false)
	if err != nil {
		panic(err)
	}
	return t
}
