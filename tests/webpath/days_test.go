package webpath

// the utc day arithmetic of the live checks. it carries no build tag, so its
// unit test runs in make test while the live suite stays behind the webpath
// tag.

import (
	"testing"
	"time"
)

// runStart is when the test binary started. every live read covers the utc
// days from this day on: a run takes over an hour and may cross midnight, and
// a range of the current day alone would then miss the events sent before
// it. umami clamps each read to the website's reset time, so the wider range
// never picks up an earlier scenario's events.
var runStart = time.Now().UTC()

// runWindow is the read range of the live checks at now: from the start of
// the utc day the run began until an hour past now.
func runWindow(start, now time.Time) (from, to time.Time) {
	return start.UTC().Truncate(24 * time.Hour), now.UTC().Add(time.Hour)
}

// pastMidnight returns how long to wait at now so that a step taking up to
// d does not cross utc midnight, or zero when it fits in the current day.
// the wait ends five seconds into the next day.
func pastMidnight(now time.Time, d time.Duration) time.Duration {
	now = now.UTC()
	next := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	if now.Add(d).Before(next) {
		return 0
	}
	return next.Add(5 * time.Second).Sub(now)
}

// secondVisitAt is when the second static visit starts after the first ended
// at end: more than 30 minutes later (the replayer drops the cache token) and
// in another utc hour (otherwise umami joins events of the hour the visit
// started in).
func secondVisitAt(end time.Time) time.Time {
	target := end.UTC().Add(31 * time.Minute)
	if h := end.UTC().Truncate(time.Hour).Add(time.Hour + 5*time.Second); h.After(target) {
		target = h
	}
	return target
}

// sameUTCDay reports whether a and b fall on one utc day. the visitor salt
// changes daily, so a returning browser on another utc day counts as one more
// visitor.
func sameUTCDay(a, b time.Time) bool {
	return a.UTC().Truncate(24 * time.Hour).Equal(b.UTC().Truncate(24 * time.Hour))
}

func TestRunWindowSpansMidnight(t *testing.T) {
	start := time.Date(2026, 11, 17, 23, 40, 0, 0, time.UTC)
	now := time.Date(2026, 11, 18, 0, 20, 0, 0, time.UTC)
	from, to := runWindow(start, now)
	if want := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Errorf("from %s, want %s", from, want)
	}
	if want := time.Date(2026, 11, 18, 1, 20, 0, 0, time.UTC); !to.Equal(want) {
		t.Errorf("to %s, want %s", to, want)
	}
	// an event of the first day, before midnight, is inside the window
	if ev := start.Add(5 * time.Minute); ev.Before(from) || ev.After(to) {
		t.Errorf("event at %s outside [%s, %s]", ev, from, to)
	}
}

func TestPastMidnight(t *testing.T) {
	cases := []struct {
		now  time.Time
		d    time.Duration
		want time.Duration
	}{
		{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), 10 * time.Minute, 0},
		{time.Date(2026, 11, 17, 23, 55, 0, 0, time.UTC), 10 * time.Minute, 5*time.Minute + 5*time.Second},
		{time.Date(2026, 11, 17, 23, 49, 59, 0, time.UTC), 10 * time.Minute, 0},
	}
	for _, c := range cases {
		if got := pastMidnight(c.now, c.d); got != c.want {
			t.Errorf("pastMidnight(%s, %s) = %s, want %s", c.now, c.d, got, c.want)
		}
	}
}

func TestSecondVisitAt(t *testing.T) {
	cases := []struct{ end, want time.Time }{
		// 31 minutes later is already another hour
		{time.Date(2026, 11, 17, 10, 40, 0, 0, time.UTC), time.Date(2026, 11, 17, 11, 11, 0, 0, time.UTC)},
		// 31 minutes later is the same hour: wait for the next one
		{time.Date(2026, 11, 17, 10, 5, 0, 0, time.UTC), time.Date(2026, 11, 17, 11, 0, 5, 0, time.UTC)},
		// crosses utc midnight
		{time.Date(2026, 11, 17, 23, 50, 0, 0, time.UTC), time.Date(2026, 11, 18, 0, 21, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got := secondVisitAt(c.end)
		if !got.Equal(c.want) {
			t.Errorf("secondVisitAt(%s) = %s, want %s", c.end, got, c.want)
		}
		if got.Sub(c.end) <= 30*time.Minute || got.Truncate(time.Hour).Equal(c.end.Truncate(time.Hour)) {
			t.Errorf("secondVisitAt(%s) = %s: not 30 minutes and another hour later", c.end, got)
		}
	}
	if sameUTCDay(cases[2].end, secondVisitAt(cases[2].end)) {
		t.Error("23:50 and 00:21 reported as one utc day")
	}
}
