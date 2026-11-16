package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const operatorInstructions = `Veristar is privacy-first web and API analytics for projects building on Stellar. ` +
	`This connection holds the operator view across every enrolled project, read-only.

How to work:
1. list_tenants finds projects by name, slug, website or domain and filters by status or review; get_tenant ` +
	`returns one project's detail.
2. get_global_web_overview and get_global_api_overview rank every project on the core web metrics (page views, ` +
	`visitors, visits, bounce rate, visit duration, top referrers, countries, devices, browsers) or API metrics ` +
	`(requests, error rates, latency percentiles, services, routes), with filters, sorting and pages.
3. compare_tenants_web and compare_tenants_api put up to five projects side by side with daily series.
4. get_tenant_onchain returns one project's declared Stellar accounts and contracts, their proofs and daily ledger ` +
	`activity; the overviews and compares carry an onchain summary per project.

Ranges are whole UTC days (from, to, both included, at most 366 days, default the 30 days ending today). The views ` +
	`read daily aggregates only, never sessions. API latency percentiles are estimated from histogram buckets.`

type dayRangeIn struct {
	From string `json:"from,omitempty" jsonschema:"first UTC day, ISO 8601 date such as 2026-11-01; default 29 days before to"`
	To   string `json:"to,omitempty" jsonschema:"last UTC day, included; default today"`
}

func (d dayRangeIn) values(q url.Values) error {
	from, err := dayOf("from", d.From)
	if err != nil {
		return err
	}
	to, err := dayOf("to", d.To)
	if err != nil {
		return err
	}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	return nil
}

var tenantStatuses = []string{"pending", "active", "rejected", "suspended", "removed"}

func statusList(q url.Values, statuses []string) error {
	if len(statuses) == 0 {
		return nil
	}
	for _, s := range statuses {
		ok := false
		for _, v := range tenantStatuses {
			ok = ok || v == s
		}
		if !ok {
			return invalid("status values must be pending, active, rejected, suspended or removed")
		}
	}
	q.Set("status", strings.Join(statuses, ","))
	return nil
}

func statusPatch(props map[string]*jsonschema.Schema) {
	setEnum(props["status"].Items, tenantStatuses...)
}

