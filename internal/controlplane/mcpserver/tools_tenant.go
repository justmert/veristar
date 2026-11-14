package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const tenantInstructions = `Veristar is privacy-first web and API analytics for projects building on Stellar. ` +
	`This connection reads one project, the one its credential was made for, and every tool is read-only.

How to work:
1. Call list_projects to see the project, then list_sites for its websites and their site ids.
2. Web analytics (from the site's tracker): get_web_stats for totals with a comparison period, get_web_traffic for trends, ` +
	`get_web_metrics for rankings (pages, referrers, channels, countries, devices, browsers, events, UTM), get_web_realtime ` +
	`for visitors right now, get_event_stats, get_event_series and get_event_properties for custom events, get_sessions and ` +
	`get_session_stats for sessions, and list_funnels, run_funnel, run_journey, run_retention and get_goals for reports.
3. API analytics (from OpenTelemetry): list_api_services, then get_api_overview, get_api_timeseries, list_api_routes, ` +
	`get_api_status_codes, get_api_top_error_routes and get_api_warnings.

Dates are ISO 8601: a date such as 2026-11-01 (UTC) or a timestamp with a zone. Web views count visitors without cookies ` +
	`and rotate visitor ids daily, so unique visitors over several days are an upper bound of distinct people. API latency ` +
	`percentiles are estimated from histogram buckets.`

