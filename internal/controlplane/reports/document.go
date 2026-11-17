// Package reports serves signed usage reports (control-plane-api section
// 20, docs/reference/usage-reports.md). a member of a project generates a
// report for a period of closed utc days; the control plane reads the same
// aggregates the operator views read, writes the report in its canonical
// json form (internal/canonjson), signs those bytes with the platform's
// report key the sep-53 way (internal/chain) and stores both. a report is
// immutable. its unguessable link shows it to anyone, with the signature
// checked in the browser, until a member revokes the link.
package reports

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/routenorm"
)

// the report format and the method versions it records. each version
// names a definition in the documentation; a change of definition raises
// its version, so a reader knows which rules produced a report's numbers.
const (
	// Format is the value of the report's format member.
	Format = "veristar-usage-report"
	// FormatVersion is the version of the document layout below. version 2
	// lists the integrity rule versions that graded the period, and adds
	// the coverage estimate and has_data to web, lossy, infrastructure
	// requests and has_data to api, and weekly active accounts to onchain.
	// a version 1 report keeps its layout and still verifies: the
	// signature covers its bytes whatever members they hold.
	FormatVersion = 2
	// WebMetricsVersion: docs/reference/web-metrics.md, primary band,
	// daily-unique visitors summed over days and sites.
	WebMetricsVersion = 1
	// APIMetricsVersion: control-plane-api section 8, per-service metric
	// rows over span rows, percentiles from summed histogram buckets.
	// version 2 leaves infrastructure requests out of every number
	// (traffic class rule set 1, section 7.4).
	APIMetricsVersion = 2
	// OnchainVersion: control-plane-api section 15, horizon operations of
	// accounts and rpc events of contracts.
	OnchainVersion = 1
	// CohortVersion: control-plane-api section 19, change against the
	// previous period of equal length and cume_dist percentiles.
	CohortVersion = 1
	// SignatureScheme names sep-53 over the canonical json bytes.
	SignatureScheme = "sep-53"
)

// Issuer says who signed the report and where its key is published.
type Issuer struct {
	Name        string `json:"name"`
	Console     string `json:"console"`
	StellarTOML string `json:"stellar_toml"`
	SigningKey  string `json:"signing_key"`
}

// Project names the reported project.
type Project struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Period is the reported range of utc days, both included.
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

// Methods are the versions of every definition behind the numbers.
type Methods struct {
	Format     int `json:"format"`
	WebMetrics int `json:"web_metrics"`
	APIMetrics int `json:"api_metrics"`
	Onchain    int `json:"onchain"`
	Cohort     int `json:"cohort"`
	// IntegrityRules are the rule versions that graded the period's site
	// days, ascending: a grade is never re-derived, so a period graded
	// across a rule change names both versions. empty without a graded day
	IntegrityRules  []int  `json:"integrity_rules"`
	RouteNormaliser int    `json:"route_normaliser"`
	Platform        string `json:"platform"`
}

// Coverage estimates the visits not measured because the browser sends
// global privacy control: tracker file requests, the ones that carried
// the signal, and their share as a decimal string.
type Coverage struct {
	ScriptRequests  int64   `json:"script_requests"`
	GPCRequests     int64   `json:"gpc_requests"`
	UnmeasuredShare *string `json:"unmeasured_share"`
}

// WeeklyActive is the weekly active accounts over the iso weeks that lie
// whole inside the period: the mean of the counted weeks as a decimal
// string, the last counted week, and how many weeks were counted and not.
type WeeklyActive struct {
	Average      *string `json:"average"`
	Latest       *int64  `json:"latest"`
	WeeksCounted int64   `json:"weeks_counted"`
	WeeksMissing int64   `json:"weeks_missing"`
}

// WebDay is one day of the web series. Aggregated is false for a day the
// aggregation never read; its numbers are then null.
type WebDay struct {
	Day        string `json:"day"`
	Aggregated bool   `json:"aggregated"`
	Pageviews  *int64 `json:"pageviews"`
	Visitors   *int64 `json:"visitors"`
	Visits     *int64 `json:"visits"`
}

