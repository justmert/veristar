package operator

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
)

// "what scf sees" (contract 16.3): a member reads the figures the operator
// views show about its own project, computed by the operator views' own
// sql under cp_operator, so both see the same numbers for the same range.
// the tenant id comes only from the member's verified membership; the
// route never names another project and takes no tenant list.

// SCFView serves GET /api/tenants/{tenant_id}/scf-view.
type SCFView struct {
	scope *tenants.Scope
	db    *db.DB
	now   func() time.Time
}

// NewSCFView needs the tenant scope and a database with the cp_operator pool.
func NewSCFView(s *tenants.Scope, d *db.DB, now func() time.Time) (*SCFView, error) {
	if s == nil || d == nil || now == nil {
		return nil, errors.New("operator: the scf view needs a scope, a database and a clock")
	}
	return &SCFView{scope: s, db: d, now: now}, nil
}

// Routes registers the path under a router mounted at /api.
func (v *SCFView) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/scf-view", httpx.HandlerFunc(v.get))
}

// SCFWebRow is the project's row of the global web view.
type SCFWebRow struct {
	Pageviews           int64    `json:"pageviews"`
	Visitors            int64    `json:"visitors"`
	Visits              int64    `json:"visits"`
	Bounces             int64    `json:"bounces"`
	Totaltime           int64    `json:"totaltime"`
	BounceRate          *float64 `json:"bounce_rate"`
	AvgVisitDuration    *float64 `json:"avg_visit_duration"`
	QuarantinePageviews int64    `json:"quarantine_pageviews"`
	QuarantineVisitors  int64    `json:"quarantine_visitors"`
	QuarantineShare     *float64 `json:"quarantine_share"`
	IngestRequests      int64    `json:"ingest_requests"`
	IngestRejected      int64    `json:"ingest_rejected"`
	RejectedShare       *float64 `json:"rejected_share"`
}

// SCFAPIRow is the project's row of the global api view.
type SCFAPIRow struct {
	APINumbers
	Services     int64   `json:"services"`
	Routes       int64   `json:"routes"`
	SpanServices int64   `json:"span_services"`
	Source       *string `json:"source"`
	Lossy        bool    `json:"lossy"`
}

// SCFViewAnswer is the answer of GET /api/tenants/{tenant_id}/scf-view.
// InOperatorViews is false for a project the operator views do not list
// (an internal project); its rows are then null.
type SCFViewAnswer struct {
	From            string           `json:"from"`
	To              string           `json:"to"`
	Days            []string         `json:"days"`
	InOperatorViews bool             `json:"in_operator_views"`
	Tenant          *TenantRef       `json:"tenant"`
	Sites           *SiteCounts      `json:"sites"`
	HasWebData      bool             `json:"has_web_data"`
	HasAPIData      bool             `json:"has_api_data"`
	Web             *SCFWebRow       `json:"web"`
	API             *SCFAPIRow       `json:"api"`
	Onchain         *onchain.Summary `json:"onchain"`
	// the integrity grade of the project's row in the global web view
	// (contract 17.3), present when the views list the project
	Integrity     *IntegrityGrade    `json:"integrity,omitempty"`
	LinkTwoWay    bool               `json:"link_two_way"`
	WebSeries     []WebPoint         `json:"web_series"`
	APISeries     []APIPoint         `json:"api_series"`
	OnchainSeries []onchain.DayTotal `json:"onchain_series"`
}

// noFilter is the list query of an unfiltered list over rg: the scf view
// reads the project's row out of it.
func noFilter(rg dayRange) listQuery {
	return listQuery{Range: rg, Filter: projectFilter{Statuses: []string{}}, Limit: 1}
}

func (v *SCFView) get(w http.ResponseWriter, r *http.Request) error {
	a, err := v.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	now := v.now()
	rg, err := parseDayRange(r.URL.Query(), now, DefaultRangeDays, MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	id := a.TenantID
	out := SCFViewAnswer{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Days: dayStrings(rg)}
	err = v.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		args := noFilter(rg).args()
		args["tenant"] = id
		var t TenantRef
		var sc SiteCounts
		var web SCFWebRow
		var at time.Time
		var grade IntegrityGrade
		err := tx.QueryRow(ctx, webListCTE+`
			select id, slug, name, status, created_at, sites, sites_verified, sites_with_stellar_toml,
			       has_web_data, has_api_data, pageviews, visitors, visits, bounces, totaltime,
			       bounce_rate, avg_visit_duration, quarantine_pageviews, quarantine_visitors, quarantine_share,
			       ingest_requests, ingest_rejected, rejected_share, days_a, days_b, days_c
			from y where id = @tenant`, args).Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &at,
			&sc.Sites, &sc.SitesVerified, &sc.SitesWithStellarToml, &out.HasWebData, &out.HasAPIData,
			&web.Pageviews, &web.Visitors, &web.Visits, &web.Bounces, &web.Totaltime,
			&web.BounceRate, &web.AvgVisitDuration, &web.QuarantinePageviews, &web.QuarantineVisitors, &web.QuarantineShare,
			&web.IngestRequests, &web.IngestRejected, &web.RejectedShare, &grade.DaysA, &grade.DaysB, &grade.DaysC)
		if errors.Is(err, pgx.ErrNoRows) {
			// the operator views do not list this project
			return nil
		}
		if err != nil {
			return fmt.Errorf("web row: %w", err)
		}
		t.CreatedAt = httpx.Timestamp(at)
		grade.derive()
		out.InOperatorViews, out.Tenant, out.Sites, out.Web, out.Integrity = true, &t, &sc, &web, &grade
		var api SCFAPIRow
		var p50, p95, p99 *float64
		err = tx.QueryRow(ctx, apiListCTE+`
			select requests, requests_4xx, requests_5xx, rate_4xx, rate_5xx, p50, p95, p99,
			       services, routes, span_services, lossy
			from y where id = @tenant`, args).Scan(&api.Requests, &api.Requests4xx, &api.Requests5xx, &api.Rate4xx, &api.Rate5xx,
			&p50, &p95, &p99, &api.Services, &api.Routes, &api.SpanServices, &api.Lossy)
		if err != nil {
			return fmt.Errorf("api row: %w", err)
		}
		api.Percentiles = newPercentiles(p50, p95, p99)
		api.Source = source(api.Services, api.SpanServices)
		out.API = &api

		ids := []uuid.UUID{id}
		series, _, err := webSeries(ctx, tx, ids, rg)
		if err != nil {
			return err
		}
		out.WebSeries = series[id]
		apiS, err := apiSeries(ctx, tx, pgx.NamedArgs{"ids": ids, "from_at": rg.From, "to_end": rg.end()}, rg)
		if err != nil {
			return err
		}
		out.APISeries = apiS[id]
		if out.APISeries == nil {
			out.APISeries = emptyAPISeries(rg)
		}
		chainOf, err := compareOnchain(ctx, tx, ids, rg, now)
		if err != nil {
			return err
		}
		s := chainOf[id].Totals
		out.Onchain = &s
		out.OnchainSeries = chainOf[id].Series
		view, err := onchain.Load(ctx, tx, onchain.OperatorSites, id, rg.onchain(), utcDay(now))
		if err != nil {
			return err
		}
		out.LinkTwoWay = view.Link.TwoWay
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: scf view: %w", err)
	}
	if out.WebSeries == nil {
		out.WebSeries = []WebPoint{}
		out.APISeries = []APIPoint{}
		out.OnchainSeries = []onchain.DayTotal{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
