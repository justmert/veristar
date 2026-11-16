// Package integrityview reads agg.web_integrity_daily into the integrity
// report that projects, operators, the machine api and the mcp tools share
// (control-plane-api section 17). the transaction's role decides what it
// sees: a project reads its own rows under row-level security, an operator
// reads every tenant's.
package integrityview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/integrity"
)

// Reason is one finding of a day, with its plain language text.
type Reason struct {
	Code  string  `json:"code"`
	Level string  `json:"level"`
	Value float64 `json:"value"`
	Limit float64 `json:"limit"`
	Text  string  `json:"text"`
}

// NetCounts are accepted events by network class.
type NetCounts struct {
	Access       int64 `json:"access"`
	Organisation int64 `json:"organisation"`
	Hosting      int64 `json:"hosting"`
	Anonymiser   int64 `json:"anonymiser"`
	Unknown      int64 `json:"unknown"`
}

// Evidence is what a day's grade was computed from.
type Evidence struct {
	Events           int64     `json:"events"`
	Quarantined      int64     `json:"quarantined"`
	ProofFailures    int64     `json:"proof_failures"`
	Networks         int64     `json:"networks"`
	Visitors         int64     `json:"visitors"`
	Pageviews        int64     `json:"pageviews"`
	BaselineVisitors *int64    `json:"baseline_visitors"`
	BaselineDays     int       `json:"baseline_days"`
	Net              NetCounts `json:"net"`
	HostingShare     *float64  `json:"hosting_share"`
	QuarantineShare  *float64  `json:"quarantine_share"`
	ProofPassRate    *float64  `json:"proof_pass_rate"`
}

// Day is one graded or ungraded site day.
type Day struct {
	Day         string   `json:"day"`
	Grade       *string  `json:"grade"`
	Closed      bool     `json:"closed"`
	RuleVersion int      `json:"rule_version"`
	Reasons     []Reason `json:"reasons"`
	Evidence    Evidence `json:"evidence"`
}

// RangeReason is one reason code over a range.
type RangeReason struct {
	Code  string `json:"code"`
	Level string `json:"level"`
	Days  int    `json:"days"`
	Text  string `json:"text"`
}

// Summary is the grade of a range (integrity.Combine).
type Summary struct {
	Grade      *string       `json:"grade"`
	GradedDays int           `json:"graded_days"`
	DaysA      int           `json:"days_a"`
	DaysB      int           `json:"days_b"`
	DaysC      int           `json:"days_c"`
	Reasons    []RangeReason `json:"reasons"`
}

// Site is one site's range and days.
type Site struct {
	SiteID uuid.UUID `json:"site_id"`
	Summary
	Days []Day `json:"days"`
}

// Report is a project's integrity over a range of utc days.
type Report struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	RuleVersion int       `json:"rule_version"`
	Summary
	Sites []Site `json:"sites"`
}

// SummaryOf turns a combined range into its api form.
func SummaryOf(r integrity.RangeGrade) Summary {
	s := Summary{GradedDays: r.GradedDays, DaysA: r.DaysA, DaysB: r.DaysB, DaysC: r.DaysC, Reasons: []RangeReason{}}
	if r.Grade != "" {
		g := r.Grade
		s.Grade = &g
	}
	for _, x := range r.Reasons {
		s.Reasons = append(s.Reasons, RangeReason{Code: x.Code, Level: x.Level, Days: x.Days, Text: integrity.RangeText(x.Code)})
	}
	return s
}