// Web is the project's web numbers over the period. HasData is false
// when no page view was counted, so zeros mean no data rather than no use.
type Web struct {
	HasData                 bool     `json:"has_data"`
	Pageviews               int64    `json:"pageviews"`
	Visitors                int64    `json:"visitors"`
	Visits                  int64    `json:"visits"`
	Bounces                 int64    `json:"bounces"`
	TotaltimeSeconds        int64    `json:"totaltime_seconds"`
	BounceRate              *string  `json:"bounce_rate"`
	AvgVisitDurationSeconds *string  `json:"avg_visit_duration_seconds"`
	QuarantinePageviews     int64    `json:"quarantine_pageviews"`
	QuarantineVisitors      int64    `json:"quarantine_visitors"`
	QuarantineShare         *string  `json:"quarantine_share"`
	GatewayRequests         int64    `json:"gateway_requests"`
	GatewayRejected         int64    `json:"gateway_rejected"`
	Sites                   int64    `json:"sites"`
	SitesVerified           int64    `json:"sites_verified"`
	Coverage                Coverage `json:"coverage"`
	Series                  []WebDay `json:"series"`
}

// APIDay is one day of the api series.
type APIDay struct {
	Day         string `json:"day"`
	Requests    int64  `json:"requests"`
	Requests4xx int64  `json:"requests_4xx"`
	Requests5xx int64  `json:"requests_5xx"`
}

// Latency is one percentile: seconds as a decimal string, or null with
// OverTenSeconds set when it lies in the open bucket.
type Latency struct {
	Seconds        *string `json:"seconds"`
	OverTenSeconds bool    `json:"over_10s"`
}

// API is the project's api numbers over the period. Lossy is set when a
// metric row of the period overflowed its route limit, and
// InfrastructureRequests counts the requests every number leaves out.
type API struct {
	HasData                bool     `json:"has_data"`
	Requests               int64    `json:"requests"`
	Requests4xx            int64    `json:"requests_4xx"`
	Requests5xx            int64    `json:"requests_5xx"`
	Rate4xx                *string  `json:"rate_4xx"`
	Rate5xx                *string  `json:"rate_5xx"`
	P50                    Latency  `json:"p50"`
	P95                    Latency  `json:"p95"`
	P99                    Latency  `json:"p99"`
	Services               int64    `json:"services"`
	Routes                 int64    `json:"routes"`
	Source                 *string  `json:"source"`
	Lossy                  bool     `json:"lossy"`
	InfrastructureRequests int64    `json:"infrastructure_requests"`
	Series                 []APIDay `json:"series"`
}

// OnchainDay is one day of the on-chain series; the numbers are null when
// no declared id was read that day.
type OnchainDay struct {
	Day          string `json:"day"`
	IDsRead      int64  `json:"ids_read"`
	Operations   *int64 `json:"operations"`
	Transactions *int64 `json:"transactions"`
}

// Onchain is the project's ledger activity over the period: the numbers
// and the series count verified ids only.
type Onchain struct {
	IDs          int64        `json:"ids"`
	IDsVerified  int64        `json:"ids_verified"`
	Operations   int64        `json:"operations"`
	Transactions int64        `json:"transactions"`
	DaysMissing  int64        `json:"days_missing"`
	TwoWayLink   bool         `json:"two_way_link"`
	WeeklyActive WeeklyActive `json:"weekly_active_accounts"`
	Series       []OnchainDay `json:"series"`
}

// Reason is one integrity reason over the period.
type Reason struct {
	Code  string `json:"code"`
	Level string `json:"level"`
	Days  int64  `json:"days"`
	Text  string `json:"text"`
}

// Integrity is the grade over the period and its reasons.
type Integrity struct {
	Grade      *string  `json:"grade"`
	GradedDays int64    `json:"graded_days"`
	DaysA      int64    `json:"days_a"`
	DaysB      int64    `json:"days_b"`
	DaysC      int64    `json:"days_c"`
	Reasons    []Reason `json:"reasons"`
}

// KPI is one key metric with its change and percentile, as decimal
// strings; null where the view has none.
type KPI struct {
	Value      *string `json:"value"`
	Previous   *string `json:"previous"`
	Change     *string `json:"change"`
	Percentile *string `json:"percentile"`
}