func (s *Server) addTenantTools(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:  "list_projects",
		Title: "The project",
		Description: "Returns the Veristar project (tenant) this connection reads: id, name, slug, status, " +
			"enrollment answers and the caller's role. A connection always reads exactly one project.",
	}, nil, func(ctx context.Context, p Principal, _ struct{}) (any, error) {
		var t json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String(), nil, &t); err != nil {
			return nil, err
		}
		return map[string]any{"projects": []json.RawMessage{t}}, nil
	})

	type sitesIn struct {
		Limit  int    `json:"limit,omitempty" jsonschema:"sites per page, 1 to 200, default 50"`
		Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "list_sites",
		Title: "Websites of the project",
		Description: "Lists the project's websites with their site ids, domains, allowed origins, status " +
			"(active, pending review, suspended), domain verification and whether UTM parameters are kept. Every " +
			"web analytics tool needs a site id from here.",
	}, func(props map[string]*jsonschema.Schema) {
		intRange(props, "limit", 1, 200)
		maxLen(props, "cursor", 1000)
	}, func(ctx context.Context, p Principal, in sitesIn) (any, error) {
		q := url.Values{}
		if in.Limit != 0 {
			if err := checkInt("limit", in.Limit, 1, 200); err != nil {
				return nil, err
			}
			q.Set("limit", strconv.Itoa(in.Limit))
		}
		if in.Cursor != "" {
			q.Set("cursor", in.Cursor)
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/sites", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})

	type siteOnly struct {
		SiteID string `json:"site_id" jsonschema:"site id from list_sites"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:        "get_site",
		Title:       "One website",
		Description: "Returns one website of the project by its site id: domain, origins, status, verification and settings.",
	}, func(props map[string]*jsonschema.Schema) {
		uuidFormat(props, "site_id")
	}, func(ctx context.Context, p Principal, in siteOnly) (any, error) {
		id, err := parseUUID("site_id", in.SiteID)
		if err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/sites/"+id.String(), nil, &out); err != nil {
			return nil, err
		}
		return out, nil
	})

	s.addWebTools(srv)
	s.addAPITools(srv)
}

// api metrics tools (contract 12.4.3): the console's routes of contract 7

type apiRangeIn struct {
	Range string `json:"range,omitempty" jsonschema:"1h (minute buckets), 24h, 7d, 30d (hour buckets), 90d, 12mo, 24mo (day buckets) or custom; default 24h"`
	From  string `json:"from,omitempty" jsonschema:"with range custom: start, ISO 8601 date or timestamp with zone"`
	To    string `json:"to,omitempty" jsonschema:"with range custom: end, exclusive"`
}

type apiFilterIn struct {
	Service string `json:"service,omitempty" jsonschema:"exact service.name, from list_api_services"`
	Route   string `json:"route,omitempty" jsonschema:"exact normalized route such as /v1/accounts/{id}"`
	Method  string `json:"method,omitempty" jsonschema:"exact HTTP method such as GET"`
}

func apiRangePatch(props map[string]*jsonschema.Schema) {
	enum(props, "range", "1h", "24h", "7d", "30d", "90d", "12mo", "24mo", "custom")
}

func apiFilterPatch(props map[string]*jsonschema.Schema) {
	maxLen(props, "service", 128)
	maxLen(props, "route", 1024)
	maxLen(props, "method", 16)
}

// rfc3339 turns an ISO 8601 date into the timestamp the api routes take.
func rfc3339(name, v string) (string, error) {
	t, err := parseInstant(name, v, false)
	if err != nil {
		return "", err
	}
	return t.Format(time.RFC3339), nil
}

func (r apiRangeIn) values(q url.Values) error {
	if r.Range != "" {
		q.Set("range", r.Range)
	}
	if r.Range == "custom" {
		if r.From == "" || r.To == "" {
			return invalid("range custom needs from and to")
		}
		from, err := rfc3339("from", r.From)
		if err != nil {
			return err
		}
		to, err := rfc3339("to", r.To)
		if err != nil {
			return err
		}
		q.Set("from", from)
		q.Set("to", to)
	} else if r.From != "" || r.To != "" {
		return invalid("from and to apply only with range custom")
	}
	return nil
}

func (f apiFilterIn) values(q url.Values) error {
	for _, kv := range []struct{ k, v string }{{"service", f.Service}, {"route", f.Route}, {"method", f.Method}} {
		if kv.v == "" {
			continue
		}
		if err := text(kv.k, kv.v, 1024); err != nil {
			return err
		}
		q.Set(kv.k, kv.v)
	}
	return nil
}

const percentileNote = "p50, p95 and p99 are in seconds and are estimated from histogram buckets (interpolated " +
	"inside the bucket), not exact request latencies; \">10 s\" means the percentile is in the open bucket above " +
	"10 seconds. lossy true means some histograms were re-bucketed, so percentiles are approximate. source span " +
	"means numbers come from sampled traces and can undercount requests."

func (s *Server) apiRead(ctx context.Context, p Principal, route string, q url.Values) (any, error) {
	var out map[string]any
	if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/api-metrics/"+route, q, &out); err != nil {
		return nil, err
	}
	out["note"] = percentileNote
	return out, nil
}

func (s *Server) addAPITools(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:  "list_api_services",
		Title: "API services",
		Description: "Lists the project's API services (OpenTelemetry service.name) with data in the range, " +
			"their request counts and whether their numbers come from metrics or sampled traces. Start here for " +
			"API questions.",
	}, apiRangePatch, func(ctx context.Context, p Principal, in apiRangeIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		return s.apiRead(ctx, p, "services", q)
	})

	type filteredIn struct {
		apiRangeIn
		apiFilterIn
	}
	patch := func(props map[string]*jsonschema.Schema) { apiRangePatch(props); apiFilterPatch(props) }
	query := func(in filteredIn) (url.Values, error) {
		q := url.Values{}
		if err := in.apiRangeIn.values(q); err != nil {
			return nil, err
		}
		return q, in.apiFilterIn.values(q)
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_api_overview",
		Title: "API totals",
		Description: "Totals of the project's API traffic over a range, with the previous range of equal " +
			"length: requests, 4xx and 5xx errors and their rates, and latency percentiles p50, p95 and p99. " +
			"Filter by service, route or method. Percentiles are estimates from histogram buckets.",
	}, patch, func(ctx context.Context, p Principal, in filteredIn) (any, error) {
		q, err := query(in)
		if err != nil {
			return nil, err
		}
		return s.apiRead(ctx, p, "overview", q)
	})
	addTool(s, srv, &mcp.Tool{
		Name:  "get_api_timeseries",
		Title: "API traffic over time",
		Description: "The project's API requests, error rates and latency percentiles per bucket over a range: " +
			"minute buckets for 1h, hour buckets up to 30 days, day buckets beyond, within the retention of each " +
			"resolution (the answer says when it switched). Every bucket is present, empty ones included.",
	}, patch, func(ctx context.Context, p Principal, in filteredIn) (any, error) {
		q, err := query(in)
		if err != nil {
			return nil, err
		}
		return s.apiRead(ctx, p, "timeseries", q)
	})
	addTool(s, srv, &mcp.Tool{
		Name:  "get_api_status_codes",
		Title: "API status codes",
		Description: "The distribution of HTTP status codes of the project's API over a range: counts per code " +
			"and per class (2xx, 3xx, 4xx, 5xx, unknown). Filter by service, route or method.",
	}, patch, func(ctx context.Context, p Principal, in filteredIn) (any, error) {
		q, err := query(in)
		if err != nil {
			return nil, err
		}
		return s.apiRead(ctx, p, "status", q)
	})

	type routesIn struct {
		filteredIn
		Sort   string `json:"sort,omitempty" jsonschema:"requests (default), errors_4xx, errors_5xx, rate_4xx, rate_5xx, p50, p95, p99 or route"`
		Order  string `json:"order,omitempty" jsonschema:"desc (default, ascending for route) or asc"`
		Limit  int    `json:"limit,omitempty" jsonschema:"routes per page, 1 to 200, default 50"`
		Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
	}
	routesPatch := func(props map[string]*jsonschema.Schema) {
		patch(props)
		enum(props, "sort", "requests", "errors_4xx", "errors_5xx", "rate_4xx", "rate_5xx", "p50", "p95", "p99", "route")
		enum(props, "order", "asc", "desc")
		intRange(props, "limit", 1, 200)
		maxLen(props, "cursor", 1000)
	}
	routesQuery := func(in routesIn) (url.Values, error) {
		q, err := query(in.filteredIn)
		if err != nil {
			return nil, err
		}
		if in.Sort != "" {
			q.Set("sort", in.Sort)
		}
		if in.Order != "" {
			q.Set("order", in.Order)
		}
		if in.Limit != 0 {
			if err := checkInt("limit", in.Limit, 1, 200); err != nil {
				return nil, err
			}
			q.Set("limit", strconv.Itoa(in.Limit))
		}
		if in.Cursor != "" {
			q.Set("cursor", in.Cursor)
		}
		return q, nil
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "list_api_routes",
		Title: "API routes",
		Description: "The project's API endpoints (service, method and normalized route) over a range with " +
			"requests, errors, error rates and latency percentiles each, sorted and paginated. Use it for the " +
			"busiest or slowest endpoints.",
	}, routesPatch, func(ctx context.Context, p Principal, in routesIn) (any, error) {
		q, err := routesQuery(in)
		if err != nil {
			return nil, err
		}
		return s.apiRead(ctx, p, "routes", q)
	})

	type errorRoutesIn struct {
		apiRangeIn
		Service string `json:"service,omitempty" jsonschema:"exact service.name"`
		Class   string `json:"class,omitempty" jsonschema:"5xx (default) or 4xx"`
		By      string `json:"by,omitempty" jsonschema:"count (default): most errors first; rate: highest error rate first"`
		Limit   int    `json:"limit,omitempty" jsonschema:"routes, 1 to 200, default 10"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_api_top_error_routes",
		Title: "API routes with most errors",
		Description: "The project's API routes with the most server errors (5xx) or client errors (4xx) over a " +
			"range, by count or by rate, with their request counts and latency percentiles.",
	}, func(props map[string]*jsonschema.Schema) {
		apiRangePatch(props)
		maxLen(props, "service", 128)
		enum(props, "class", "5xx", "4xx")
		enum(props, "by", "count", "rate")
		intRange(props, "limit", 1, 200)
	}, func(ctx context.Context, p Principal, in errorRoutesIn) (any, error) {
		class, by := in.Class, in.By
		if class == "" {
			class = "5xx"
		}
		if by == "" {
			by = "count"
		}
		sort := map[string]string{"5xx count": "errors_5xx", "5xx rate": "rate_5xx", "4xx count": "errors_4xx", "4xx rate": "rate_4xx"}[class+" "+by]
		if sort == "" {
			return nil, invalid("class must be 5xx or 4xx and by count or rate")
		}
		q, err := routesQuery(routesIn{filteredIn: filteredIn{apiRangeIn: in.apiRangeIn, apiFilterIn: apiFilterIn{Service: in.Service}},
			Sort: sort, Order: "desc", Limit: orDefault(in.Limit, 10)})
		if err != nil {
			return nil, err
		}
		out, err := s.apiRead(ctx, p, "routes", q)
		if err != nil {
			return nil, err
		}
		m, ok := out.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcpserver: routes answer of type %T", out)
		}
		m["sorted_by"] = sort
		return m, nil
	})

	addTool(s, srv, &mcp.Tool{
		Name:  "get_api_warnings",
		Title: "API ingest warnings",
		Description: "Data quality warnings of the project's OpenTelemetry ingest per UTC day over a range: " +
			"span_derived (numbers derived from sampled traces), lossy_histogram (a histogram re-bucketed " +
			"inexactly), cardinality_overflow (routes or services above the daily cap folded into {other}), " +
			"secret_in_path (a Stellar secret seed in a path was redacted) and coarse_buckets (buckets too wide for " +
			"reliable percentiles). Use it when API numbers look incomplete or odd.",
	}, apiRangePatch, func(ctx context.Context, p Principal, in apiRangeIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/api-metrics/warnings", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
}
