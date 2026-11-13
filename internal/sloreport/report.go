package sloreport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// objectives of the design. freshness has none: it is reported next to
// the two objectives because it counts against the pipeline, not ingestion.
var objectives = map[string]float64{
	KeyIngestion: 99.99,
	KeyDashboard: 99.90,
}

var titles = map[string]string{
	KeyIngestion:    "Ingestion",
	KeyDashboard:    "Dashboard",
	KeyFreshnessWeb: "Web pipeline freshness",
	KeyFreshnessAPI: "API pipeline freshness",
}

// Incident is a maximal run of failed results of one endpoint.
type Incident struct {
	Key   string
	Start time.Time
	// End is the first success after the run; zero while ongoing.
	End        time.Time
	Failed     int
	FirstError string
}

// Ongoing reports whether the run lasted until the end of the data.
func (i Incident) Ongoing() bool { return i.End.IsZero() }

// Stats are the numbers of one endpoint over the covered range.
type Stats struct {
	Key       string
	Results   int
	Successes int
	Incidents []Incident
}

// Failed is the number of failed intervals.
func (s Stats) Failed() int { return s.Results - s.Successes }

// Availability is successes over results in percent; ok is false without
// results, because an availability of nothing measured is not 100%.
func (s Stats) Availability() (float64, bool) {
	if s.Results == 0 {
		return 0, false
	}
	return 100 * float64(s.Successes) / float64(s.Results), true
}

// Month is the input of one report.
type Month struct {
	Month   time.Time
	Through time.Time // last day included
	Days    []time.Time
	Missing []time.Time
	// Partial maps a day collected before it ended to the end of its
	// results; such a day counts only up to there in the covered range.
	Partial map[string]time.Time
	// Gaps counts, per key, the results gatus wrote itself because no push
	// arrived within the heartbeat interval. they say the probe was not
	// running, not that the platform failed, so they are left out of the
	// measured results and reported apart.
	Gaps    map[string]int
	Stats   []Stats
	Sources []string
}

// PeriodSeconds is the length of the covered range, missing days included.
func (m Month) PeriodSeconds() float64 {
	total := 0.0
	for _, d := range m.Days {
		if u, ok := m.Partial[d.Format(dayLayout)]; ok {
			total += u.Sub(d).Seconds()
			continue
		}
		total += 86400
	}
	return total
}

// Load reads the daily files of month up to through (both utc days).
func Load(dir string, month, through time.Time) (Month, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	if through.IsZero() {
		through = last
	}
	if through.Before(first) || through.After(last) {
		return Month{}, fmt.Errorf("through %s is outside %s", through.Format(dayLayout), first.Format(monthLayout))
	}
	m := Month{Month: first, Through: through, Partial: map[string]time.Time{}, Gaps: map[string]int{}}
	series := map[string][]Result{}
	seen := map[string]bool{}
	for d := first; !d.After(through); d = d.AddDate(0, 0, 1) {
		m.Days = append(m.Days, d)
		day, err := ReadDay(dir, d)
		if errors.Is(err, fs.ErrNotExist) {
			m.Missing = append(m.Missing, d)
			continue
		}
		if err != nil {
			return Month{}, err
		}
		if day.Until != nil {
			m.Partial[d.Format(dayLayout)] = day.Until.UTC()
		}
		if !seen[day.Source] {
			seen[day.Source] = true
			m.Sources = append(m.Sources, day.Source)
		}
		for _, k := range Keys {
			rs, ok := day.Endpoints[k]
			if !ok {
				return Month{}, fmt.Errorf("%s: no results array for %s", DataPath(dir, d), k)
			}
			for _, r := range rs {
				if isHeartbeatGap(r) {
					m.Gaps[k]++
					continue
				}
				series[k] = append(series[k], r)
			}
		}
	}
	for _, k := range Keys {
		m.Stats = append(m.Stats, compute(k, series[k]))
	}
	return m, nil
}

