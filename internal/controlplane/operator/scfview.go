package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/coverageview"
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
	// Coverage estimates the visits not measured because of privacy
	// signals, the column of the global web view (contract 17.5)
	Coverage coverageview.Estimate `json:"coverage"`
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
	Integrity  *IntegrityGrade `json:"integrity,omitempty"`
	LinkTwoWay bool            `json:"link_two_way"`
	// the project's cohort record, the cohort its percentiles come from
	// (contract 19.3) and its row's cohort, growth and percentiles
	Cohort           CohortRecord      `json:"cohort"`
	PercentileCohort *PercentileCohort `json:"percentile_cohort"`
	Comparison       *Comparison       `json:"comparison"`
	// the saved rules the project meets today, with the evidence
	RulesMet      []SCFRule          `json:"rules_met"`
	WebSeries     []WebPoint         `json:"web_series"`
	APISeries     []APIPoint         `json:"api_series"`
	OnchainSeries []onchain.DayTotal `json:"onchain_series"`
}

// PercentileCohort names the population of a project's percentiles in the
// "what scf sees" view and in a usage report: the projects confirmed in its
// program and round, or every listed project when it has no confirmed
// cohort (both fields null).
type PercentileCohort struct {
	Program *string `json:"program"`
	Round   *string `json:"round"`
}

// SCFRule is a rule the project meets and its evidence.
type SCFRule struct {
	Rule  Rule      `json:"rule"`
	Match RuleMatch `json:"match"`
}

// ownCohortFilter is the percentile population of a project: its
// confirmed program and round, or no cohort filter without one.
func ownCohortFilter(c CohortRecord) (cohortFilter, PercentileCohort) {
	if c.Confirmed == nil {
		return cohortFilter{}, PercentileCohort{}
	}
	return cohortFilter{Program: c.Confirmed.Program, Round: c.Confirmed.Round},
		PercentileCohort{Program: c.Confirmed.Program, Round: c.Confirmed.Round}
}

// RulesMet evaluates every saved rule for one project on asOf and keeps
// the ones it meets.
func RulesMet(ctx context.Context, tx pgx.Tx, id uuid.UUID, asOf time.Time) ([]SCFRule, error) {
	rules, err := ListRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []SCFRule{}
	for _, rule := range rules {
		m, err := EvaluateRule(ctx, tx, rule, asOf, &id)
		if err != nil {
			return nil, err
		}
		for _, it := range m.Items {
			if it.Meets {
				out = append(out, SCFRule{Rule: rule, Match: it})
			}
		}
	}
	return out, nil
}

// noFilter is the list query of an unfiltered list over rg: the scf view
// reads the project's row out of it.
func noFilter(rg dayRange) listQuery {
	return listQuery{Range: rg, Filter: projectFilter{Statuses: []string{}}, Limit: 1}
}

// LoadSCFView reads the "what scf sees" answer of one project over
// [from, to] inside a cp_operator transaction whose snapshot the caller
// set. usage reports read their numbers through it, so a report and the
// view show the same figures for the same range.
func LoadSCFView(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to, now time.Time) (SCFViewAnswer, error) {
	rg := dayRange{From: from, To: to}
	out := SCFViewAnswer{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Days: dayStrings(rg)}
	if err := loadSCF(ctx, tx, id, rg, now, &out); err != nil {
		return out, fmt.Errorf("operator: scf view: %w", err)
	}
	if out.RulesMet == nil {
		out.RulesMet = []SCFRule{}
	}
	if out.WebSeries == nil {
		out.WebSeries = []WebPoint{}
		out.APISeries = []APIPoint{}
		out.OnchainSeries = []onchain.DayTotal{}
	}
	return out, nil
}

func loadSCF(ctx context.Context, tx pgx.Tx, id uuid.UUID, rg dayRange, now time.Time, out *SCFViewAnswer) error {
	cohort, err := readCohort(ctx, tx, id)
	if err != nil {
		return err
	}
	out.Cohort = cohort
	lq := noFilter(rg)
	var pc PercentileCohort
	lq.Filter.Cohort, pc = ownCohortFilter(cohort)
	args := lq.args()
	args["tenant"] = id
	p, err := scanWebRow(tx.QueryRow(ctx, webListCTE+webRowSelect+` where id = @tenant`, args))
	if errors.Is(err, pgx.ErrNoRows) {
		// the operator views do not list this project
		return nil
	}
	if err != nil {
		return fmt.Errorf("web row: %w", err)
	}
	t, sc, grade, cmp := p.Tenant, p.SiteCounts, p.Integrity, p.Comparison
	out.HasWebData, out.HasAPIData = p.HasWebData, p.HasAPIData
	web := SCFWebRow{
		Pageviews: p.Pageviews, Visitors: p.Visitors, Visits: p.Visits, Bounces: p.Bounces, Totaltime: p.Totaltime,
		BounceRate: p.BounceRate, AvgVisitDuration: p.AvgVisitDuration, QuarantinePageviews: p.QuarantinePageviews,
		QuarantineVisitors: p.QuarantineVisitors, QuarantineShare: p.QuarantineShare, IngestRequests: p.IngestRequests,
		IngestRejected: p.IngestRejected, RejectedShare: p.RejectedShare, Coverage: p.Coverage,
	}
	out.InOperatorViews, out.Tenant, out.Sites, out.Web, out.Integrity = true, &t, &sc, &web, &grade
	out.PercentileCohort, out.Comparison = &pc, &cmp
	if out.RulesMet, err = RulesMet(ctx, tx, id, utcDay(now)); err != nil {
		return err
	}
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
	var out SCFViewAnswer
	err = v.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		out, err = LoadSCFView(ctx, tx, a.TenantID, rg.From, rg.To, now)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
