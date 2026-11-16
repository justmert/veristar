package exports

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/apimetrics"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// OverviewHeader is the header row of the tenant overview export
// (contract 16.2): one row per utc day.
var OverviewHeader = []string{
	"day",
	"web_pageviews", "web_visitors", "web_visits", "web_bounces", "web_totaltime_seconds",
	"web_quarantine_pageviews",
	"api_requests", "api_errors_4xx", "api_errors_5xx",
	"onchain_ids_read", "onchain_operations", "onchain_transactions",
}

// Overview serves GET /api/tenants/{tenant_id}/overview.csv.
type Overview struct {
	scope *tenants.Scope
	now   func() time.Time
}

// NewOverview needs the tenant scope.
func NewOverview(s *tenants.Scope, now func() time.Time) (*Overview, error) {
	if s == nil || now == nil {
		return nil, errors.New("exports: scope and clock are required")
	}
	return &Overview{scope: s, now: now}, nil
}

// Routes registers the path under a router mounted at /api.
func (o *Overview) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/overview.csv", httpx.HandlerFunc(o.overview))
}

type webDay struct {
	pageviews, visitors, visits, bounces, totaltime, quarantine int64
}

// overview reads every number under cp_tenant, so row-level security
// keys each table on the caller's tenant: agg.web_daily (the numbers
// operators see, read from umami per day), the api daily series the api
// traffic page shows, and agg.chain_daily. a day without a row for a
// signal has empty cells for it.
func (o *Overview) overview(w http.ResponseWriter, r *http.Request) error {
	a, err := o.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	now := o.now()
	rg, err := onchain.ParseRange(r.URL.Query(), now, onchain.DefaultRangeDays, onchain.MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	web := map[string]webDay{}
	api := map[string]tenantq.APIMetricTimeseriesRow{}
	var chain map[string]onchain.DayTotal
	err = o.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select day, coalesce(sum(pageviews) filter (where band = 'primary'), 0)::bigint,
			       coalesce(sum(visitors) filter (where band = 'primary'), 0)::bigint,
			       coalesce(sum(visits) filter (where band = 'primary'), 0)::bigint,
			       coalesce(sum(bounces) filter (where band = 'primary'), 0)::bigint,
			       coalesce(sum(totaltime) filter (where band = 'primary'), 0)::bigint,
			       coalesce(sum(pageviews) filter (where band = 'quarantine'), 0)::bigint
			from agg.web_daily where day between $1::date and $2::date group by day`, rg.From, rg.To)
		if err != nil {
			return err
		}
		for rows.Next() {
			var day time.Time
			var d webDay
			if err := rows.Scan(&day, &d.pageviews, &d.visitors, &d.visits, &d.bounces, &d.totaltime, &d.quarantine); err != nil {
				rows.Close()
				return err
			}
			web[day.Format(time.DateOnly)] = d
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		q := tenantq.New(tx)
		ar := apimetrics.Range{From: rg.From, To: rg.To.AddDate(0, 0, 1), Resolution: apimetrics.Day}
		sel, err := apimetrics.Select(ctx, q, ar, nil)
		if err != nil {
			return err
		}
		series, err := q.APIMetricTimeseries(ctx, tenantq.APIMetricTimeseriesParams{
			Resolution: string(ar.Resolution), RangeFrom: ar.From, RangeTo: ar.To,
			MetricServices: sel.Metric, SpanServices: sel.Span,
		})
		if err != nil {
			return err
		}
		for _, p := range series {
			api[p.BucketStart.UTC().Format(time.DateOnly)] = p
		}
		totals, err := onchain.DailyTotals(ctx, tx, []uuid.UUID{a.TenantID}, rg)
		if err != nil {
			return err
		}
		chain = map[string]onchain.DayTotal{}
		for _, p := range totals[a.TenantID] {
			chain[p.Day] = p
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionExport, Target: audit.TenantTarget(a.TenantID),
			Detail: map[string]any{
				"tenant_id": a.TenantID.String(), "view": "tenant_overview",
				"from": rg.From.Format(time.DateOnly), "to": rg.To.Format(time.DateOnly), "rows": len(rg.Days()),
			},
		})
	})
	if err != nil {
		return err
	}
	cw, err := NewWriter(w, FileName("veristar-overview", a.TenantID.String()[:8], rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly)), OverviewHeader)
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, d := range rg.Days() {
		day := d.Format(time.DateOnly)
		row := []string{day}
		if x, ok := web[day]; ok {
			row = append(row, Int(x.pageviews), Int(x.visitors), Int(x.visits), Int(x.bounces), Int(x.totaltime), Int(x.quarantine))
		} else {
			row = append(row, "", "", "", "", "", "")
		}
		// the api series has no row for a day without requests; a day the
		// writer has no data for is a zero there, as on the api traffic page
		p := api[day]
		row = append(row, Int(p.Requests), Int(p.Errors4xx), Int(p.Errors5xx))
		c := chain[day]
		row = append(row, Int(c.IDsRead), IntPtr(c.Operations), IntPtr(c.Transactions))
		if err := cw.Row(row...); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
