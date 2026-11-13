package apimetrics

import (
	"errors"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/jobs/apipartition"
)

// Resolution names the table a range is read from (contract 7.2).
type Resolution string

// the three resolutions, as the contract and the queries spell them
const (
	Minute Resolution = "minute"
	Hour   Resolution = "hour"
	Day    Resolution = "day"
)

// BucketSeconds is the length of one bucket. utc days have no leap
// seconds in unix time, so a day is always 86400.
func (r Resolution) BucketSeconds() int {
	switch r {
	case Minute:
		return 60
	case Hour:
		return 3600
	default:
		return 86400
	}
}

// Floor is the start of the bucket that holds t.
func (r Resolution) Floor(t time.Time) time.Time {
	t = t.UTC()
	switch r {
	case Minute:
		return t.Truncate(time.Minute)
	case Hour:
		return t.Truncate(time.Hour)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
}

// Next is the start of the bucket after the one starting at t.
func (r Resolution) Next(t time.Time) time.Time {
	switch r {
	case Minute:
		return t.Add(time.Minute)
	case Hour:
		return t.Add(time.Hour)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// ceil moves t forward to a bucket boundary unless it is on one
func (r Resolution) ceil(t time.Time) time.Time {
	f := r.Floor(t)
	if f.Equal(t.UTC()) {
		return f
	}
	return r.Next(f)
}

// presets of contract 7.2
const (
	Preset1h       = "1h"
	Preset24h      = "24h"
	Preset7d       = "7d"
	Preset30d      = "30d"
	Preset90d      = "90d"
	Preset12mo     = "12mo"
	Preset24mo     = "24mo"
	PresetCustom   = "custom"
	PresetPrevious = "previous"
	defaultPreset  = Preset24h
)

// resolution notes of contract 7.2
const (
	NoteOlderThanMinuteRetention = "older_than_minute_retention"
	NoteOlderThanHourlyRetention = "older_than_hourly_retention"
)

// limits of a custom range (contract 7.1). the lower bound keeps the
// arithmetic of a typo such as year 0002 away from the database; the span
// bound keeps a timeseries answer at most 801 points.
var (
	minFrom       = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	maxFutureTo   = 24 * time.Hour
	maxCustomSpan = 800 * 24 * time.Hour
	maxMinuteSpan = 6 * time.Hour
	maxHourSpan   = 31 * 24 * time.Hour
)

// Range is a resolved half-open range [From, To) at one resolution.
type Range struct {
	Preset     string
	From, To   time.Time
	Resolution Resolution
	// Note is set when the retention step changed the resolution
	Note string
}

// Buckets is the number of buckets in the range.
func (r Range) Buckets() int {
	n := 0
	for t := r.From; t.Before(r.To); t = r.Resolution.Next(t) {
		n++
	}
	return n
}

// Resolver turns range parameters into a Range against server time and
// the api retention horizons, which must be the values the jobs retention
// task runs with: a range that starts before the minute horizon would
// otherwise read minute partitions that no longer exist.
type Resolver struct {
	Now       func() time.Time
	Retention config.APIRetention
}

func (rv Resolver) check() error {
	if rv.Now == nil {
		return errors.New("apimetrics: clock is required")
	}
	if rv.Retention.MinuteDays < 1 || rv.Retention.HourlyMonths < config.MinHourlyRetentionMonths {
		return errors.New("apimetrics: retention horizons must be loaded with config.LoadAPIRetention")
	}
	return nil
}

// Parse reads range, from and to. it expects the query to have passed
// single, so every present key holds exactly one value.
func (rv Resolver) Parse(q url.Values) (Range, error) {
	now := rv.Now().UTC()
	preset := defaultPreset
	if v, ok := q["range"]; ok {
		preset = v[0]
	}
	_, hasFrom := q["from"]
	_, hasTo := q["to"]
	if preset != PresetCustom && (hasFrom || hasTo) {
		return Range{}, httpx.BadQuery("from and to are only accepted with range=custom")
	}
	var r Range
	switch preset {
	case Preset1h:
		to := Minute.Next(Minute.Floor(now))
		r = Range{From: to.Add(-time.Hour), To: to, Resolution: Minute}
	case Preset24h, Preset7d, Preset30d:
		hours := map[string]int{Preset24h: 24, Preset7d: 168, Preset30d: 720}[preset]
		to := Hour.Next(Hour.Floor(now))
		r = Range{From: to.Add(-time.Duration(hours) * time.Hour), To: to, Resolution: Hour}
	case Preset90d:
		to := Day.Next(Day.Floor(now))
		r = Range{From: to.AddDate(0, 0, -90), To: to, Resolution: Day}
	case Preset12mo, Preset24mo:
		months := map[string]int{Preset12mo: 12, Preset24mo: 24}[preset]
		to := Day.Next(Day.Floor(now))
		r = Range{From: to.AddDate(0, -months, 0), To: to, Resolution: Day}
	case PresetCustom:
		var err error
		if r, err = custom(q, now, hasFrom, hasTo); err != nil {
			return Range{}, err
		}
	default:
		return Range{}, httpx.BadQuery("range must be one of 1h, 24h, 7d, 30d, 90d, 12mo, 24mo, custom")
	}
	r.Preset = preset
	return rv.retentionStep(r, now), nil
}

func custom(q url.Values, now time.Time, hasFrom, hasTo bool) (Range, error) {
	if !hasFrom || !hasTo {
		return Range{}, httpx.BadQuery("range=custom needs both from and to")
	}
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		return Range{}, httpx.BadQuery("from must be an RFC 3339 time")
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		return Range{}, httpx.BadQuery("to must be an RFC 3339 time")
	}
	from, to = from.UTC(), to.UTC()
	switch {
	case from.Before(minFrom):
		return Range{}, httpx.BadQuery("from must not be before 2000-01-01T00:00:00Z")
	case !from.Before(to):
		return Range{}, httpx.BadQuery("from must be before to")
	case to.After(now.Add(maxFutureTo)):
		return Range{}, httpx.BadQuery("to must be at most 24 hours after server time")
	case to.Sub(from) > maxCustomSpan:
		return Range{}, httpx.BadQuery("a custom range spans at most 800 days")
	}
	span := to.Sub(from)
	res := Day
	switch {
	case span <= maxMinuteSpan:
		res = Minute
	case span <= maxHourSpan:
		res = Hour
	}
	return Range{From: res.Floor(from), To: res.ceil(to), Resolution: res}, nil
}

// retentionStep moves a range whose start lies before the horizon of its
// table to the next coarser table (contract 7.2). the cutoffs are the
// functions the retention job drops partitions by, so both sides agree.
func (rv Resolver) retentionStep(r Range, now time.Time) Range {
	if r.Resolution == Minute && r.From.Before(apipartition.MinuteCutoff(now, rv.Retention.MinuteDays)) {
		r.Resolution, r.Note = Hour, NoteOlderThanMinuteRetention
		r.From, r.To = Hour.Floor(r.From), Hour.ceil(r.To)
	}
	if r.Resolution == Hour && r.From.Before(apipartition.HourlyCutoff(now, rv.Retention.HourlyMonths)) {
		r.Resolution, r.Note = Day, NoteOlderThanHourlyRetention
		r.From, r.To = Day.Floor(r.From), Day.ceil(r.To)
	}
	return r
}

// Previous is the range of equal length right before r, for the overview
// comparison (contract 7.5). r is aligned, so its length is a whole number
// of buckets and the previous range is aligned too.
func (rv Resolver) Previous(r Range) Range {
	span := r.To.Sub(r.From)
	p := Range{Preset: PresetPrevious, From: r.From.Add(-span), To: r.From, Resolution: r.Resolution, Note: r.Note}
	return rv.retentionStep(p, rv.Now().UTC())
}

// Filters are the optional exact-match filters of contract 7.1. nil means
// no filter; a pointer to "" matches the empty value.
type Filters struct {
	Service, Route, Method *string
}

// column byte limits of the api metric tables (00015)
const (
	maxService = 128
	maxMethod  = 16
	maxRoute   = 1024
)

func filterValue(q url.Values, key string, maxBytes int) (*string, error) {
	v, ok := q[key]
	if !ok {
		return nil, nil
	}
	s := v[0]
	if len(s) > maxBytes {
		return nil, httpx.BadQuery(key + " is longer than the stored values can be")
	}
	if !utf8.ValidString(s) {
		return nil, httpx.BadQuery(key + " must be valid utf-8")
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return nil, httpx.BadQuery(key + " must not contain control characters")
		}
	}
	return &s, nil
}

// ParseFilters reads service, route and method.
func ParseFilters(q url.Values) (Filters, error) {
	var f Filters
	var err error
	if f.Service, err = filterValue(q, "service", maxService); err != nil {
		return Filters{}, err
	}
	if f.Route, err = filterValue(q, "route", maxRoute); err != nil {
		return Filters{}, err
	}
	if f.Method, err = filterValue(q, "method", maxMethod); err != nil {
		return Filters{}, err
	}
	return f, nil
}

// single refuses parameters a route does not take and parameters given
// more than once, so a typo or a smuggled second value never passes
// silently (contract 7.1).
func single(q url.Values, allowed ...string) error {
	for k, v := range q {
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
				break
			}
		}
		if !ok {
			return httpx.BadQuery("parameter " + k + " is not accepted by this route")
		}
		if len(v) != 1 {
			return httpx.BadQuery("parameter " + k + " must be given once")
		}
	}
	return nil
}