// heartbeatPrefix starts the error gatus 5.37.0 records when an external
// endpoint got no push within its heartbeat interval
// (watchdog/external_endpoint.go)
const heartbeatPrefix = "heartbeat: no update received"

func isHeartbeatGap(r Result) bool {
	return !r.Success && len(r.Errors) == 1 && strings.HasPrefix(r.Errors[0], heartbeatPrefix)
}

// compute counts results and finds incidents. results are in time order
// (each daily file is sorted and days are read in order), so a run that
// spans midnight stays one incident.
func compute(key string, rs []Result) Stats {
	s := Stats{Key: key, Results: len(rs)}
	var cur *Incident
	for _, r := range rs {
		if r.Success {
			s.Successes++
			if cur != nil {
				cur.End = r.Timestamp
				s.Incidents = append(s.Incidents, *cur)
				cur = nil
			}
			continue
		}
		if cur == nil {
			cur = &Incident{Key: key, Start: r.Timestamp}
			if len(r.Errors) > 0 {
				cur.FirstError = r.Errors[0]
			}
		}
		cur.Failed++
	}
	if cur != nil {
		s.Incidents = append(s.Incidents, *cur)
	}
	return s
}

func pct(v float64) string { return fmt.Sprintf("%.4f%%", v) }

func minutes(v float64) string { return fmt.Sprintf("%.2f min", v) }

// cell keeps an error text from breaking the markdown table.
func cell(s string) string {
	if s == "" {
		return "(none recorded)"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "..."
	}
	return s
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	mi := int(d % time.Hour / time.Minute)
	sec := int(d % time.Minute / time.Second)
	if h > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", h, mi, sec)
	}
	return fmt.Sprintf("%dm %02ds", mi, sec)
}