// Load reads the reports of the named tenants over [from, to]. every tenant
// gets a report, empty when it has no graded day. sites are ordered by id,
// days by date.
func Load(ctx context.Context, tx pgx.Tx, tenants []uuid.UUID, from, to time.Time) (map[uuid.UUID]*Report, error) {
	out := make(map[uuid.UUID]*Report, len(tenants))
	for _, id := range tenants {
		out[id] = &Report{TenantID: id, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly),
			RuleVersion: integrity.RuleVersion, Summary: SummaryOf(integrity.Combine(nil)), Sites: []Site{}}
	}
	if len(tenants) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select tenant_id, site_id, day, rule_version, grade, closed, events, quarantined, proof_failures,
		       net_access, net_organisation, net_hosting, net_anonymiser, net_unknown, networks,
		       visitors, pageviews, baseline_visitors, baseline_days, reasons
		from agg.web_integrity_daily
		where tenant_id = any(@ids::uuid[]) and day between @from_day::date and @to_day::date
		order by tenant_id, site_id, day`,
		pgx.NamedArgs{"ids": tenants, "from_day": from, "to_day": to})
	if err != nil {
		return nil, fmt.Errorf("integrity: %w", err)
	}
	defer rows.Close()
	type acc struct {
		site Site
		days []integrity.DayGrade
	}
	sites := map[uuid.UUID][]*acc{}
	for rows.Next() {
		var tenant, site uuid.UUID
		var day time.Time
		var rv, baseDays int32
		var grade *string
		var closed bool
		var in integrity.Inputs
		var raw []byte
		if err := rows.Scan(&tenant, &site, &day, &rv, &grade, &closed, &in.Events, &in.Quarantined, &in.ProofFailures,
			&in.NetAccess, &in.NetOrganisation, &in.NetHosting, &in.NetAnonymiser, &in.NetUnknown, &in.Networks,
			&in.Visitors, &in.Pageviews, &in.BaselineVisitors, &baseDays, &raw); err != nil {
			return nil, fmt.Errorf("integrity: %w", err)
		}
		var rs []integrity.Reason
		if err := json.Unmarshal(raw, &rs); err != nil {
			return nil, fmt.Errorf("integrity: reasons of site %s day %s: %w", site, day.Format(time.DateOnly), err)
		}
		list := sites[tenant]
		if len(list) == 0 || list[len(list)-1].site.SiteID != site {
			list = append(list, &acc{site: Site{SiteID: site, Days: []Day{}}})
			sites[tenant] = list
		}
		a := list[len(list)-1]
		d := Day{Day: day.Format(time.DateOnly), Grade: grade, Closed: closed, RuleVersion: int(rv), Reasons: []Reason{},
			Evidence: Evidence{
				Events: in.Events, Quarantined: in.Quarantined, ProofFailures: in.ProofFailures, Networks: in.Networks,
				Visitors: in.Visitors, Pageviews: in.Pageviews, BaselineVisitors: in.BaselineVisitors, BaselineDays: int(baseDays),
				Net: NetCounts{Access: in.NetAccess, Organisation: in.NetOrganisation, Hosting: in.NetHosting,
					Anonymiser: in.NetAnonymiser, Unknown: in.NetUnknown},
				HostingShare: in.HostingShare(), QuarantineShare: in.QuarantineShare(), ProofPassRate: in.ProofPassRate(),
			}}
		for _, r := range rs {
			d.Reasons = append(d.Reasons, Reason{Code: r.Code, Level: r.Level, Value: r.Value, Limit: r.Limit, Text: r.Text()})
		}
		a.site.Days = append(a.site.Days, d)
		dg := integrity.DayGrade{Reasons: rs}
		if grade != nil {
			dg.Grade = *grade
		}
		a.days = append(a.days, dg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: %w", err)
	}
	for tenant, list := range sites {
		var all []integrity.DayGrade
		rep := out[tenant]
		for _, a := range list {
			a.site.Summary = SummaryOf(integrity.Combine(a.days))
			rep.Sites = append(rep.Sites, a.site)
			all = append(all, a.days...)
		}
		rep.Summary = SummaryOf(integrity.Combine(all))
	}
	return out, nil
}

// DayKey is a tenant and utc day.
type DayKey struct {
	Tenant uuid.UUID
	Day    string
}

// ByTenantDay sums a tenant's sites per day, for the per-day items of the
// machine api: the grade of the day over all of the tenant's sites.
func ByTenantDay(ctx context.Context, tx pgx.Tx, tenants []uuid.UUID, from, to time.Time) (map[DayKey]Summary, error) {
	reps, err := Load(ctx, tx, tenants, from, to)
	if err != nil {
		return nil, err
	}
	out := map[DayKey]Summary{}
	for tenant, rep := range reps {
		days := map[string][]integrity.DayGrade{}
		for _, s := range rep.Sites {
			for _, d := range s.Days {
				dg := integrity.DayGrade{}
				if d.Grade != nil {
					dg.Grade = *d.Grade
				}
				for _, r := range d.Reasons {
					dg.Reasons = append(dg.Reasons, integrity.Reason{Code: r.Code, Level: r.Level, Value: r.Value, Limit: r.Limit})
				}
				days[d.Day] = append(days[d.Day], dg)
			}
		}
		for day, dgs := range days {
			out[DayKey{tenant, day}] = SummaryOf(integrity.Combine(dgs))
		}
	}
	return out, nil
}

// range limits of the project route, the same as the operator views'
// (contract 8.1)
const (
	DefaultRangeDays = 30
	MaxRangeDays     = 366
)

// ParseRange reads from and to as utc days, both included: without to the
// range ends today, without from it holds DefaultRangeDays days. to after
// today, to before from and more than MaxRangeDays days are 400
// invalid_query.
func ParseRange(q url.Values, now time.Time) (from, to time.Time, err error) {
	t := now.UTC()
	today := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	day := func(key string) (time.Time, bool, error) {
		vs := q[key]
		switch {
		case len(vs) == 0:
			return time.Time{}, false, nil
		case len(vs) > 1:
			return time.Time{}, false, httpx.BadQuery(key + " is given more than once")
		}
		d, perr := time.Parse(time.DateOnly, vs[0])
		if perr != nil {
			return time.Time{}, false, httpx.BadQuery(key + " must be a date yyyy-mm-dd")
		}
		return d, true, nil
	}
	from, hasFrom, err := day("from")
	if err != nil {
		return from, to, err
	}
	to, hasTo, err := day("to")
	if err != nil {
		return from, to, err
	}
	if !hasTo {
		to = today
		if hasFrom && from.After(today) {
			to = from
		}
	}
	if !hasFrom {
		from = to.AddDate(0, 0, -(DefaultRangeDays - 1))
	}
	switch {
	case to.After(today):
		return from, to, httpx.BadQuery("to must not be after today (utc)")
	case to.Before(from):
		return from, to, httpx.BadQuery("to must not be before from")
	case int(to.Sub(from).Hours()/24)+1 > MaxRangeDays:
		return from, to, httpx.BadQuery(fmt.Sprintf("the range may hold at most %d days", MaxRangeDays))
	}
	return from, to, nil
}
