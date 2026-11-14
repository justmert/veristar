// Package sloreport turns the probe results the prober pushes to gatus into
// daily raw files (docs/slo/data/YYYY-MM-DD.json) and a monthly report
// (docs/slo/YYYY-MM.md).
//
// gatus 5.37.0 keeps a fixed number of raw results per endpoint and hourly
// uptime for only 30 days, which is shorter than a 31-day month, so the raw
// results are copied out once a day and the report is computed from those
// copies, never from gatus' own uptime numbers.
package sloreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IntervalSeconds is the prober period: every result stands for one 30 s
// interval.
const IntervalSeconds = 30

// the gatus keys of the four slo external endpoints the prober pushes to
// (group "slo"; gatus builds the key as group_name with spaces turned into
// dashes)
const (
	KeyIngestion     = "slo_ingestion"
	KeyDashboard     = "slo_dashboard"
	KeyFreshnessWeb  = "slo_freshness-web"
	KeyFreshnessAPI  = "slo_freshness-api"
	dayLayout        = "2006-01-02"
	monthLayout      = "2006-01"
	dataFileSuffix   = ".json"
	reportFileSuffix = ".md"
)

// Keys lists the slo endpoints in report order.
var Keys = []string{KeyIngestion, KeyDashboard, KeyFreshnessWeb, KeyFreshnessAPI}

// Result is one probe interval as stored in a daily file.
type Result struct {
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
	Errors    []string  `json:"errors,omitempty"`
}

// Day is the content of docs/slo/data/YYYY-MM-DD.json.
type Day struct {
	Day             string              `json:"day"`
	CollectedAt     time.Time           `json:"collected_at"`
	Source          string              `json:"source"`
	IntervalSeconds int                 `json:"interval_seconds"`
	Endpoints       map[string][]Result `json:"endpoints"`
	// Until marks a partial day: the results cover [day start, Until) only.
	// the scheduled collection never sets it; it exists so a report can be
	// made from a day that has not ended (the local verification).
	Until *time.Time `json:"until,omitempty"`
}

// DataPath is the file of one day under dir.
func DataPath(dir string, day time.Time) string {
	return filepath.Join(dir, day.UTC().Format(dayLayout)+dataFileSuffix)
}

// ParseDay reads a YYYY-MM-DD utc day.
func ParseDay(s string) (time.Time, error) {
	d, err := time.Parse(dayLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("day %q is not YYYY-MM-DD: %w", s, err)
	}
	return d, nil
}

// ParseMonth reads a YYYY-MM utc month.
func ParseMonth(s string) (time.Time, error) {
	m, err := time.Parse(monthLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("month %q is not YYYY-MM: %w", s, err)
	}
	return m, nil
}

// WriteDay writes d as indented json. the file is written to a temporary
// name first, so a crash never leaves a half-written day that a later
// report would read as complete.
func WriteDay(dir string, d Day) (string, error) {
	day, err := ParseDay(d.Day)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the data directory is committed to the repository and meant to be readable
		return "", err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	path := DataPath(dir, day)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil { //nolint:gosec // a committed data file, not a secret
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// ReadDay reads one daily file. a missing file is reported as
// os.ErrNotExist so the report can list the day as missing.
func ReadDay(dir string, day time.Time) (Day, error) {
	path := DataPath(dir, day)
	b, err := os.ReadFile(path) //nolint:gosec // path is built from the data directory and a parsed date
	if err != nil {
		return Day{}, err
	}
	var d Day
	if err := json.Unmarshal(b, &d); err != nil {
		return Day{}, fmt.Errorf("%s: %w", path, err)
	}
	if d.Day != day.Format(dayLayout) {
		return Day{}, fmt.Errorf("%s: holds day %q", path, d.Day)
	}
	if d.IntervalSeconds != IntervalSeconds {
		return Day{}, fmt.Errorf("%s: interval %d s, the report assumes %d s", path, d.IntervalSeconds, IntervalSeconds)
	}
	start := day
	end := day.AddDate(0, 0, 1)
	for k, rs := range d.Endpoints {
		for _, r := range rs {
			if r.Timestamp.Before(start) || !r.Timestamp.Before(end) {
				return Day{}, fmt.Errorf("%s: endpoint %s has a result at %s outside the day", path, k, r.Timestamp.Format(time.RFC3339))
			}
		}
	}
	return d, nil
}

// errNotEnded is returned when a day that has not ended is collected.
var errNotEnded = errors.New("the day has not ended yet")
