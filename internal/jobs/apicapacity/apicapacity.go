// Package apicapacity measures what api metric rows cost on disk and
// extrapolates it to the retention horizons, for docs/reference/capacity.md.
// it reads real rows: the bytes per row come from pg_total_relation_size of
// every partition (heap, toast and indexes together) divided by the live
// row count, so the number includes the primary key index the upserts need.
package apicapacity

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
)

// Tables are the metric tables measured, in the order reports list them.
var Tables = []string{"api.metrics_minute", "api.metrics_hourly", "api.metrics_daily"}

// Table is the measurement of one table.
type Table struct {
	Name  string
	Rows  int64
	Bytes int64
	// Partitions is the number of leaf partitions holding the bytes; 1 for
	// an unpartitioned table.
	Partitions int
	// EmptyPartitions never held a row (their heap is zero bytes); their
	// index pages are left out of Bytes, because partitions created ahead
	// cost a fixed few pages each rather than anything per row
	EmptyPartitions int
	EmptyBytes      int64
}

// BytesPerRow is Bytes over Rows, or NaN for an empty table, so a report
// can never print a made-up figure for a table that held nothing.
func (t Table) BytesPerRow() float64 {
	if t.Rows == 0 {
		return math.NaN()
	}
	return float64(t.Bytes) / float64(t.Rows)
}

// Measure counts the rows and the on-disk bytes of each table in Tables.
// run it after a vacuum (or on a quiet table) when the number is meant for
// the capacity document: dead tuples of upserts count as bytes until then.
func Measure(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	out := make([]Table, 0, len(Tables))
	for _, name := range Tables {
		var t Table
		t.Name = name
		// pg_partition_tree lists nothing for a plain table, so a table
		// without partitions is its own single leaf
		err := tx.QueryRow(ctx, `
with leaves as (
  select relid from pg_partition_tree($1::regclass) where isleaf
  union all
  select $1::regclass where (select relkind from pg_class where oid = $1::regclass) = 'r'
)
select count(*) filter (where pg_relation_size(relid) > 0)::int,
       coalesce(sum(pg_total_relation_size(relid)) filter (where pg_relation_size(relid) > 0), 0)::bigint,
       count(*) filter (where pg_relation_size(relid) = 0)::int,
       coalesce(sum(pg_total_relation_size(relid)) filter (where pg_relation_size(relid) = 0), 0)::bigint
from leaves`, name).Scan(&t.Partitions, &t.Bytes, &t.EmptyPartitions, &t.EmptyBytes)
		if err != nil {
			return nil, fmt.Errorf("apicapacity: size of %s: %w", name, err)
		}
		if err := tx.QueryRow(ctx, `select count(*) from `+pgx.Identifier{"api", name[len("api."):]}.Sanitize()).Scan(&t.Rows); err != nil {
			return nil, fmt.Errorf("apicapacity: rows of %s: %w", name, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// Load describes a steady state to extrapolate to.
type Load struct {
	// MinuteRowsPerDay is how many minute rows one day writes: active series
	// times the minutes each one reports in.
	MinuteRowsPerDay float64
	// HourlyRowsPerDay and DailyRowsPerDay are the rows the rollup writes
	// per day at the same load.
	HourlyRowsPerDay float64
	DailyRowsPerDay  float64
}

// Projection is the extrapolated size at the retention horizons.
type Projection struct {
	// MinuteBytes is api.metrics_minute holding MinuteDays of data.
	MinuteBytes float64
	// HourlyBytes is api.metrics_hourly holding HourlyMonths of data.
	HourlyBytes float64
	// DailyBytesPerYear is what api.metrics_daily grows by in a year; it is
	// never expired.
	DailyBytesPerYear float64
}

// daysPerMonth is the mean gregorian month, so 36 months is 1095.75 days.
const daysPerMonth = 365.25 / 12

// Project extrapolates measured bytes per row to the horizons.
func Project(measured []Table, load Load, minuteDays, hourlyMonths int) (Projection, error) {
	per := map[string]float64{}
	for _, t := range measured {
		per[t.Name] = t.BytesPerRow()
	}
	for _, name := range Tables {
		v, ok := per[name]
		if !ok || math.IsNaN(v) {
			return Projection{}, fmt.Errorf("apicapacity: %s has no measured rows to extrapolate from", name)
		}
	}
	if minuteDays <= 0 || hourlyMonths <= 0 || load.MinuteRowsPerDay <= 0 {
		return Projection{}, errors.New("apicapacity: horizons and the minute row rate must be positive")
	}
	return Projection{
		MinuteBytes:       per["api.metrics_minute"] * load.MinuteRowsPerDay * float64(minuteDays),
		HourlyBytes:       per["api.metrics_hourly"] * load.HourlyRowsPerDay * float64(hourlyMonths) * daysPerMonth,
		DailyBytesPerYear: per["api.metrics_daily"] * load.DailyRowsPerDay * 365.25,
	}, nil
}