// Cohort is the project's confirmed cohort and its place in it.
type Cohort struct {
	Program   *string `json:"program"`
	Round     *string `json:"round"`
	Category  *string `json:"category"`
	AwardDate *string `json:"award_date"`
	// the population of the percentiles: projects confirmed in this
	// program and round, or every listed project when both are null
	PercentileProgram *string        `json:"percentile_program"`
	PercentileRound   *string        `json:"percentile_round"`
	CohortSize        int64          `json:"cohort_size"`
	KPIs              map[string]KPI `json:"kpis"`
}

// RuleMet is a saved operator rule the project meets on the generation day.
type RuleMet struct {
	Name      string  `json:"name"`
	Metric    string  `json:"metric"`
	MinPerDay int64   `json:"min_per_day"`
	MinDays   int64   `json:"min_days"`
	Months    int64   `json:"months"`
	MinGrade  *string `json:"min_grade"`
	From      string  `json:"from"`
	To        string  `json:"to"`
}

// Report is the signed document.
type Report struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	ID          string    `json:"id"`
	Issuer      Issuer    `json:"issuer"`
	Project     Project   `json:"project"`
	Period      Period    `json:"period"`
	GeneratedAt string    `json:"generated_at"`
	Methods     Methods   `json:"methods"`
	Web         Web       `json:"web"`
	API         API       `json:"api"`
	Onchain     Onchain   `json:"onchain"`
	Integrity   Integrity `json:"integrity"`
	Cohort      Cohort    `json:"cohort"`
	RulesMet    []RuleMet `json:"rules_met"`
}

// decimal writes a ratio or a number of seconds with six decimals, so the
// canonical form holds no floating point number.
func decimal(f *float64) *string {
	if f == nil {
		return nil
	}
	s := strconv.FormatFloat(*f, 'f', 6, 64)
	return &s
}

func latency(s *float64, over bool) Latency {
	return Latency{Seconds: decimal(s), OverTenSeconds: over}
}

func kpi(k operator.KPI) KPI {
	return KPI{Value: decimal(k.Value), Previous: decimal(k.Previous), Change: decimal(k.Change), Percentile: decimal(k.Percentile)}
}

