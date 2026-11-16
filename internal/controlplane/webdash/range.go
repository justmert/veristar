package webdash

import (
	"fmt"
	"net/url"
	"time"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/umami"
)

// range rules of contract 18.1: whole utc days, both ends included, the
// same unit the aggregates and the operator views use
const (
	// DefaultDays is the range without from and to: the last 30 utc days,
	// today included
	DefaultDays = 30
	// MaxDays bounds a range; the aggregates are kept 25 months, so a
	// longer range could not be compared with its previous period anyway
	MaxDays = 400
)

// Days is a range of utc days, both included.
type Days struct {
	From time.Time
	To   time.Time
}

// N is the number of days.
func (d Days) N() int { return int(d.To.Sub(d.From).Hours()/24) + 1 }

// List returns every day in order.
func (d Days) List() []time.Time {
	out := make([]time.Time, 0, d.N())
	for x := d.From; !x.After(d.To); x = x.AddDate(0, 0, 1) {
		out = append(out, x)
	}
	return out
}

// Previous is the range of equal length that ends the day before From.
func (d Days) Previous() Days {
	n := d.N()
	return Days{From: d.From.AddDate(0, 0, -n), To: d.From.AddDate(0, 0, -1)}
}

// Umami is the range as umami's read routes take it: from 00:00:00.000 of
// the first day to 23:59:59.999 of the last. umami includes both ends, so
// the next midnight as the end would count an event at exactly 00:00:00 in
// two ranges (docs/reference/web-metrics.md, day boundaries).
func (d Days) Umami() umami.Range {
	return umami.Range{Start: d.From, End: d.To.AddDate(0, 0, 1).Add(-time.Millisecond)}
}

// RangeJSON is the range of an answer.
type RangeJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

// JSON is the range in its answer form.
func (d Days) JSON() RangeJSON {
	return RangeJSON{From: d.From.Format(time.DateOnly), To: d.To.Format(time.DateOnly), Days: d.N()}
}

// UTCDay is the utc day that holds t.
func UTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func oneValue(q url.Values, key string) (string, bool, error) {
	v, ok := q[key]
	if !ok {
		return "", false, nil
	}
	if len(v) != 1 {
		return "", false, httpx.BadQuery(key + " must be given once")
	}
	return v[0], true, nil
}

func parseDay(q url.Values, key string) (time.Time, bool, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return time.Time{}, false, httpx.BadQuery(key + " must be a day such as 2026-11-01")
	}
	return t, true, nil
}

// ParseDays reads from and to. without to the range ends today; without
// from it holds DefaultDays days. to after today, to before from and more
// than MaxDays days are 400 invalid_query.
func ParseDays(q url.Values, now time.Time) (Days, error) {
	today := UTCDay(now)
	from, hasFrom, err := parseDay(q, "from")
	if err != nil {
		return Days{}, err
	}
	to, hasTo, err := parseDay(q, "to")
	if err != nil {
		return Days{}, err
	}
	if !hasTo {
		to = today
	}
	if !hasFrom {
		from = to.AddDate(0, 0, -(DefaultDays - 1))
	}
	d := Days{From: from, To: to}
	switch {
	case to.After(today):
		return Days{}, httpx.BadQuery("to must not be after today (utc)")
	case to.Before(from):
		return Days{}, httpx.BadQuery("to must not be before from")
	case d.N() > MaxDays:
		return Days{}, httpx.BadQuery(fmt.Sprintf("the range may hold at most %d days", MaxDays))
	}
	return d, nil
}
