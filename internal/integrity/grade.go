package integrity

import (
	"fmt"
	"math"
	"slices"
)

// RuleVersion numbers the rule set below. a change of any threshold or rule
// is a new version; stored grades keep the version they were computed with.
const RuleVersion = 1

// grades, best first. a day with too little traffic has no grade.
const (
	GradeA = "A"
	GradeB = "B"
	GradeC = "C"
)

// Levels a reason sets. a reason at LevelB caps the grade at B, one at
// LevelC caps it at C. LevelNone explains a day without a grade.
const (
	LevelB    = "B"
	LevelC    = "C"
	LevelNone = "none"
)

// reason codes, stable api values
const (
	ReasonTooLittleTraffic = "too_little_traffic"
	ReasonHostingShare     = "hosting_share"
	ReasonQuarantineShare  = "quarantine_share"
	ReasonProofFailures    = "proof_failures"
	ReasonFewNetworks      = "few_networks"
	ReasonSpike            = "spike"
	ReasonPageVolume       = "page_volume"
)

// ReasonCodes lists every code in display order.
var ReasonCodes = []string{
	ReasonHostingShare, ReasonQuarantineShare, ReasonFewNetworks, ReasonSpike,
	ReasonProofFailures, ReasonPageVolume, ReasonTooLittleTraffic,
}

// thresholds of rule version 1
const (
	// MinEvents is the fewest accepted events a day needs for a grade
	MinEvents = 50
	// hosting and anonymiser share of classified events
	hostingShareB = 0.25
	hostingShareC = 0.50
	// quarantine share of accepted events
	quarantineShareB = 0.10
	quarantineShareC = 0.30
	// share of challenge-backed requests refused for their proof
	proofFailuresB = 0.50
	// fewer than one network per this many visitors, from fewNetworksMin
	// visitors on
	fewNetworksVisitorsPerNetwork = 100
	fewNetworksMin                = 100
	// visitors at least this many times the baseline median, from spikeMin
	// visitors on, with at least BaselineMinDays days of baseline
	spikeFactor     = 10
	spikeMin        = 100
	BaselineMinDays = 7
	// page views per visitor the page itself reports
	pageVolumePerVisitor = 50
)

// BaselineDays is how many days before a day its baseline covers.
const BaselineDays = 28

// Inputs are one site's evidence for one utc day. every count is a
// platform observation except Pageviews, which the page reports and which
// can therefore only lower a grade.
type Inputs struct {
	// Events are accepted events, both bands; Quarantined is the part of
	// them that went to the quarantine band
	Events      int64
	Quarantined int64
	// ProofFailures are requests refused for a missing, expired, foreign,
	// replayed or insufficient proof of work
	ProofFailures int64
	// accepted events by network class
	NetAccess       int64
	NetOrganisation int64
	NetHosting      int64
	NetAnonymiser   int64
	NetUnknown      int64
	// Networks is the number of distinct autonomous systems the site's
	// accepted events came from
	Networks int64
	// Visitors and Pageviews are the primary band's umami numbers
	Visitors  int64
	Pageviews int64
	// BaselineVisitors is the median of the primary visitors of up to
	// BaselineDays earlier days with data; BaselineDaysUsed says how many
	BaselineVisitors *int64
	BaselineDaysUsed int
}

// Classified is the number of events with a network class.
func (in Inputs) Classified() int64 {
	return in.NetAccess + in.NetOrganisation + in.NetHosting + in.NetAnonymiser + in.NetUnknown
}

// HostingShare is the hosting and anonymiser share of classified events,
// nil before any event was classified.
func (in Inputs) HostingShare() *float64 {
	return share(in.NetHosting+in.NetAnonymiser, in.Classified())
}

// QuarantineShare is the share of accepted events in the quarantine band.
func (in Inputs) QuarantineShare() *float64 { return share(in.Quarantined, in.Events) }

// ProofPassRate is the share of challenge-backed requests whose proof held.
func (in Inputs) ProofPassRate() *float64 { return share(in.Events, in.Events+in.ProofFailures) }

func share(n, d int64) *float64 {
	if d <= 0 {
		return nil
	}
	v := float64(n) / float64(d)
	return &v
}