type pageIn struct {
	Sort   string `json:"sort,omitempty" jsonschema:"column to sort by"`
	Order  string `json:"order,omitempty" jsonschema:"desc (default) or asc"`
	Limit  int    `json:"limit,omitempty" jsonschema:"rows per page, 1 to 200, default 50"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
}

func (pg pageIn) values(q url.Values) error {
	if pg.Sort != "" {
		q.Set("sort", pg.Sort)
	}
	if pg.Order != "" {
		q.Set("order", pg.Order)
	}
	if pg.Limit != 0 {
		if err := checkInt("limit", pg.Limit, 1, 200); err != nil {
			return err
		}
		q.Set("limit", strconv.Itoa(pg.Limit))
	}
	if pg.Cursor != "" {
		q.Set("cursor", pg.Cursor)
	}
	return nil
}

func pagePatch(props map[string]*jsonschema.Schema, sorts ...string) {
	enum(props, "sort", sorts...)
	enum(props, "order", "asc", "desc")
	intRange(props, "limit", 1, 200)
	maxLen(props, "cursor", 1000)
}

// projectFilters are the project filters of contract 8.2
type projectFilters struct {
	Status       []string `json:"status,omitempty" jsonschema:"only projects in these statuses"`
	Verified     *bool    `json:"verified,omitempty" jsonschema:"only projects with (true) or without (false) a verified domain"`
	StellarToml  *bool    `json:"stellar_toml,omitempty" jsonschema:"only projects with (true) or without (false) a stellar.toml naming the site"`
	HasWebData   *bool    `json:"has_web_data,omitempty" jsonschema:"only projects with (true) or without (false) page views in the range"`
	HasAPIData   *bool    `json:"has_api_data,omitempty" jsonschema:"only projects with (true) or without (false) API requests in the range"`
	EnrolledFrom string   `json:"enrolled_from,omitempty" jsonschema:"only projects enrolled on or after this UTC day"`
	EnrolledTo   string   `json:"enrolled_to,omitempty" jsonschema:"only projects enrolled on or before this UTC day"`
	Search       string   `json:"q,omitempty" jsonschema:"case-insensitive text in the project name or slug, 1 to 100 characters"`
}

func (f projectFilters) values(q url.Values) error {
	if err := statusList(q, f.Status); err != nil {
		return err
	}
	for k, v := range map[string]*bool{"verified": f.Verified, "stellar_toml": f.StellarToml,
		"has_web_data": f.HasWebData, "has_api_data": f.HasAPIData} {
		if v != nil {
			q.Set(k, strconv.FormatBool(*v))
		}
	}
	for k, v := range map[string]string{"enrolled_from": f.EnrolledFrom, "enrolled_to": f.EnrolledTo} {
		d, err := dayOf(k, v)
		if err != nil {
			return err
		}
		if d != "" {
			q.Set(k, d)
		}
	}
	if f.Search != "" {
		if err := text("q", f.Search, 400); err != nil {
			return err
		}
		q.Set("q", f.Search)
	}
	return nil
}

func filtersPatch(props map[string]*jsonschema.Schema) {
	statusPatch(props)
	maxLen(props, "q", 100)
}

func (s *Server) operatorRead(ctx context.Context, p Principal, path string, q url.Values) (any, error) {
	var out json.RawMessage
	if err := s.read(ctx, p, "/operator"+path, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) addOperatorTools(srv *mcp.Server) {
	type tenantsIn struct {
		pageIn
		Status []string `json:"status,omitempty" jsonschema:"only tenants in these statuses"`
		Review bool     `json:"review,omitempty" jsonschema:"true: only tenants that need an operator (pending, or sites in review)"`
		Search string   `json:"q,omitempty" jsonschema:"case-insensitive text in the name, slug, website or a site domain, 1 to 100 characters"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "list_tenants",
		Title: "Search projects",
		Description: "Lists and searches the enrolled projects (tenants) with their status, enrollment date, " +
			"site counts, sites in review and verification. Search by name, slug, website or site domain; filter " +
			"by status or to projects needing review. Use the id with get_tenant and the compare tools.",
	}, func(props map[string]*jsonschema.Schema) {
		pagePatch(props, "created_at", "status_changed_at", "name")
		statusPatch(props)
		maxLen(props, "q", 100)
	}, func(ctx context.Context, p Principal, in tenantsIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		if err := statusList(q, in.Status); err != nil {
			return nil, err
		}
		if in.Review {
			q.Set("review", "true")
		}
		if in.Search != "" {
			if err := text("q", in.Search, 400); err != nil {
				return nil, err
			}
			q.Set("q", in.Search)
		}
		return s.operatorRead(ctx, p, "/tenants", q)
	})

	type tenantIn struct {
		TenantID string `json:"tenant_id" jsonschema:"tenant id from list_tenants"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_tenant",
		Title: "One project",
		Description: "The operator detail of one project: status and reason, enrollment answers, members, sites " +
			"with verification and stellar.toml results, and removal progress.",
	}, func(props map[string]*jsonschema.Schema) { uuidFormat(props, "tenant_id") }, func(ctx context.Context, p Principal, in tenantIn) (any, error) {
		id, err := parseUUID("tenant_id", in.TenantID)
		if err != nil {
			return nil, err
		}
		return s.operatorRead(ctx, p, "/tenants/"+id.String(), nil)
	})

	type webProjectsIn struct {
		dayRangeIn
		projectFilters
		pageIn
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_global_web_overview",
		Title: "Web metrics of every project",
		Description: "Ranks every project on its web metrics over whole UTC days: page views, visitors, visits, " +
			"bounces and bounce rate, visit duration, quarantined (likely automated) traffic, gateway rejections and " +
			"the top referrers, countries, devices and browsers. Sort by any metric to find the most or least used " +
			"projects; filter by status, verification, stellar.toml or data presence.",
	}, func(props map[string]*jsonschema.Schema) {
		filtersPatch(props)
		pagePatch(props, "name", "status", "created_at", "sites", "sites_verified", "sites_with_stellar_toml",
			"pageviews", "visitors", "visits", "bounces", "totaltime", "bounce_rate", "avg_visit_duration",
			"quarantine_pageviews", "quarantine_visitors", "quarantine_share", "ingest_requests", "ingest_rejected",
			"rejected_share")
	}, func(ctx context.Context, p Principal, in webProjectsIn) (any, error) {
		q := url.Values{}
		for _, f := range []func(url.Values) error{in.dayRangeIn.values, in.projectFilters.values, in.pageIn.values} {
			if err := f(q); err != nil {
				return nil, err
			}
		}
		return s.operatorRead(ctx, p, "/web/projects", q)
	})

	type apiProjectsIn struct {
		dayRangeIn
		projectFilters
		pageIn
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_global_api_overview",
		Title: "API metrics of every project",
		Description: "Ranks every project on its API metrics over whole UTC days: requests, 4xx and 5xx errors " +
			"and rates, latency percentiles p50, p95 and p99 (estimated from histogram buckets), services and " +
			"routes. Sort by requests to find high and low usage services.",
	}, func(props map[string]*jsonschema.Schema) {
		filtersPatch(props)
		pagePatch(props, "name", "status", "created_at", "sites", "requests", "requests_4xx", "requests_5xx",
			"rate_4xx", "rate_5xx", "p50", "p95", "p99", "services", "routes")
	}, func(ctx context.Context, p Principal, in apiProjectsIn) (any, error) {
		q := url.Values{}
		for _, f := range []func(url.Values) error{in.dayRangeIn.values, in.projectFilters.values, in.pageIn.values} {
			if err := f(q); err != nil {
				return nil, err
			}
		}
		return s.operatorRead(ctx, p, "/api/projects", q)
	})

	type compareWebIn struct {
		dayRangeIn
		TenantIDs      []string `json:"tenant_ids" jsonschema:"1 to 5 tenant ids from list_tenants"`
		BreakdownLimit int      `json:"breakdown_limit,omitempty" jsonschema:"keys per breakdown, 1 to 500, default 10"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "compare_tenants_web",
		Title: "Compare projects (web)",
		Description: "Puts 1 to 5 projects side by side on web metrics: totals, one point per UTC day, and " +
			"breakdowns by referrer, country, device, browser and operating system. The same data as the console's " +
			"compare view.",
	}, func(props map[string]*jsonschema.Schema) {
		lo, hi := 1, 5
		props["tenant_ids"].MinItems, props["tenant_ids"].MaxItems = &lo, &hi
		setUUID(props["tenant_ids"].Items)
		intRange(props, "breakdown_limit", 1, 500)
	}, func(ctx context.Context, p Principal, in compareWebIn) (any, error) {
		ids, err := joinIDs("tenant_ids", in.TenantIDs, 1, 5)
		if err != nil {
			return nil, err
		}
		q := url.Values{"tenants": {ids}}
		if err := in.values(q); err != nil {
			return nil, err
		}
		if in.BreakdownLimit != 0 {
			if err := checkInt("breakdown_limit", in.BreakdownLimit, 1, 500); err != nil {
				return nil, err
			}
			q.Set("breakdown_limit", strconv.Itoa(in.BreakdownLimit))
		}
		return s.operatorRead(ctx, p, "/web/compare", q)
	})

	type compareAPIIn struct {
		dayRangeIn
		TenantIDs    []string `json:"tenant_ids" jsonschema:"1 to 5 tenant ids from list_tenants"`
		ServiceOrder string   `json:"service_order,omitempty" jsonschema:"desc (default): busiest service first; asc: quietest first"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "compare_tenants_api",
		Title: "Compare projects (API)",
		Description: "Puts 1 to 5 projects side by side on API metrics: totals, one point per UTC day, and every " +
			"service with its requests, errors, percentiles and routes. Percentiles are estimated from histogram " +
			"buckets.",
	}, func(props map[string]*jsonschema.Schema) {
		lo, hi := 1, 5
		props["tenant_ids"].MinItems, props["tenant_ids"].MaxItems = &lo, &hi
		setUUID(props["tenant_ids"].Items)
		enum(props, "service_order", "asc", "desc")
	}, func(ctx context.Context, p Principal, in compareAPIIn) (any, error) {
		ids, err := joinIDs("tenant_ids", in.TenantIDs, 1, 5)
		if err != nil {
			return nil, err
		}
		q := url.Values{"tenants": {ids}}
		if err := in.values(q); err != nil {
			return nil, err
		}
		if in.ServiceOrder != "" {
			q.Set("service_order", in.ServiceOrder)
		}
		return s.operatorRead(ctx, p, "/api/compare", q)
	})
	s.addOnchainOperatorTools(srv)
}
