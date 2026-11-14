package mcpserver

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

// input rules shared by the tools (contract 12.4)

// earliest is the first instant any range may start, as for the api
// metrics routes (contract 7.1)
var earliest = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// maxWebSpan is the longest web range, the 366 days of the operator views
// (contract 8.1) plus the partial days a datetime range can touch
const maxWebSpan = 367 * 24 * time.Hour

// parseInstant reads an ISO 8601 date (yyyy-mm-dd, a utc day) or an RFC
// 3339 timestamp with a zone. a date as the end of a range means the end
// of that day, so "from 2026-11-01 to 2026-11-07" covers seven whole days.
func parseInstant(name, v string, end bool) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		if end {
			return t.Add(24*time.Hour - time.Millisecond), nil
		}
		return t, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, invalid("%s must be an ISO 8601 date such as 2026-11-01 or a timestamp with a zone such as 2026-11-01T09:00:00Z", name)
	}
	return t.UTC(), nil
}

// webRange resolves from and to of a web tool: both optional, the default
// the 7 days ending now; to may not lie more than a day ahead, from not
// before 2000, and the span is at most 366 days.
func webRange(from, to string, now time.Time) (time.Time, time.Time, error) {
	end := now
	var err error
	if to != "" {
		if end, err = parseInstant("to", to, true); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	start := end.Add(-7 * 24 * time.Hour)
	if from != "" {
		if start, err = parseInstant("from", from, false); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	switch {
	case start.Before(earliest):
		return time.Time{}, time.Time{}, invalid("from must not be before 2000-01-01")
	case !end.After(start):
		return time.Time{}, time.Time{}, invalid("to must be after from")
	case end.After(now.Add(24 * time.Hour)):
		return time.Time{}, time.Time{}, invalid("to must not be more than a day in the future")
	case end.Sub(start) > maxWebSpan:
		return time.Time{}, time.Time{}, invalid("the range may cover at most 366 days")
	}
	return start, end, nil
}

// rangeValues are umami's startAt and endAt, unix milliseconds.
func rangeValues(start, end time.Time) url.Values {
	v := url.Values{}
	v.Set("startAt", strconv.FormatInt(start.UnixMilli(), 10))
	v.Set("endAt", strconv.FormatInt(end.UnixMilli(), 10))
	return v
}

type rangeOut struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func rangeJSON(start, end time.Time) rangeOut {
	return rangeOut{From: start.UTC().Format(time.RFC3339Nano), To: end.UTC().Format(time.RFC3339Nano)}
}

// checkUnit refuses a time series bucket too fine for the range, which
// umami would otherwise answer with thousands of points.
func checkUnit(unit string, start, end time.Time) error {
	span := end.Sub(start)
	switch unit {
	case "minute":
		if span > 24*time.Hour+time.Minute {
			return invalid("unit minute is for ranges of at most one day; use hour or day")
		}
	case "hour":
		if span > 31*24*time.Hour+time.Hour {
			return invalid("unit hour is for ranges of at most 31 days; use day or month")
		}
	case "day", "month", "year":
	default:
		return invalid("unit must be minute, hour, day, month or year")
	}
	return nil
}

// checkTimezone accepts an iana zone name.
func checkTimezone(tz string) (string, error) {
	if tz == "" {
		return "UTC", nil
	}
	if len(tz) > 64 {
		return "", invalid("timezone must be an IANA zone name such as Europe/Berlin")
	}
	if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
		return "", invalid("timezone must be an IANA zone name such as Europe/Berlin")
	}
	return tz, nil
}

// text checks a free text value: valid utf-8, no control characters, at
// most max bytes.
func text(name, v string, max int) error {
	if !utf8.ValidString(v) || len(v) > max {
		return invalid("%s must be valid text of at most %d bytes", name, max)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return invalid("%s must not contain control characters", name)
		}
	}
	return nil
}

// parseUUID reads a lowercase uuid; anything else is not an id of ours.
func parseUUID(name, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil || id.String() != v {
		return uuid.Nil, invalid("%s must be a lowercase uuid such as 0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10", name)
	}
	return id, nil
}

// webFilters are umami's filters (src/lib/request.ts getQueryFilters) a
// tool may pass: exact match, or a prefix operator. distinct ids,
// segments and cohorts are left out: the tracker sends no distinct id, and
// segments are umami ui objects that no project defines here.
type webFilters struct {
	Path        string `json:"path,omitempty" jsonschema:"page path, for example /pricing"`
	Referrer    string `json:"referrer,omitempty" jsonschema:"referrer domain, for example github.com"`
	Title       string `json:"title,omitempty" jsonschema:"page title"`
	Query       string `json:"query,omitempty" jsonschema:"url query string (kept only when the site collects UTM parameters)"`
	Hostname    string `json:"hostname,omitempty" jsonschema:"hostname the page was served from"`
	Event       string `json:"event,omitempty" jsonschema:"custom event name"`
	Tag         string `json:"tag,omitempty" jsonschema:"tracker tag"`
	Browser     string `json:"browser,omitempty" jsonschema:"browser, for example chrome"`
	OS          string `json:"os,omitempty" jsonschema:"operating system, for example Mac OS"`
	Device      string `json:"device,omitempty" jsonschema:"device type: desktop, laptop, tablet or mobile"`
	Country     string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code, for example DE"`
	Region      string `json:"region,omitempty" jsonschema:"region code, for example DE-BE"`
	City        string `json:"city,omitempty" jsonschema:"city name"`
	Language    string `json:"language,omitempty" jsonschema:"browser language, for example en-US"`
	UTMSource   string `json:"utm_source,omitempty" jsonschema:"utm_source (only when the site collects UTM parameters)"`
	UTMMedium   string `json:"utm_medium,omitempty" jsonschema:"utm_medium"`
	UTMCampaign string `json:"utm_campaign,omitempty" jsonschema:"utm_campaign"`
	UTMContent  string `json:"utm_content,omitempty" jsonschema:"utm_content"`
	UTMTerm     string `json:"utm_term,omitempty" jsonschema:"utm_term"`
	Match       string `json:"match,omitempty" jsonschema:"all (default): every filter must match; any: one is enough"`
}

