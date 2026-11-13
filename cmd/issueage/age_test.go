package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

func policy(t *testing.T, holidays ...string) Policy {
	t.Helper()
	c, err := NewCalendar(holidays)
	require.NoError(t, err)
	p := Policy{ResponseDays: 5, ResolutionDays: 10, WarnDays: 1, Calendar: c}
	require.NoError(t, p.Validate())
	return p
}

func TestElapsedSkipsWeekendsAndHolidays(t *testing.T) {
	p := policy(t)
	// friday 18:00 to monday 06:00: 6 h of friday, 6 h of monday
	require.Equal(t, 12*time.Hour, p.Calendar.Elapsed(ts(t, "2026-11-06T18:00:00Z"), ts(t, "2026-11-09T06:00:00Z")))
	// saturday to sunday: nothing
	require.Zero(t, p.Calendar.Elapsed(ts(t, "2026-11-07T01:00:00Z"), ts(t, "2026-11-08T23:00:00Z")))
	// backwards: nothing
	require.Zero(t, p.Calendar.Elapsed(ts(t, "2026-11-10T00:00:00Z"), ts(t, "2026-11-09T00:00:00Z")))
	// a non-utc input is read in utc: 2026-11-09 01:00+03:00 is sunday 22:00 utc
	ist := time.FixedZone("ist", 3*3600)
	require.Equal(t, time.Hour, p.Calendar.Elapsed(time.Date(2026, 11, 9, 1, 0, 0, 0, ist), ts(t, "2026-11-09T01:00:00Z")))

	h := policy(t, "2026-12-25", " 2027-01-01 ")
	require.Equal(t, []string{"2026-12-25", "2027-01-01"}, h.Calendar.Holidays())
	// thursday 24 december 00:00 to monday 28 december 00:00: only thursday
	require.Equal(t, day, h.Calendar.Elapsed(ts(t, "2026-12-24T00:00:00Z"), ts(t, "2026-12-28T00:00:00Z")))

	_, err := NewCalendar([]string{"25.12.2026"})
	require.Error(t, err)
}

func TestAddIsTheInverseOfElapsed(t *testing.T) {
	p := policy(t, "2026-12-25")
	start := ts(t, "2026-11-01T00:00:00Z")
	// every 7 h 13 min over two months, so starts land on every weekday,
	// weekends, the holiday and odd minutes
	for from := start; from.Before(start.AddDate(0, 2, 0)); from = from.Add(7*time.Hour + 13*time.Minute) {
		for _, d := range []time.Duration{time.Minute, 4 * day, 5 * day, 9 * day, 10 * day} {
			to := p.Calendar.Add(from, d)
			require.Equal(t, d, p.Calendar.Elapsed(from, to), "from %s plus %s", from, d)
			// earliest such moment: one second earlier is short
			require.Less(t, p.Calendar.Elapsed(from, to.Add(-time.Second)), d, "from %s plus %s", from, d)
		}
	}
}

func TestAssessCrossesLimitsExactly(t *testing.T) {
	p := policy(t)
	// monday 10:00 utc
	tk := Ticket{Number: 1, CreatedAt: ts(t, "2026-11-02T10:00:00Z")}

	cases := []struct {
		now        string
		response   State
		resolution State
	}{
		{"2026-11-02T10:00:00Z", StateOpen, StateOpen},
		{"2026-11-06T09:59:59Z", StateOpen, StateOpen},       // 3 d 23:59:59
		{"2026-11-06T10:00:00Z", StateDue, StateOpen},        // 4 business days: response approaching
		{"2026-11-08T12:00:00Z", StateDue, StateOpen},        // the weekend does not count
		{"2026-11-09T09:59:59Z", StateDue, StateOpen},        // one second before 5
		{"2026-11-09T10:00:00Z", StateOverdue, StateOpen},    // exactly 5 business days
		{"2026-11-13T09:59:59Z", StateOverdue, StateOpen},    // one second before 9
		{"2026-11-13T10:00:00Z", StateOverdue, StateDue},     // 9 business days: resolution approaching
		{"2026-11-16T09:59:59Z", StateOverdue, StateDue},     // one second before 10
		{"2026-11-16T10:00:00Z", StateOverdue, StateOverdue}, // exactly 10 business days
	}
	for _, c := range cases {
		a := p.Assess(tk, ts(t, c.now))
		require.Equal(t, c.response, a.Response, "response at %s", c.now)
		require.Equal(t, c.resolution, a.Resolution, "resolution at %s", c.now)
		require.Equal(t, ts(t, "2026-11-09T10:00:00Z"), a.ResponseDeadline)
		require.Equal(t, ts(t, "2026-11-16T10:00:00Z"), a.ResolutionDeadline)
	}
}

