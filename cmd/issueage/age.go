package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

const day = 24 * time.Hour

// Calendar counts business time: monday to friday in utc, minus holidays.
// the support targets are in business days, so an issue opened on friday
// evening does not use up its window over the weekend.
type Calendar struct {
	holidays map[string]bool
}

// NewCalendar takes holidays as YYYY-MM-DD utc days.
func NewCalendar(holidays []string) (Calendar, error) {
	c := Calendar{holidays: map[string]bool{}}
	for _, h := range holidays {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		d, err := time.Parse(time.DateOnly, h)
		if err != nil {
			return Calendar{}, fmt.Errorf("holiday %q: want YYYY-MM-DD", h)
		}
		c.holidays[d.Format(time.DateOnly)] = true
	}
	return c, nil
}

// Holidays lists the configured holidays in order.
func (c Calendar) Holidays() []string {
	out := make([]string, 0, len(c.holidays))
	for h := range c.holidays {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (c Calendar) workday(d time.Time) bool {
	switch d.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	return !c.holidays[d.Format(time.DateOnly)]
}

// Elapsed is the business time between from and to; zero when to is not
// after from.
func (c Calendar) Elapsed(from, to time.Time) time.Duration {
	from, to = from.UTC(), to.UTC()
	if !to.After(from) {
		return 0
	}
	var total time.Duration
	for d := startOfDay(from); d.Before(to); d = d.AddDate(0, 0, 1) {
		if !c.workday(d) {
			continue
		}
		s, e := d, d.AddDate(0, 0, 1)
		if from.After(s) {
			s = from
		}
		if to.Before(e) {
			e = to
		}
		total += e.Sub(s)
	}
	return total
}

// Add returns the earliest time t with Elapsed(from, t) == d, the moment a
// business-time budget of d that starts at from runs out.
func (c Calendar) Add(from time.Time, d time.Duration) time.Time {
	s := from.UTC()
	for dd := startOfDay(s); ; dd = dd.AddDate(0, 0, 1) {
		next := dd.AddDate(0, 0, 1)
		if dd.After(s) {
			s = dd
		}
		if !c.workday(dd) {
			continue
		}
		avail := next.Sub(s)
		if d <= avail {
			return s.Add(d)
		}
		d -= avail
	}
}

// Policy holds the support targets in business days.
type Policy struct {
	ResponseDays   int
	ResolutionDays int
	// WarnDays is how many business days before a limit an issue is flagged
	// as approaching it; 0 flags only issues that crossed a limit.
	WarnDays int
	Calendar Calendar
}

func (p Policy) Validate() error {
	switch {
	case p.ResponseDays < 1:
		return fmt.Errorf("response days must be at least 1")
	case p.ResolutionDays < p.ResponseDays:
		return fmt.Errorf("resolution days must be at least the response days")
	case p.WarnDays < 0 || p.WarnDays >= p.ResponseDays:
		return fmt.Errorf("warn days must be between 0 and the response days minus 1")
	}
	return nil
}

// State of one target for one issue.
type State string

const (
	StateOpen    State = "open"    // inside the limit, not yet approaching it
	StateDue     State = "due"     // within WarnDays of the limit
	StateOverdue State = "overdue" // limit crossed (or a response came after it)
	StateMet     State = "met"     // a maintainer responded inside the limit
)

// Ticket is an open support issue as the policy sees it.
type Ticket struct {
	Number    int
	Title     string
	URL       string
	CreatedAt time.Time
	// FirstResponse is the first maintainer comment, nil when none exists.
	FirstResponse *time.Time
	Labels        []string
}

// Assessment is the outcome of the policy for one ticket at one moment.
type Assessment struct {
	Ticket
	Elapsed            time.Duration
	Response           State
	ResponseDeadline   time.Time
	Resolution         State
	ResolutionDeadline time.Time
}

// Assess applies the policy to an open ticket at now. a limit is crossed at
// the exact moment the business time since creation reaches it.
func (p Policy) Assess(t Ticket, now time.Time) Assessment {
	a := Assessment{
		Ticket:             t,
		Elapsed:            p.Calendar.Elapsed(t.CreatedAt, now),
		ResponseDeadline:   p.Calendar.Add(t.CreatedAt, time.Duration(p.ResponseDays)*day),
		ResolutionDeadline: p.Calendar.Add(t.CreatedAt, time.Duration(p.ResolutionDays)*day),
	}
	if t.FirstResponse != nil {
		// a late response stays a missed target; an early one ends the clock
		if p.Calendar.Elapsed(t.CreatedAt, *t.FirstResponse) >= time.Duration(p.ResponseDays)*day {
			a.Response = StateOverdue
		} else {
			a.Response = StateMet
		}
	} else {
		a.Response = p.state(a.Elapsed, p.ResponseDays)
	}
	a.Resolution = p.state(a.Elapsed, p.ResolutionDays)
	return a
}

func (p Policy) state(elapsed time.Duration, limit int) State {
	switch {
	case elapsed >= time.Duration(limit)*day:
		return StateOverdue
	case p.WarnDays > 0 && elapsed >= time.Duration(limit-p.WarnDays)*day:
		return StateDue
	}
	return StateOpen
}

// the labels the tool owns. "due" labels follow the current state and are
// removed when it changes; "overdue" labels record a missed target and are
// never removed by the tool, so a closed issue keeps the record.
const (
	LabelResponseDue       = "support: response due"
	LabelResponseOverdue   = "support: response overdue"
	LabelResolutionDue     = "support: resolution due"
	LabelResolutionOverdue = "support: resolution overdue"
)

// LabelSpec is one label the tool may create in the repository.
type LabelSpec struct {
	Name, Color, Description string
}

// Labels returns the four labels with descriptions that state the policy.
func (p Policy) Labels() []LabelSpec {
	return []LabelSpec{
		{LabelResponseDue, "fbca04", fmt.Sprintf("No maintainer response yet; the %d business day response target ends within %d business day(s)", p.ResponseDays, p.WarnDays)},
		{LabelResponseOverdue, "d93f0b", fmt.Sprintf("No maintainer response within %d business days", p.ResponseDays)},
		{LabelResolutionDue, "fbca04", fmt.Sprintf("Open; the %d business day resolution target ends within %d business day(s)", p.ResolutionDays, p.WarnDays)},
		{LabelResolutionOverdue, "b60205", fmt.Sprintf("Not resolved within %d business days", p.ResolutionDays)},
	}
}

// Wanted is the set of tool labels an assessment calls for.
func (a Assessment) Wanted() []string {
	var out []string
	switch a.Response {
	case StateDue:
		out = append(out, LabelResponseDue)
	case StateOverdue:
		out = append(out, LabelResponseOverdue)
	}
	switch a.Resolution {
	case StateDue:
		out = append(out, LabelResolutionDue)
	case StateOverdue:
		out = append(out, LabelResolutionOverdue)
	}
	return out
}

// Changes compares the wanted labels with the ticket's current labels.
func (a Assessment) Changes() (add, remove []string) {
	wanted := a.Wanted()
	for _, w := range wanted {
		if !slices.Contains(a.Labels, w) {
			add = append(add, w)
		}
	}
	for _, l := range []string{LabelResponseDue, LabelResolutionDue} {
		if slices.Contains(a.Labels, l) && !slices.Contains(wanted, l) {
			remove = append(remove, l)
		}
	}
	return add, remove
}
