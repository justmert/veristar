package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
)

// cell makes a value safe inside a markdown table cell. keys come from
// tenant traffic (referrer hosts, routes, service names), so a pipe or a
// line break must not be able to change the table.
func cell(s string) string {
	if s == "" {
		return "(empty)"
	}
	r := strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ", "`", "'", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func pct(num, den int64) string {
	if den == 0 {
		return "none (no visits)"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(num)/float64(den))
}

func duration(total, visits int64) string {
	if visits == 0 {
		return "none (no visits)"
	}
	return fmt.Sprintf("%.0f s", float64(total)/float64(visits))
}

// percentile follows the contract: null with the over-10s flag is the open
// bucket, reported as ">10 s" and never as a number
func percentile(v *float64, over bool) string {
	switch {
	case over:
		return ">10 s"
	case v == nil:
		return "none"
	case *v < 1:
		return fmt.Sprintf("%.1f ms", *v*1000)
	default:
		return fmt.Sprintf("%.3f s", *v)
	}
}

func runText(r dayRun) string {
	if r.Days == 0 {
		return "0 days"
	}
	if r.Days == 1 {
		return "1 day (" + r.Start.Format(dayLayout) + ")"
	}
	return fmt.Sprintf("%d days (%s to %s)", r.Days, r.Start.Format(dayLayout), r.End.Format(dayLayout))
}

func render(w io.Writer, ev evidence) error {
	b := bufio.NewWriter(w)
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(b, format, a...) }

	p("# Adoption evidence, %s\n\n", ev.GeneratedAt.Format(dayLayout))
	p("Written by `cmd/adoptionreport` from the machine metrics API. No number in this file was typed\n")
	p("by hand; each one traces to a request listed under [Sources](#sources). Do not edit the\n")
	p("numbers. To refresh them, run the command again ([README.md](README.md), section 6).\n\n")
	p("| | |\n|---|---|\n")
	p("| Generated at | %s |\n", ev.GeneratedAt.Format(time.RFC3339))
	p("| Machine API | `%s` |\n", ev.API)
	p("| Range | %s to %s (%d UTC days) |\n", ev.From.Format(dayLayout), ev.To.Format(dayLayout), ev.rangeDays())
	if !ev.To.Before(ev.Today) {
		p("| Note | the range ends today, which is not complete; its numbers will still grow |\n")
	}
	p("| Threshold | %d consecutive UTC days with accepted page views (the design, beyond the RFP) |\n\n", ev.MinDays)

	p("## Summary\n\n")
	p("| Project | Tenant id | Status | Enrolled | First accepted page view in range | Longest run | Meets %d days | Days with API requests |\n", ev.MinDays)
	p("|---|---|---|---|---|---|---|---|\n")
	for _, t := range ev.Tenants {
		first := "none"
		if t.FirstDay != nil {
			first = t.FirstDay.Format(dayLayout)
		}
		meets := "no"
		if t.passes(ev.MinDays) {
			meets = "yes"
		}
		enrolled := t.Tenant.CreatedAt
		if ts, err := time.Parse(time.RFC3339, t.Tenant.CreatedAt); err == nil {
			enrolled = ts.UTC().Format(dayLayout)
		}
		p("| %s | `%s` | %s | %s | %s | %s | %s | %d of %d |\n", cell(t.Tenant.Name), t.Tenant.ID, cell(t.Tenant.Status),
			enrolled, first, runText(t.Longest), meets, len(t.APIDays), ev.rangeDays())
	}
	p("\n")

	p("## How to read this file\n\n")
	p("- **Accepted page view.** The gateway accepts an event only after its challenge proof\n")
	p("  verified, and the replayer stores it in the site's primary or quarantine band\n")
	p("  (`docs/reference/browser-proof.md`). A day counts towards a run when the primary band has\n")
	p("  at least one page view. Quarantined page views are shown beside it and never counted.\n")
	p("- **Days not aggregated.** The API leaves out a day the aggregation task never read. Such a\n")
	p("  day breaks a run, the same as a day without page views, and is marked in the daily table.\n")
	p("- **Visitors** are unique per UTC day and site, so visitors over the range are the sum of the\n")
	p("  daily numbers, and breakdown values are visitor-days (`docs/reference/web-metrics.md`).\n")
	p("- **Session duration** is the average visit duration, total time over visits; **bounce rate**\n")
	p("  is bounces over visits. Both are over the days with accepted page views in the range.\n")
	p("- **API percentiles** are per day. The API never combines percentiles across days, so the\n")
	p("  range has request totals only. Routes are shown for the newest day with requests.\n\n")

	for _, t := range ev.Tenants {
		renderTenant(p, ev, t)
	}

	p("## Sources\n\n")
	p("Every answer this file was computed from, in request order. The request id is the one the\n")
	p("control plane logged; the hash is the SHA-256 of the response body.\n\n")
	p("| # | Request | Status | Request id | SHA-256 of the body |\n|---|---|---|---|---|\n")
	for i, s := range ev.Sources {
		p("| %d | `%s` | %d | `%s` | `%s` |\n", i+1, strings.ReplaceAll(s.Path, "|", "%7C"), s.Status, cell(s.RequestID), s.SHA256)
	}
	return b.Flush()
}

func renderTenant(p func(string, ...any), ev evidence, t tenantEvidence) {
	p("## %s\n\n", cell(t.Tenant.Name))
	p("Tenant `%s`, slug `%s`, status %s. Sites: %d, verified: %d, with a stellar.toml: %d.\n\n",
		t.Tenant.ID, cell(t.Tenant.Slug), cell(t.Tenant.Status), t.Tenant.Sites, t.Tenant.SitesVerified, t.Tenant.SitesWithStellarToml)

	p("### Web: days with accepted page views\n\n")
	p("| | |\n|---|---|\n")
	p("| Days with accepted page views | %d of %d |\n", t.DaysAccepted, ev.rangeDays())
	p("| Longest run | %s |\n", runText(t.Longest))
	p("| Run ending on the last day of the range | %s |\n", runText(t.Current))
	p("| Days the aggregation never read | %d |\n", t.DaysMissing)
	verdict := fmt.Sprintf("not met: the longest run is %d of %d days", t.Longest.Days, ev.MinDays)
	if t.passes(ev.MinDays) {
		verdict = "met"
	}
	p("| %d-day threshold | %s |\n\n", ev.MinDays, verdict)

	p("### Web: the seven metrics over the range (primary band)\n\n")
	p("| RFP metric | Value |\n|---|---|\n")
	p("| Page views | %d |\n", t.Primary.Pageviews)
	p("| Unique visitors (sum of daily unique visitors) | %d |\n", t.Primary.Visitors)
	p("| Session duration (average visit) | %s over %d visits |\n", duration(t.Primary.Totaltime, t.Primary.Visits), t.Primary.Visits)
	p("| Bounce rate | %s |\n", pct(t.Primary.Bounces, t.Primary.Visits))
	for _, dim := range dimensions {
		p("| %s | %d distinct, top below |\n", dim.Title, t.BreakdownKeys[dim.Key])
	}
	p("\nQuarantined page views in the range, not counted above: %d.\n\n", t.QuarantineViews)
	for _, dim := range dimensions {
		kv := t.Breakdowns[dim.Key]
		p("**%s** (visitor-days, top %d)\n\n", dim.Title, topN)
		if len(kv) == 0 {
			p("None in the range.\n\n")
			continue
		}
		p("| Key | Visitor-days |\n|---|---|\n")
		for _, x := range kv {
			p("| %s | %d |\n", cell(x.Key), x.Value)
		}
		p("\n")
	}

	p("### Web: per day\n\n")
	p("| Day | Page views | Visitors | Visits | Bounce rate | Session duration | Quarantined page views | Counts |\n")
	p("|---|---|---|---|---|---|---|---|\n")
	for _, d := range t.Days {
		if !d.Present {
			p("| %s | not aggregated | | | | | | no |\n", d.Day.Format(dayLayout))
			continue
		}
		counts := "no"
		if d.accepted() {
			counts = "yes"
		}
		p("| %s | %d | %d | %d | %s | %s | %d | %s |\n", d.Day.Format(dayLayout), d.Primary.Pageviews, d.Primary.Visitors,
			d.Primary.Visits, pct(d.Primary.Bounces, d.Primary.Visits), duration(d.Primary.Totaltime, d.Primary.Visits),
			d.Quarantine.Pageviews, counts)
	}
	p("\n")

	p("### API: volume, status codes and latency\n\n")
	if len(t.APIDays) == 0 {
		p("No API requests in the range.\n\n")
		return
	}
	other := t.APITotal.Requests - t.APITotal.Requests4xx - t.APITotal.Requests5xx
	p("| | |\n|---|---|\n")
	p("| Days with requests | %d of %d |\n", len(t.APIDays), ev.rangeDays())
	p("| Longest run of days with requests | %s |\n", runText(t.APIRun))
	p("| Requests | %d |\n", t.APITotal.Requests)
	p("| Status below 400 | %d |\n", other)
	p("| Status 4xx | %d (%s) |\n", t.APITotal.Requests4xx, pct(t.APITotal.Requests4xx, t.APITotal.Requests))
	p("| Status 5xx | %d (%s) |\n", t.APITotal.Requests5xx, pct(t.APITotal.Requests5xx, t.APITotal.Requests))
	if t.APITotal.Lossy {
		p("| Note | some days were re-bucketed inexactly, so their percentiles are approximate |\n")
	}
	p("\n| Day | Requests | Below 400 | 4xx | 5xx | p50 | p95 | Source | Services |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, d := range t.APIDays {
		src := d.Source
		if d.Lossy {
			src += ", approximate"
		}
		p("| %s | %d | %d | %d | %d | %s | %s | %s | %d |\n", d.Day, d.Requests, d.Requests-d.Requests4xx-d.Requests5xx,
			d.Requests4xx, d.Requests5xx, percentile(d.P50Seconds, d.P50Over10s), percentile(d.P95Seconds, d.P95Over10s), src, d.Services)
	}
	p("\n**Routes on %s**, by requests\n\n", t.RoutesDay)
	p("| Service | Method | Route | Requests | 4xx | 5xx | p50 | p95 | Source |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range t.Routes {
		p("| %s | %s | %s | %d | %d | %d | %s | %s | %s |\n", cell(r.Service), cell(r.Method), cell(r.Route), r.Requests,
			r.Requests4xx, r.Requests5xx, percentile(r.P50Seconds, r.P50Over10s), percentile(r.P95Seconds, r.P95Over10s), r.Source)
	}
	if t.RoutesTruncate > 0 {
		p("\n%d more routes with fewer requests are not listed.\n", t.RoutesTruncate)
	}
	p("\n")
}