func TestAssessWeekendAndHolidayStart(t *testing.T) {
	p := policy(t, "2026-12-25")
	// opened saturday: the clock starts monday 00:00, 5 days end with friday
	a := p.Assess(Ticket{CreatedAt: ts(t, "2026-11-07T15:00:00Z")}, ts(t, "2026-11-13T23:59:59Z"))
	require.Equal(t, StateDue, a.Response)
	require.Equal(t, ts(t, "2026-11-14T00:00:00Z"), a.ResponseDeadline)
	// opened on the wednesday before christmas at noon: thursday counts,
	// the holiday and the weekend do not
	a = p.Assess(Ticket{CreatedAt: ts(t, "2026-12-23T12:00:00Z")}, ts(t, "2026-12-30T12:00:00Z"))
	require.Equal(t, 4*day, a.Elapsed)
	require.Equal(t, StateDue, a.Response)
	require.Equal(t, ts(t, "2026-12-31T12:00:00Z"), a.ResponseDeadline)
}

func TestAssessResponses(t *testing.T) {
	p := policy(t)
	created := ts(t, "2026-11-02T10:00:00Z")
	in := ts(t, "2026-11-09T09:59:59Z")
	late := ts(t, "2026-11-09T10:00:00Z")

	a := p.Assess(Ticket{CreatedAt: created, FirstResponse: &in}, ts(t, "2026-11-12T00:00:00Z"))
	require.Equal(t, StateMet, a.Response)
	require.Empty(t, a.Wanted())

	a = p.Assess(Ticket{CreatedAt: created, FirstResponse: &late}, ts(t, "2026-11-10T00:00:00Z"))
	require.Equal(t, StateOverdue, a.Response, "a response at the limit is late")
	require.Equal(t, []string{LabelResponseOverdue}, a.Wanted())
}

func TestChanges(t *testing.T) {
	p := policy(t)
	created := ts(t, "2026-11-02T10:00:00Z")
	resp := ts(t, "2026-11-03T10:00:00Z")

	// approaching: add the due label
	a := p.Assess(Ticket{CreatedAt: created}, ts(t, "2026-11-06T10:00:00Z"))
	add, remove := a.Changes()
	require.Equal(t, []string{LabelResponseDue}, add)
	require.Empty(t, remove)

	// crossed: the due label goes, the overdue label comes
	a = p.Assess(Ticket{CreatedAt: created, Labels: []string{"bug", LabelResponseDue}}, ts(t, "2026-11-09T10:00:00Z"))
	add, remove = a.Changes()
	require.Equal(t, []string{LabelResponseOverdue}, add)
	require.Equal(t, []string{LabelResponseDue}, remove)

	// answered later: the overdue label is a record and stays, nothing to add
	late := ts(t, "2026-11-10T10:00:00Z")
	a = p.Assess(Ticket{CreatedAt: created, FirstResponse: &late, Labels: []string{LabelResponseOverdue}}, ts(t, "2026-11-10T11:00:00Z"))
	add, remove = a.Changes()
	require.Empty(t, add)
	require.Empty(t, remove)

	// answered in time while flagged due: the due label goes
	a = p.Assess(Ticket{CreatedAt: created, FirstResponse: &resp, Labels: []string{LabelResponseDue}}, ts(t, "2026-11-06T12:00:00Z"))
	add, remove = a.Changes()
	require.Empty(t, add)
	require.Equal(t, []string{LabelResponseDue}, remove)

	// already labelled: nothing to do
	a = p.Assess(Ticket{CreatedAt: created, Labels: []string{LabelResponseOverdue, LabelResolutionOverdue}}, ts(t, "2026-11-20T00:00:00Z"))
	add, remove = a.Changes()
	require.Empty(t, add)
	require.Empty(t, remove)
}

func TestWarnDaysZeroAndValidation(t *testing.T) {
	p := policy(t)
	p.WarnDays = 0
	require.NoError(t, p.Validate())
	a := p.Assess(Ticket{CreatedAt: ts(t, "2026-11-02T10:00:00Z")}, ts(t, "2026-11-09T09:59:59Z"))
	require.Equal(t, StateOpen, a.Response, "without a warning window nothing is due")

	for _, bad := range []Policy{
		{ResponseDays: 0, ResolutionDays: 10},
		{ResponseDays: 5, ResolutionDays: 4},
		{ResponseDays: 5, ResolutionDays: 10, WarnDays: 5},
		{ResponseDays: 5, ResolutionDays: 10, WarnDays: -1},
	} {
		require.Error(t, bad.Validate(), "%+v", bad)
	}
}