// Build reads a project's numbers over [from, to] inside a cp_operator
// transaction whose snapshot the caller set, and returns the report.
func Build(ctx context.Context, tx pgx.Tx, id string, tenant uuid.UUID, from, to, now time.Time, issuer Issuer, platform string) (Report, error) {
	v, err := operator.LoadSCFView(ctx, tx, tenant, from, to, now)
	if err != nil {
		return Report{}, err
	}
	if !v.InOperatorViews {
		return Report{}, ErrNotListed
	}
	reps, err := integrityview.Load(ctx, tx, []uuid.UUID{tenant}, from, to)
	if err != nil {
		return Report{}, err
	}
	ig := reps[tenant].Summary
	r := Report{
		Format: Format, Version: FormatVersion, ID: id, Issuer: issuer,
		Project:     Project{ID: v.Tenant.ID.String(), Slug: v.Tenant.Slug, Name: v.Tenant.Name},
		Period:      Period{From: v.From, To: v.To, Days: len(v.Days)},
		GeneratedAt: now.UTC().Truncate(time.Second).Format(time.RFC3339),
		Methods: Methods{
			Format: FormatVersion, WebMetrics: WebMetricsVersion, APIMetrics: APIMetricsVersion, Onchain: OnchainVersion,
			Cohort: CohortVersion, IntegrityRules: reps[tenant].RuleVersions, RouteNormaliser: routenorm.Version, Platform: platform,
		},
		Integrity: Integrity{Grade: ig.Grade, GradedDays: int64(ig.GradedDays), DaysA: int64(ig.DaysA), DaysB: int64(ig.DaysB),
			DaysC: int64(ig.DaysC), Reasons: []Reason{}},
		RulesMet: []RuleMet{},
	}
	for _, x := range ig.Reasons {
		r.Integrity.Reasons = append(r.Integrity.Reasons, Reason{Code: x.Code, Level: x.Level, Days: int64(x.Days), Text: x.Text})
	}
	w := v.Web
	r.Web = Web{
		HasData: v.HasWebData, Pageviews: w.Pageviews, Visitors: w.Visitors, Visits: w.Visits, Bounces: w.Bounces, TotaltimeSeconds: w.Totaltime,
		BounceRate: decimal(w.BounceRate), AvgVisitDurationSeconds: decimal(w.AvgVisitDuration),
		QuarantinePageviews: w.QuarantinePageviews, QuarantineVisitors: w.QuarantineVisitors,
		QuarantineShare: decimal(w.QuarantineShare), GatewayRequests: w.IngestRequests, GatewayRejected: w.IngestRejected,
		Sites: v.Sites.Sites, SitesVerified: v.Sites.SitesVerified, Series: []WebDay{},
		Coverage: Coverage{ScriptRequests: w.Coverage.ScriptRequests, GPCRequests: w.Coverage.GPCRequests,
			UnmeasuredShare: decimal(w.Coverage.UnmeasuredShare)},
	}
	for _, p := range v.WebSeries {
		r.Web.Series = append(r.Web.Series, WebDay{Day: p.Day, Aggregated: p.Aggregated, Pageviews: p.Pageviews,
			Visitors: p.Visitors, Visits: p.Visits})
	}
	a := v.API
	r.API = API{
		HasData: v.HasAPIData, Lossy: a.Lossy, InfrastructureRequests: a.InfrastructureRequests, Requests: a.Requests, Requests4xx: a.Requests4xx, Requests5xx: a.Requests5xx,
		Rate4xx: decimal(a.Rate4xx), Rate5xx: decimal(a.Rate5xx),
		P50: latency(a.P50Seconds, a.P50Over10s), P95: latency(a.P95Seconds, a.P95Over10s), P99: latency(a.P99Seconds, a.P99Over10s),
		Services: a.Services, Routes: a.Routes, Source: a.Source, Series: []APIDay{},
	}
	for _, p := range v.APISeries {
		r.API.Series = append(r.API.Series, APIDay{Day: p.Day, Requests: p.Requests, Requests4xx: p.Requests4xx, Requests5xx: p.Requests5xx})
	}
	o := v.Onchain
	r.Onchain = Onchain{IDs: o.IDs, IDsVerified: o.IDsVerified, Operations: o.Operations, Transactions: o.Transactions,
		DaysMissing: o.DaysMissing, TwoWayLink: v.LinkTwoWay, Series: []OnchainDay{},
		WeeklyActive: WeeklyActive{Average: decimal(o.WeeklyActive.Average), Latest: o.WeeklyActive.Latest,
			WeeksCounted: int64(o.WeeklyActive.WeeksCounted), WeeksMissing: int64(o.WeeklyActive.WeeksMissing)}}
	for _, p := range v.OnchainSeries {
		r.Onchain.Series = append(r.Onchain.Series, OnchainDay{Day: p.Day, IDsRead: p.IDsRead, Operations: p.Operations, Transactions: p.Transactions})
	}
	c := v.Comparison
	r.Cohort = Cohort{
		Program: c.Cohort.Program, Round: c.Cohort.Round, Category: c.Cohort.Category, AwardDate: c.Cohort.AwardDate,
		PercentileProgram: v.PercentileCohort.Program, PercentileRound: v.PercentileCohort.Round, CohortSize: c.CohortSize,
		KPIs: map[string]KPI{
			"visitors": kpi(c.KPIs.Visitors), "pageviews": kpi(c.KPIs.Pageviews), "requests": kpi(c.KPIs.Requests),
			"error_rate": kpi(c.KPIs.ErrorRate), "onchain_operations": kpi(c.KPIs.OnchainOperations),
		},
	}
	for _, m := range v.RulesMet {
		r.RulesMet = append(r.RulesMet, RuleMet{
			Name: m.Rule.Name, Metric: m.Rule.Metric, MinPerDay: m.Rule.MinPerDay, MinDays: int64(m.Rule.MinDays),
			Months: int64(m.Rule.Months), MinGrade: m.Rule.MinGrade, From: m.Match.Months[0].Month,
			To: m.Match.Months[len(m.Match.Months)-1].Month,
		})
	}
	return r, nil
}

// ErrNotListed is a project the operator views do not list (an internal
// project): it has no row to report.
var ErrNotListed = fmt.Errorf("reports: the project is not listed in the operator views")
