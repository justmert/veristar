// Package apimetrics serves a tenant's api metrics (control-plane-api
// section 7): overview, timeseries, routes, status, services
// and warnings under /api/tenants/{tenant_id}/api-metrics/. every query runs
// under cp_tenant through db.WithTenant, so row-level security limits it to
// the tenant even if a handler named the wrong one.
package apimetrics

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// Routes are the api metrics routes.
type Routes struct {
	s  *tenants.Scope
	rv Resolver
}

// NewRoutes needs the scope and a resolver whose retention horizons are
// the ones the jobs retention task runs with.
func NewRoutes(s *tenants.Scope, rv Resolver) (*Routes, error) {
	if s == nil {
		return nil, errors.New("apimetrics: scope is required")
	}
	if err := rv.check(); err != nil {
		return nil, err
	}
	return &Routes{s: s, rv: rv}, nil
}

const base = "/tenants/{tenant_id}/api-metrics"

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodGet, base+"/overview", httpx.HandlerFunc(h.overview))
	r.Method(http.MethodGet, base+"/timeseries", httpx.HandlerFunc(h.timeseries))
	r.Method(http.MethodGet, base+"/routes", httpx.HandlerFunc(h.routes))
	r.Method(http.MethodGet, base+"/status", httpx.HandlerFunc(h.status))
	r.Method(http.MethodGet, base+"/services", httpx.HandlerFunc(h.services))
	r.Method(http.MethodGet, base+"/warnings", httpx.HandlerFunc(h.warnings))
}

// parameter sets of contract 7.1
var (
	rangeParams    = []string{"range", "from", "to"}
	filteredParams = append(append([]string{}, rangeParams...), "service", "route", "method")
	routeParams    = append(append([]string{}, filteredParams...), "sort", "order", "limit", "cursor")
)

// request is a parsed request: the verified tenant, the range and the
// filters. membership is checked first, so a non-member gets 404 whatever
// the query holds and cannot probe for validation answers.
type request struct {
	access  tenants.Access
	q       url.Values
	rng     Range
	filters Filters
}

func (h *Routes) parse(r *http.Request, allowed []string) (request, error) {
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return request{}, err
	}
	// r.URL.Query drops pairs it cannot parse (a semicolon, a bad escape)
	// without a word; a dropped filter would widen the answer, so refuse
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return request{}, httpx.BadQuery("the query string is malformed")
	}
	if err := single(q, allowed...); err != nil {
		return request{}, err
	}
	rng, err := h.rv.Parse(q)
	if err != nil {
		return request{}, err
	}
	f, err := ParseFilters(q)
	if err != nil {
		return request{}, err
	}
	return request{access: a, q: q, rng: rng, filters: f}, nil
}

// read runs fn under cp_tenant for the request's tenant
func (h *Routes) read(ctx context.Context, req request, fn func(*tenantq.Queries) error) error {
	return h.s.InTenant(ctx, req.access, func(tx pgx.Tx) error {
		return fn(tenantq.New(tx))
	})
}

// periodSummary selects the sources of a range and sums it
func periodSummary(ctx context.Context, q *tenantq.Queries, rng Range, f Filters) (Period, error) {
	sel, err := Select(ctx, q, rng, f.Service)
	if err != nil {
		return Period{}, err
	}
	row, err := q.APIMetricSummary(ctx, tenantq.APIMetricSummaryParams{
		Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
		MetricServices: sel.Metric, SpanServices: sel.Span, Route: f.Route, Method: f.Method,
	})
	if err != nil {
		return Period{}, fmt.Errorf("apimetrics: summary: %w", err)
	}
	s, err := summary(row.Requests, row.Errors4xx, row.Errors5xx, row.Lossy, row.P50, row.P95, row.P99)
	if err != nil {
		return Period{}, err
	}
	return Period{Range: rangeJSON(rng), Sources: sel.Sources, Summary: s}, nil
}

