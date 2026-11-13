package apimetrics

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/histo"
)

// OpenLabel is how a percentile in the open bucket above 10 s is reported
//: never as a number, because the bucket has no upper bound to
// interpolate towards.
const OpenLabel = ">10 s"

// Percentile is a latency percentile in seconds, the open bucket, or
// nothing when there were no requests (contract 7.4).
type Percentile struct {
	Valid   bool
	Open    bool
	Seconds float64
}

// MarshalJSON writes a number, ">10 s" or null.
func (p Percentile) MarshalJSON() ([]byte, error) {
	switch {
	case !p.Valid:
		return []byte("null"), nil
	case p.Open:
		return json.Marshal(OpenLabel)
	default:
		return json.Marshal(p.Seconds)
	}
}

// UnmarshalJSON reads the three forms back, for tests and go clients.
func (p *Percentile) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*p = Percentile{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s != OpenLabel {
			return fmt.Errorf("apimetrics: percentile string %q", s)
		}
		*p = Percentile{Valid: true, Open: true}
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*p = Percentile{Valid: true, Seconds: f}
	return nil
}

// percentile converts the value of api.histogram_quantile. sqlc types the
// nullable result as any; pgx gives float64 or nil for a float8 column, and
// anything else means the query changed under this code.
func percentile(v any) (Percentile, error) {
	switch x := v.(type) {
	case nil:
		return Percentile{}, nil
	case float64:
		if histo.IsOpen(x) {
			return Percentile{Valid: true, Open: true}, nil
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
			return Percentile{}, fmt.Errorf("apimetrics: percentile %v from the database", x)
		}
		return Percentile{Valid: true, Seconds: x}, nil
	default:
		return Percentile{}, fmt.Errorf("apimetrics: percentile of type %T from the database", v)
	}
}

func percentiles(p50, p95, p99 any) (a, b, c Percentile, err error) {
	if a, err = percentile(p50); err != nil {
		return
	}
	if b, err = percentile(p95); err != nil {
		return
	}
	c, err = percentile(p99)
	return
}

// rate is errors over requests, null without requests
func rate(errs, requests int64) *float64 {
	if requests == 0 {
		return nil
	}
	v := float64(errs) / float64(requests)
	return &v
}

// RangeJSON is the range object of contract 7.2.
type RangeJSON struct {
	Preset         string          `json:"preset"`
	From           httpx.Timestamp `json:"from"`
	To             httpx.Timestamp `json:"to"`
	Resolution     Resolution      `json:"resolution"`
	BucketSeconds  int             `json:"bucket_seconds"`
	ResolutionNote *string         `json:"resolution_note"`
}

func rangeJSON(r Range) RangeJSON {
	out := RangeJSON{
		Preset: r.Preset, From: httpx.Timestamp(r.From), To: httpx.Timestamp(r.To),
		Resolution: r.Resolution, BucketSeconds: r.Resolution.BucketSeconds(),
	}
	if r.Note != "" {
		n := r.Note
		out.ResolutionNote = &n
	}
	return out
}

// Summary is the summary object of contract 7.4.
type Summary struct {
	Requests  int64      `json:"requests"`
	Errors4xx int64      `json:"errors_4xx"`
	Errors5xx int64      `json:"errors_5xx"`
	Rate4xx   *float64   `json:"rate_4xx"`
	Rate5xx   *float64   `json:"rate_5xx"`
	P50       Percentile `json:"p50"`
	P95       Percentile `json:"p95"`
	P99       Percentile `json:"p99"`
	Lossy     bool       `json:"lossy"`
}

func summary(requests, e4, e5 int64, lossy bool, p50, p95, p99 any) (Summary, error) {
	a, b, c, err := percentiles(p50, p95, p99)
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		Requests: requests, Errors4xx: e4, Errors5xx: e5,
		Rate4xx: rate(e4, requests), Rate5xx: rate(e5, requests),
		P50: a, P95: b, P99: c, Lossy: lossy,
	}, nil
}

// Period is one range of the overview with its sources and summary.
type Period struct {
	Range   RangeJSON `json:"range"`
	Sources []Source  `json:"sources"`
	Summary Summary   `json:"summary"`
}

// Overview is the answer of contract 7.5.
type Overview struct {
	Current  Period `json:"current"`
	Previous Period `json:"previous"`
}

// Point is one time bucket of contract 7.6.
type Point struct {
	At httpx.Timestamp `json:"at"`
	Summary
}

// Timeseries is the answer of contract 7.6.
type Timeseries struct {
	Range   RangeJSON `json:"range"`
	Sources []Source  `json:"sources"`
	Points  []Point   `json:"points"`
}

// RouteItem is one row of contract 7.7.
type RouteItem struct {
	Service string `json:"service"`
	Method  string `json:"method"`
	Route   string `json:"route"`
	Source  string `json:"source"`
	Summary
}

// RouteList is the answer of contract 7.7.
type RouteList struct {
	Range      RangeJSON   `json:"range"`
	Sources    []Source    `json:"sources"`
	Total      int64       `json:"total"`
	Items      []RouteItem `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

// ClassCount is one status class of contract 7.8.
type ClassCount struct {
	Class    string `json:"class"`
	Requests int64  `json:"requests"`
}

// CodeCount is one status code of contract 7.8.
type CodeCount struct {
	StatusCode int16  `json:"status_code"`
	Class      string `json:"class"`
	Requests   int64  `json:"requests"`
}

// Status is the answer of contract 7.8.
type Status struct {
	Range    RangeJSON    `json:"range"`
	Sources  []Source     `json:"sources"`
	Requests int64        `json:"requests"`
	Classes  []ClassCount `json:"classes"`
	Codes    []CodeCount  `json:"codes"`
}

// ServiceItem is one service of contract 7.9.
type ServiceItem struct {
	Source
	Requests int64 `json:"requests"`
	Lossy    bool  `json:"lossy"`
}

// Services is the answer of contract 7.9.
type Services struct {
	Range RangeJSON     `json:"range"`
	Items []ServiceItem `json:"items"`
}

// WarningTotal is one kind of contract 7.10.
type WarningTotal struct {
	Kind    string  `json:"kind"`
	Count   int64   `json:"count"`
	LastDay *string `json:"last_day"`
}

// WarningDay is one day and kind of contract 7.10.
type WarningDay struct {
	Day   string `json:"day"`
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// Warnings is the answer of contract 7.10.
type Warnings struct {
	Range  RangeJSON      `json:"range"`
	Totals []WarningTotal `json:"totals"`
	Days   []WarningDay   `json:"days"`
}

// classes in contract order; unknown is status_code 0
var classOrder = []string{"1xx", "2xx", "3xx", "4xx", "5xx", "unknown"}

func classOf(code int16) string {
	if code >= 100 && code <= 599 {
		return classOrder[code/100-1]
	}
	return "unknown"
}

// warning kinds in contract order (api.ingest_warnings check). coarse_buckets
// came with review and is last, so the earlier positions keep their
// meaning for existing clients.
var warningKinds = []string{"span_derived", "lossy_histogram", "cardinality_overflow", "secret_in_path", "coarse_buckets"}