const filterHelp = `Optional filters; only give the keys you filter on. A value is an exact match, ` +
	`several exact values are comma separated ("chrome,firefox"), and a prefix selects another comparison: ` +
	`"neq." not equal, "c." contains, "dnc." does not contain, "re." regular expression, "nre." not matching.`

// values adds the filters to q under umami's names.
func (f *webFilters) values(q url.Values, allowEvent bool) error {
	if f == nil {
		return nil
	}
	pairs := []struct{ name, key, v string }{
		{"path", "path", f.Path}, {"referrer", "referrer", f.Referrer}, {"title", "title", f.Title},
		{"query", "query", f.Query}, {"hostname", "hostname", f.Hostname}, {"event", "event", f.Event},
		{"tag", "tag", f.Tag}, {"browser", "browser", f.Browser}, {"os", "os", f.OS},
		{"device", "device", f.Device}, {"country", "country", f.Country}, {"region", "region", f.Region},
		{"city", "city", f.City}, {"language", "language", f.Language},
		{"utm_source", "utmSource", f.UTMSource}, {"utm_medium", "utmMedium", f.UTMMedium},
		{"utm_campaign", "utmCampaign", f.UTMCampaign}, {"utm_content", "utmContent", f.UTMContent},
		{"utm_term", "utmTerm", f.UTMTerm},
	}
	for _, p := range pairs {
		if p.v == "" {
			continue
		}
		if p.name == "event" && !allowEvent {
			// page view and session queries never match an event name, so
			// the filter would only produce zeros (umami issue 3886)
			return invalid("filters.event applies to the event tools and to get_web_metrics with type event only")
		}
		if err := text("filters."+p.name, p.v, 500); err != nil {
			return err
		}
		q.Set(p.key, p.v)
	}
	switch f.Match {
	case "":
	case "all", "any":
		q.Set("match", f.Match)
	default:
		return invalid("filters.match must be all or any")
	}
	return nil
}

// patches for the inferred schemas

func enum(props map[string]*jsonschema.Schema, name string, values ...string) {
	p := props[name]
	if p == nil {
		panic("mcpserver: no property " + name)
	}
	setEnum(p, values...)
}

func setEnum(p *jsonschema.Schema, values ...string) {
	p.Enum = make([]any, len(values))
	for i, v := range values {
		p.Enum[i] = v
	}
}

func intRange(props map[string]*jsonschema.Schema, name string, lo, hi int) {
	p := props[name]
	if p == nil {
		panic("mcpserver: no property " + name)
	}
	l, h := float64(lo), float64(hi)
	p.Minimum, p.Maximum = &l, &h
}

func uuidFormat(props map[string]*jsonschema.Schema, names ...string) {
	for _, n := range names {
		p := props[n]
		if p == nil {
			panic("mcpserver: no property " + n)
		}
		setUUID(p)
	}
}

func setUUID(p *jsonschema.Schema) {
	p.Format = "uuid"
	p.Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
}

func maxLen(props map[string]*jsonschema.Schema, name string, n int) {
	p := props[name]
	if p == nil {
		panic("mcpserver: no property " + name)
	}
	p.MaxLength = &n
}

// checkInt refuses a value outside [lo, hi]; the schema says the same, the
// check keeps the tool correct without it.
func checkInt(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return invalid("%s must be between %d and %d", name, lo, hi)
	}
	return nil
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// dayOf reads a utc day for the operator views, which work on whole days
// (contract 8.1): a date, or the utc day of a timestamp.
func dayOf(name, v string) (string, error) {
	if v == "" {
		return "", nil
	}
	t, err := parseInstant(name, v, false)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(time.DateOnly), nil
}

func joinIDs(name string, ids []string, lo, hi int) (string, error) {
	if len(ids) < lo || len(ids) > hi {
		return "", invalid("%s must hold %d to %d ids", name, lo, hi)
	}
	seen := map[string]bool{}
	for i, v := range ids {
		if _, err := parseUUID(fmt.Sprintf("%s[%d]", name, i), v); err != nil {
			return "", err
		}
		if seen[v] {
			return "", invalid("%s repeats %s", name, v)
		}
		seen[v] = true
	}
	return strings.Join(ids, ","), nil
}