func (h *Routes) overview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, filteredParams)
	if err != nil {
		return err
	}
	prev := h.rv.Previous(req.rng)
	var out Overview
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		if out.Current, err = periodSummary(ctx, q, req.rng, req.filters); err != nil {
			return err
		}
		out.Previous, err = periodSummary(ctx, q, prev, req.filters)
		return err
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) timeseries(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, filteredParams)
	if err != nil {
		return err
	}
	rng, f := req.rng, req.filters
	var sel Selection
	var rows []tenantq.APIMetricTimeseriesRow
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		if sel, err = Select(ctx, q, rng, f.Service); err != nil {
			return err
		}
		rows, err = q.APIMetricTimeseries(ctx, tenantq.APIMetricTimeseriesParams{
			Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
			MetricServices: sel.Metric, SpanServices: sel.Span, Route: f.Route, Method: f.Method,
		})
		return err
	}); err != nil {
		return err
	}
	byStart := make(map[int64]Summary, len(rows))
	for _, row := range rows {
		s, err := summary(row.Requests, row.Errors4xx, row.Errors5xx, row.Lossy, row.P50, row.P95, row.P99)
		if err != nil {
			return err
		}
		byStart[row.BucketStart.Unix()] = s
	}
	// every bucket of the range, empty ones included, so a chart needs no
	// gap filling (contract 7.6)
	out := Timeseries{Range: rangeJSON(rng), Sources: sel.Sources, Points: make([]Point, 0, rng.Buckets())}
	for t := rng.From; t.Before(rng.To); t = rng.Resolution.Next(t) {
		s, ok := byStart[t.Unix()]
		if ok {
			delete(byStart, t.Unix())
		}
		out.Points = append(out.Points, Point{At: httpx.Timestamp(t), Summary: s})
	}
	if len(byStart) != 0 {
		// a row outside the bucket grid would mean the table's alignment
		// check or the range arithmetic is wrong; never drop it silently
		return fmt.Errorf("apimetrics: %d timeseries rows fell outside the bucket grid of %s", len(byStart), rng.Resolution)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// sort keys of contract 7.7
var sortKeys = map[string]bool{
	"requests": true, "errors_4xx": true, "errors_5xx": true, "rate_4xx": true, "rate_5xx": true,
	"p50": true, "p95": true, "p99": true, "route": true,
}

const routesCursorName = "api-metrics-routes"

// routesCursor is the json inside a routes cursor: the offset of the next
// page and a digest of every parameter that shapes the list, so a cursor
// is refused when any of them changed instead of paging another list.
type routesCursor struct {
	R string `json:"r"`
	O int    `json:"o"`
	H string `json:"h"`
}

// maxOffset keeps offset plus limit inside int32
const maxOffset = math.MaxInt32 - 1000

func listDigest(q url.Values, sortKey string, desc bool) string {
	shape := url.Values{}
	for _, k := range filteredParams {
		if v, ok := q[k]; ok {
			shape[k] = v
		}
	}
	shape.Set("sort", sortKey)
	shape.Set("order", strconv.FormatBool(desc))
	sum := sha256.Sum256([]byte(shape.Encode()))
	return hex.EncodeToString(sum[:16])
}

func encodeCursor(offset int, digest string) *string {
	b, err := json.Marshal(routesCursor{R: routesCursorName, O: offset, H: digest})
	if err != nil {
		// an int and two strings always encode
		panic(err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(raw, digest string) (int, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, httpx.BadCursor()
	}
	var c routesCursor
	if err := json.Unmarshal(b, &c); err != nil || c.R != routesCursorName || c.H != digest ||
		c.O < 1 || c.O > maxOffset {
		return 0, httpx.BadCursor()
	}
	return c.O, nil
}

func (h *Routes) routes(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, routeParams)
	if err != nil {
		return err
	}
	q := req.q
	sortKey := "requests"
	if v, ok := q["sort"]; ok {
		if !sortKeys[v[0]] {
			return httpx.BadQuery("sort must be one of requests, errors_4xx, errors_5xx, rate_4xx, rate_5xx, p50, p95, p99, route")
		}
		sortKey = v[0]
	}
	desc := sortKey != "route"
	if v, ok := q["order"]; ok {
		switch v[0] {
		case "asc":
			desc = false
		case "desc":
			desc = true
		default:
			return httpx.BadQuery("order must be asc or desc")
		}
	}
	limit := httpx.DefaultLimit
	if v, ok := q["limit"]; ok {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 1 || n > httpx.MaxLimit {
			return httpx.BadQuery("limit must be an integer between 1 and 200")
		}
		limit = n
	}
	digest := listDigest(q, sortKey, desc)
	offset := 0
	if v, ok := q["cursor"]; ok {
		if offset, err = decodeCursor(v[0], digest); err != nil {
			return err
		}
	}

	rng, f := req.rng, req.filters
	var sel Selection
	var rows []tenantq.APIMetricRoutesRow
	var total int64
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		if sel, err = Select(ctx, q, rng, f.Service); err != nil {
			return err
		}
		rows, err = q.APIMetricRoutes(ctx, tenantq.APIMetricRoutesParams{
			SortKey: sortKey, Descending: desc,
			RowOffset:  int32(offset),    //nolint:gosec // bounded by maxOffset
			RowLimit:   int32(limit + 1), //nolint:gosec // at most 201
			Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
			MetricServices: sel.Metric, SpanServices: sel.Span, Route: f.Route, Method: f.Method,
		})
		if err != nil {
			return fmt.Errorf("apimetrics: routes: %w", err)
		}
		total, err = q.APIMetricRouteCount(ctx, tenantq.APIMetricRouteCountParams{
			Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
			MetricServices: sel.Metric, SpanServices: sel.Span, Route: f.Route, Method: f.Method,
		})
		return err
	}); err != nil {
		return err
	}
	out := RouteList{Range: rangeJSON(rng), Sources: sel.Sources, Total: total, Items: []RouteItem{}}
	for i, row := range rows {
		if i == limit {
			out.NextCursor = encodeCursor(offset+limit, digest)
			break
		}
		s, err := summary(row.Requests, row.Errors4xx, row.Errors5xx, row.Lossy, row.P50, row.P95, row.P99)
		if err != nil {
			return err
		}
		out.Items = append(out.Items, RouteItem{Service: row.Service, Method: row.Method, Route: row.Route, Source: row.Source, Summary: s})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) status(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, filteredParams)
	if err != nil {
		return err
	}
	rng, f := req.rng, req.filters
	var sel Selection
	var rows []tenantq.APIMetricStatusCodesRow
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		if sel, err = Select(ctx, q, rng, f.Service); err != nil {
			return err
		}
		rows, err = q.APIMetricStatusCodes(ctx, tenantq.APIMetricStatusCodesParams{
			Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
			MetricServices: sel.Metric, SpanServices: sel.Span, Route: f.Route, Method: f.Method,
		})
		return err
	}); err != nil {
		return err
	}
	out := Status{Range: rangeJSON(rng), Sources: sel.Sources, Classes: make([]ClassCount, len(classOrder)), Codes: []CodeCount{}}
	idx := map[string]int{}
	for i, c := range classOrder {
		out.Classes[i] = ClassCount{Class: c}
		idx[c] = i
	}
	for _, row := range rows {
		c := classOf(row.StatusCode)
		out.Codes = append(out.Codes, CodeCount{StatusCode: row.StatusCode, Class: c, Requests: row.Requests})
		out.Classes[idx[c]].Requests += row.Requests
		out.Requests += row.Requests
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) services(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, rangeParams)
	if err != nil {
		return err
	}
	rng := req.rng
	var sel Selection
	var rows []tenantq.APIMetricServiceTotalsRow
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		if sel, err = Select(ctx, q, rng, nil); err != nil {
			return err
		}
		rows, err = q.APIMetricServiceTotals(ctx, tenantq.APIMetricServiceTotalsParams{
			Resolution: string(rng.Resolution), RangeFrom: rng.From, RangeTo: rng.To,
			MetricServices: sel.Metric, SpanServices: sel.Span,
		})
		return err
	}); err != nil {
		return err
	}
	totals := make(map[string]tenantq.APIMetricServiceTotalsRow, len(rows))
	for _, row := range rows {
		totals[row.Service] = row
	}
	out := Services{Range: rangeJSON(rng), Items: make([]ServiceItem, 0, len(sel.Sources))}
	for _, s := range sel.Sources {
		t, ok := totals[s.Service]
		if !ok {
			// both statements read the same committed rows of the selected
			// source unless a purge ran between them; say so rather than
			// report a service with an invented zero
			return fmt.Errorf("apimetrics: service %q has a source but no rows", s.Service)
		}
		out.Items = append(out.Items, ServiceItem{Source: s, Requests: t.Requests, Lossy: t.Lossy})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Routes) warnings(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req, err := h.parse(r, rangeParams)
	if err != nil {
		return err
	}
	rng := req.rng
	// the utc days the range touches: from the day of from to the day of
	// the last instant before to
	first := Day.Floor(rng.From)
	last := Day.Floor(rng.To.Add(-time.Nanosecond))
	var rows []tenantq.APIMetricWarningsRow
	if err := h.read(ctx, req, func(q *tenantq.Queries) error {
		var err error
		rows, err = q.APIMetricWarnings(ctx, tenantq.APIMetricWarningsParams{FirstDay: first, LastDay: last})
		return err
	}); err != nil {
		return err
	}
	out := Warnings{Range: rangeJSON(rng), Totals: make([]WarningTotal, len(warningKinds)), Days: make([]WarningDay, 0, len(rows))}
	idx := map[string]int{}
	for i, k := range warningKinds {
		out.Totals[i] = WarningTotal{Kind: k}
		idx[k] = i
	}
	for _, row := range rows {
		i, ok := idx[row.Kind]
		if !ok {
			return fmt.Errorf("apimetrics: unknown warning kind %q", row.Kind)
		}
		day := row.Day.UTC().Format(time.DateOnly)
		out.Days = append(out.Days, WarningDay{Day: day, Kind: row.Kind, Count: row.Count})
		out.Totals[i].Count += row.Count
		// rows come in day order, so the last one seen is the latest
		d := day
		out.Totals[i].LastDay = &d
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