// Render writes the markdown report.
func Render(m Month) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	last := m.Month.AddDate(0, 1, -1)
	w("# SLO report %s\n\n", m.Month.Format(monthLayout))
	w("Generated by `cmd/sloreport` from the daily probe results in `docs/slo/data/`.\n\n")
	w("- Covered range: %s to %s (UTC), %d days.", m.Days[0].Format(dayLayout), m.Through.Format(dayLayout), len(m.Days))
	if m.Through.Before(last) {
		w(" This is a partial month: it ends before %s.", last.Format(dayLayout))
	}
	w("\n")
	if len(m.Partial) > 0 {
		ds := make([]string, 0, len(m.Partial))
		for _, d := range m.Days {
			if u, ok := m.Partial[d.Format(dayLayout)]; ok {
				ds = append(ds, d.Format(dayLayout)+" until "+u.Format(time.RFC3339))
			}
		}
		w("- Partial days: %s. They were collected before they ended and count only up to that time.\n", strings.Join(ds, ", "))
	}
	w("- Probe interval: %d s. One result is one interval.\n", IntervalSeconds)
	gaps := make([]string, 0, len(Keys))
	for _, k := range Keys {
		if m.Gaps[k] > 0 {
			gaps = append(gaps, fmt.Sprintf("%s %d", k, m.Gaps[k]))
		}
	}
	if len(gaps) > 0 {
		w("- Probe gaps: %s. Gatus recorded these when no probe result arrived within its heartbeat interval; they are unmeasured, not failures.\n", strings.Join(gaps, ", "))
	}
	if len(m.Sources) > 0 {
		w("- Source: %s\n", strings.Join(m.Sources, ", "))
	}
	if len(m.Missing) == 0 {
		w("- Missing days: none.\n")
	} else {
		ds := make([]string, 0, len(m.Missing))
		for _, d := range m.Missing {
			ds = append(ds, d.Format(dayLayout))
		}
		w("- Missing days: %d (%s). No raw results were collected for them, so they count as unmeasured, not as available.\n",
			len(m.Missing), strings.Join(ds, ", "))
	}
	w("\nAvailability is successes over measured results. Coverage is measured time (results times the interval) over the covered range, so unmeasured intervals lower coverage and never raise availability.\n\n")

	w("## Objectives\n\n")
	w("| Measure | Results | Successes | Failed | Availability | Objective | Verdict | Coverage | Downtime budget | Budget used |\n")
	w("|---|---:|---:|---:|---:|---:|---|---:|---:|---:|\n")
	period := m.PeriodSeconds()
	for _, s := range m.Stats {
		avail := "no data"
		if a, ok := s.Availability(); ok {
			avail = pct(a)
		}
		coverage := pct(100 * float64(s.Results*IntervalSeconds) / period)
		obj, has := objectives[s.Key]
		objective, verdict, budget, used := "none", "reported only", "n/a", "n/a"
		if has {
			objective = pct(obj)
			// the budget is the downtime the objective allows over the
			// covered range; used is the failed intervals' time
			budget = minutes((1 - obj/100) * period / 60)
			used = minutes(float64(s.Failed()*IntervalSeconds) / 60)
			verdict = Verdict(s, obj, period)
		}
		w("| %s (`%s`) | %d | %d | %d | %s | %s | %s | %s | %s | %s |\n",
			titles[s.Key], s.Key, s.Results, s.Successes, s.Failed(), avail, objective, verdict, coverage, budget, used)
	}

	w("\nBelow %s coverage a measure is reported as not measured, unless its failed intervals alone already exceed the downtime budget, which makes it missed whatever the unmeasured time held.\n", pct(CoverageFloor))

	w("\n## Incidents\n\n")
	w("An incident is a run of consecutive failed results of one measure; it ends at the next success.\n\n")
	total := 0
	for _, s := range m.Stats {
		total += len(s.Incidents)
	}
	if total == 0 {
		w("None.\n")
		return b.String()
	}
	w("| Measure | Start (UTC) | End (UTC) | Duration | Failed results | First error |\n")
	w("|---|---|---|---:|---:|---|\n")
	for _, s := range m.Stats {
		for _, i := range s.Incidents {
			end, dur := "ongoing at end of data", fmt.Sprintf("at least %s", fmtDuration(time.Duration(i.Failed*IntervalSeconds)*time.Second))
			if !i.Ongoing() {
				end = i.End.UTC().Format(time.RFC3339)
				dur = fmtDuration(i.End.Sub(i.Start))
			}
			w("| %s | %s | %s | %s | %d | %s |\n", titles[s.Key], i.Start.UTC().Format(time.RFC3339), end, dur, i.Failed, cell(i.FirstError))
		}
	}
	return b.String()
}

// WriteReport writes docs/slo/YYYY-MM.md under dir.
func WriteReport(dir string, m Month) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the report directory is committed and meant to be readable
		return "", err
	}
	path := filepath.Join(dir, m.Month.Format(monthLayout)+reportFileSuffix)
	if err := os.WriteFile(path, []byte(Render(m)), 0o644); err != nil { //nolint:gosec // a committed report, not a secret
		return "", err
	}
	return path, nil
}

// CoverageFloor is the share of the covered range, in percent, that must be
// measured before a verdict of met is given. without it a month with one
// day of probe data and no failures would read as met (docs/sla.md).
const CoverageFloor = 95.0

// Verdict compares one measure with its objective over a period of the
// given seconds. availability alone is computed over measured results, so a
// barely measured month cannot say met; it can still say missed, because
// failed time beyond the whole budget stays a miss whatever the unmeasured
// intervals held.
func Verdict(s Stats, objective, periodSeconds float64) string {
	a, ok := s.Availability()
	if !ok {
		return "not measured"
	}
	failed := float64(s.Failed() * IntervalSeconds)
	budget := (1 - objective/100) * periodSeconds
	coverage := 100 * float64(s.Results*IntervalSeconds) / periodSeconds
	switch {
	case failed > budget:
		return "missed"
	case coverage < CoverageFloor:
		return "not measured"
	case a >= objective:
		return "met"
	default:
		return "missed"
	}
}