// Reason is one finding of the rule set. Value is the observed figure and
// Limit the threshold it crossed, in the unit the code implies (a share
// from 0 to 1, a count or a factor).
type Reason struct {
	Code  string  `json:"code"`
	Level string  `json:"level"`
	Value float64 `json:"value"`
	Limit float64 `json:"limit"`
}

// Text is the plain language form of the reason, the same for the project
// and for operators.
func (r Reason) Text() string {
	switch r.Code {
	case ReasonTooLittleTraffic:
		return fmt.Sprintf("Not graded: %s accepted events, a grade needs at least %s.", count(r.Value), count(r.Limit))
	case ReasonHostingShare:
		return fmt.Sprintf("%s of events came from hosting networks or anonymisers (limit %s). Ordinary visitors arrive from access networks; hosting networks also carry VPNs and relays.", pct(r.Value), pct(r.Limit))
	case ReasonQuarantineShare:
		return fmt.Sprintf("%s of accepted events went to the quarantine band (limit %s): crowded address prefixes or visitors past the hourly volume bound.", pct(r.Value), pct(r.Limit))
	case ReasonProofFailures:
		return fmt.Sprintf("%s of requests failed the proof of work (limit %s). They were refused and are not in the numbers, but show automated traffic aimed at the site.", pct(r.Value), pct(r.Limit))
	case ReasonFewNetworks:
		return fmt.Sprintf("Visitors came from few networks: one network per %s visitors (limit %s).", count(r.Value), count(r.Limit))
	case ReasonSpike:
		return fmt.Sprintf("Visitors were %sx the site's baseline (limit %sx).", factor(r.Value), factor(r.Limit))
	case ReasonPageVolume:
		return fmt.Sprintf("The page reported %s page views per visitor (limit %s).", factor(r.Value), factor(r.Limit))
	}
	return r.Code
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

func count(v float64) string { return fmt.Sprintf("%.0f", v) }

func factor(v float64) string {
	if v >= 10 || v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// Result is the grade of one site and day. Grade is "" when the day has too
// little traffic; Reasons then holds the too_little_traffic reason.
type Result struct {
	Grade   string   `json:"grade"`
	Reasons []Reason `json:"reasons"`
}

// Grade applies rule version 1. it starts at A and every rule can only
// lower it, so no input, and in particular nothing the page reports, can
// raise a grade.
func Grade(in Inputs) Result {
	if in.Events < MinEvents {
		return Result{Reasons: []Reason{{Code: ReasonTooLittleTraffic, Level: LevelNone, Value: float64(in.Events), Limit: MinEvents}}}
	}
	var rs []Reason
	add := func(code, level string, value, limit float64) {
		rs = append(rs, Reason{Code: code, Level: level, Value: value, Limit: limit})
	}
	if s := in.HostingShare(); s != nil {
		switch {
		case *s >= hostingShareC:
			add(ReasonHostingShare, LevelC, *s, hostingShareC)
		case *s >= hostingShareB:
			add(ReasonHostingShare, LevelB, *s, hostingShareB)
		}
	}
	if s := in.QuarantineShare(); s != nil {
		switch {
		case *s >= quarantineShareC:
			add(ReasonQuarantineShare, LevelC, *s, quarantineShareC)
		case *s >= quarantineShareB:
			add(ReasonQuarantineShare, LevelB, *s, quarantineShareB)
		}
	}
	// the network count exists only for days the gateway classified
	if in.Classified() > 0 && in.Visitors >= fewNetworksMin &&
		in.Networks*fewNetworksVisitorsPerNetwork < in.Visitors {
		add(ReasonFewNetworks, LevelB, float64(in.Visitors)/float64(max(in.Networks, 1)), fewNetworksVisitorsPerNetwork)
	}
	if in.BaselineVisitors != nil && in.BaselineDaysUsed >= BaselineMinDays && in.Visitors >= spikeMin {
		base := max(*in.BaselineVisitors, 1)
		if in.Visitors >= spikeFactor*base {
			add(ReasonSpike, LevelB, float64(in.Visitors)/float64(base), spikeFactor)
		}
	}
	if s := share(in.ProofFailures, in.Events+in.ProofFailures); s != nil && *s >= proofFailuresB {
		add(ReasonProofFailures, LevelB, *s, proofFailuresB)
	}
	if in.Visitors > 0 && in.Pageviews >= pageVolumePerVisitor*in.Visitors {
		add(ReasonPageVolume, LevelB, float64(in.Pageviews)/float64(in.Visitors), pageVolumePerVisitor)
	}
	g := GradeA
	for _, r := range rs {
		g = Worse(g, r.Level)
	}
	if rs == nil {
		rs = []Reason{}
	}
	return Result{Grade: g, Reasons: rs}
}

// Worse returns the worse of two grades; an empty grade or a level that is
// not a grade leaves a unchanged.
func Worse(a, b string) string {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func rank(g string) int {
	switch g {
	case GradeA:
		return 1
	case GradeB:
		return 2
	case GradeC:
		return 3
	}
	return 0
}

// Rank orders grades for sorting: A is 1, B 2, C 3, no grade 0.
func Rank(g string) int { return rank(g) }

// RangeShare is the share of graded site days at a grade or worse from
// which that grade becomes the grade of a range.
const RangeShare = 0.25

// DayGrade is one graded or ungraded site day of a range.
type DayGrade struct {
	Grade   string
	Reasons []Reason
}

// RangeReason sums one reason code over a range: the site days it applied
// to and the worst level it set.
type RangeReason struct {
	Code  string `json:"code"`
	Level string `json:"level"`
	Days  int    `json:"days"`
}

// RangeGrade is the grade of a project or site over several days.
type RangeGrade struct {
	// Grade is "" when no site day of the range has a grade
	Grade      string        `json:"grade"`
	GradedDays int           `json:"graded_days"`
	DaysA      int           `json:"days_a"`
	DaysB      int           `json:"days_b"`
	DaysC      int           `json:"days_c"`
	Reasons    []RangeReason `json:"reasons"`
}

// Combine grades a range of site days: C when at least a quarter of the
// graded days are C, B when at least a quarter are B or C, A otherwise. a
// single bad day does not define a month, a recurring pattern does, and the
// reasons list every day count so a single bad day stays visible.
func Combine(days []DayGrade) RangeGrade {
	out := RangeGrade{Reasons: []RangeReason{}}
	by := map[string]*RangeReason{}
	for _, d := range days {
		switch d.Grade {
		case GradeA:
			out.DaysA++
		case GradeB:
			out.DaysB++
		case GradeC:
			out.DaysC++
		default:
			continue
		}
		for _, r := range d.Reasons {
			rr, ok := by[r.Code]
			if !ok {
				rr = &RangeReason{Code: r.Code, Level: r.Level}
				by[r.Code] = rr
			}
			rr.Days++
			rr.Level = Worse(rr.Level, r.Level)
		}
	}
	out.GradedDays = out.DaysA + out.DaysB + out.DaysC
	out.Grade = RangeOf(out.DaysA, out.DaysB, out.DaysC)
	for _, code := range ReasonCodes {
		if rr, ok := by[code]; ok {
			out.Reasons = append(out.Reasons, *rr)
		}
	}
	slices.SortStableFunc(out.Reasons, func(a, b RangeReason) int { return rank(b.Level) - rank(a.Level) })
	return out
}

// RangeOf is the range rule of Combine on day counts. the operator list
// computes the same rule in sql (operator.integritySQL) so it can sort by it.
func RangeOf(a, b, c int) string {
	n := a + b + c
	switch {
	case n == 0:
		return ""
	case float64(c) >= RangeShare*float64(n):
		return GradeC
	case float64(b+c) >= RangeShare*float64(n):
		return GradeB
	}
	return GradeA
}

// RangeText describes a reason code over a range, without the figures of a
// single day, for the reasons panel of a project or a site.
func RangeText(code string) string {
	switch code {
	case ReasonTooLittleTraffic:
		return "Days with too little traffic for a grade."
	case ReasonHostingShare:
		return "A large share of events came from hosting networks or anonymisers."
	case ReasonQuarantineShare:
		return "A large share of accepted events went to the quarantine band."
	case ReasonProofFailures:
		return "Many requests failed the proof of work and were refused."
	case ReasonFewNetworks:
		return "Visitors came from few networks for their number."
	case ReasonSpike:
		return "Visitors rose far above the site's baseline."
	case ReasonPageVolume:
		return "The page reported an unusual number of page views per visitor."
	}
	return code
}
