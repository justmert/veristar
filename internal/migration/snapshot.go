// Package migration compares a source and a target environment after a data
// migration (the design): row counts of every table in both databases,
// per-tenant daily web and api aggregates, and umami's own /stats for a
// sample of sites and days. a snapshot is read from each side, then the two
// are compared key by key, so a difference names exactly what moved wrong.
package migration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/umami"
)

// Excluded lists tables whose rows are runtime state of the environment that
// wrote them, not data, with the reason. they are counted and shown, never
// compared.
var Excluded = map[string]string{
	// fencing revisions come from the source's nats lock bucket; the target's
	// jobs leader (reconcile after import) writes its own revision here
	"platform.cp.leader_fence": "leader lock fencing revisions of the environment's own nats cluster",
}

// WebKey is one tenant, utc day and band of agg.web_daily.
type WebKey struct {
	Tenant string
	Day    string
	Band   string
}

// WebTotals sums agg.web_daily over a tenant's sites for one day and band.
type WebTotals struct {
	Sites     int64
	Pageviews int64
	Visitors  int64
	Visits    int64
	Bounces   int64
	Totaltime int64
}

// DimKey is one tenant, utc day, band and dimension of agg.web_daily_dim.
type DimKey struct {
	Tenant    string
	Day       string
	Band      string
	Dimension string
}

// DimTotals sums agg.web_daily_dim values and counts its rows.
type DimTotals struct {
	Rows  int64
	Value int64
}

// APIKey is one tenant, utc day, source and resolution of the api metrics.
type APIKey struct {
	Table  string
	Tenant string
	Day    string
	Source string
}

// APITotals sums an api metrics table for one tenant, day and source. sum is
// accumulated in primary key order on both sides, so equal rows give a
// bit-equal float.
type APITotals struct {
	Rows   int64
	Count  int64
	Sum    float64
	Counts [15]int64
	Lossy  int64
}

// StatsKey is one umami website and utc day of the /stats sample.
type StatsKey struct {
	Website string
	Day     string
}

// Snapshot is everything compared for one environment.
type Snapshot struct {
	// Tables maps "<database>.<schema>.<table>" to its row count
	Tables map[string]int64
	Web    map[WebKey]WebTotals
	Dims   map[DimKey]DimTotals
	API    map[APIKey]APITotals
	Stats  map[StatsKey]umami.StatsValues
}

// Side is one environment: superuser connections to both databases, so
// row-level security does not hide rows, and the umami admin.
type Side struct {
	Platform *pgx.Conn
	Umami    *pgx.Conn
	Admin    StatsReader
}

// StatsReader is the part of the umami admin client the sample uses.
type StatsReader interface {
	Stats(ctx context.Context, websiteID string, r umami.Range) (umami.Stats, error)
}

// Sample picks which websites and days /stats is compared for. it is chosen
// on the source, then read on both sides with the same keys.
type Sample struct {
	Sites int
	Days  int
}

// readOnly runs fn in a repeatable read, read only transaction, so every
// count of one database comes from the same snapshot.
func readOnly(ctx context.Context, c *pgx.Conn, fn func(pgx.Tx) error) error {
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// countTables counts the rows of every ordinary table (partitions are
// ordinary tables, partitioned parents are not, so no row counts twice).
func countTables(ctx context.Context, tx pgx.Tx, database string, out map[string]int64) error {
	rows, err := tx.Query(ctx, `
		select n.nspname, c.relname from pg_class c
		join pg_namespace n on n.oid = c.relnamespace
		where c.relkind = 'r'
		  and n.nspname not in ('pg_catalog', 'information_schema')
		  and n.nspname not like 'pg\_toast%' and n.nspname not like 'pg\_temp%'
		order by 1, 2`)
	if err != nil {
		return fmt.Errorf("list tables of %s: %w", database, err)
	}
	type tbl struct{ schema, name string }
	var tables []tbl
	for rows.Next() {
		var t tbl
		if err := rows.Scan(&t.schema, &t.name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range tables {
		var n int64
		q := "select count(*) from " + pgx.Identifier{t.schema, t.name}.Sanitize()
		if err := tx.QueryRow(ctx, q).Scan(&n); err != nil {
			return fmt.Errorf("count %s.%s.%s: %w", database, t.schema, t.name, err)
		}
		out[database+"."+t.schema+"."+t.name] = n
	}
	return nil
}

func readWeb(ctx context.Context, tx pgx.Tx, s *Snapshot) error {
	rows, err := tx.Query(ctx, `
		select tenant_id::text, day::text, band, count(*), sum(pageviews), sum(visitors),
		       sum(visits), sum(bounces), sum(totaltime)
		from agg.web_daily group by 1, 2, 3`)
	if err != nil {
		return fmt.Errorf("read agg.web_daily: %w", err)
	}
	for rows.Next() {
		var k WebKey
		var v WebTotals
		if err := rows.Scan(&k.Tenant, &k.Day, &k.Band, &v.Sites, &v.Pageviews, &v.Visitors, &v.Visits, &v.Bounces, &v.Totaltime); err != nil {
			rows.Close()
			return err
		}
		s.Web[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `
		select tenant_id::text, day::text, band, dimension, count(*), sum(value)
		from agg.web_daily_dim group by 1, 2, 3, 4`)
	if err != nil {
		return fmt.Errorf("read agg.web_daily_dim: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k DimKey
		var v DimTotals
		if err := rows.Scan(&k.Tenant, &k.Day, &k.Band, &k.Dimension, &v.Rows, &v.Value); err != nil {
			return err
		}
		s.Dims[k] = v
	}
	return rows.Err()
}

// apiTables are the stored resolutions; minute rows are covered by the row
// counts and roll up into these
var apiTables = []string{"metrics_hourly", "metrics_daily"}

func readAPI(ctx context.Context, tx pgx.Tx, s *Snapshot) error {
	for _, table := range apiTables {
		q := `select tenant_id::text, to_char(bucket_start at time zone 'UTC', 'YYYY-MM-DD'), source,
		             counts, sum, count, lossy
		      from ` + pgx.Identifier{"api", table}.Sanitize() + `
		      order by tenant_id, bucket_start, service, method, route, status_code, source`
		rows, err := tx.Query(ctx, q)
		if err != nil {
			return fmt.Errorf("read api.%s: %w", table, err)
		}
		for rows.Next() {
			k := APIKey{Table: table}
			var counts []int64
			var sum float64
			var count int64
			var lossy bool
			if err := rows.Scan(&k.Tenant, &k.Day, &k.Source, &counts, &sum, &count, &lossy); err != nil {
				rows.Close()
				return err
			}
			if len(counts) != 15 {
				rows.Close()
				return fmt.Errorf("api.%s: a row of tenant %s has %d buckets, not 15", table, k.Tenant, len(counts))
			}
			v := s.API[k]
			v.Rows++
			v.Count += count
			v.Sum += sum
			for i, c := range counts {
				v.Counts[i] += c
			}
			if lossy {
				v.Lossy++
			}
			s.API[k] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// Read takes the snapshot of one side. keys is the /stats sample chosen on
// the source with PickStats; nil reads no /stats.
func Read(ctx context.Context, side Side, keys []StatsKey) (*Snapshot, error) {
	s := &Snapshot{
		Tables: map[string]int64{},
		Web:    map[WebKey]WebTotals{},
		Dims:   map[DimKey]DimTotals{},
		API:    map[APIKey]APITotals{},
		Stats:  map[StatsKey]umami.StatsValues{},
	}
	err := readOnly(ctx, side.Platform, func(tx pgx.Tx) error {
		return errors.Join(countTables(ctx, tx, "platform", s.Tables), readWeb(ctx, tx, s), readAPI(ctx, tx, s))
	})
	if err != nil {
		return nil, err
	}
	if err := readOnly(ctx, side.Umami, func(tx pgx.Tx) error {
		return countTables(ctx, tx, "umami", s.Tables)
	}); err != nil {
		return nil, err
	}
	for _, k := range keys {
		day, err := time.Parse(time.DateOnly, k.Day)
		if err != nil {
			return nil, fmt.Errorf("stats sample day %q: %w", k.Day, err)
		}
		st, err := side.Admin.Stats(ctx, k.Website, umami.Range{Start: day, End: day.Add(24*time.Hour - time.Millisecond)})
		if err != nil {
			return nil, fmt.Errorf("umami stats of %s on %s: %w", k.Website, k.Day, err)
		}
		s.Stats[k] = st.StatsValues
	}
	return s, nil
}

// PickStats chooses the /stats sample on the source: up to sample.Sites
// sites that are not removed, in a fixed pseudo-random order (md5 of the id),
// each with its primary and quarantine website, and for each website the
// sample.Days most recent utc days on which umami stored an event.
func PickStats(ctx context.Context, side Side, sample Sample) ([]StatsKey, error) {
	if sample.Sites < 1 || sample.Days < 1 {
		return nil, errors.New("migration: the stats sample needs at least one site and one day")
	}
	var websites []string
	err := readOnly(ctx, side.Platform, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select id::text, coalesce(quarantine_website_id::text, '') from cp.sites
			where status <> 'removed' order by md5(id::text) limit $1`, sample.Sites)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, q string
			if err := rows.Scan(&id, &q); err != nil {
				return err
			}
			websites = append(websites, id)
			if q != "" {
				websites = append(websites, q)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("pick sample sites: %w", err)
	}
	var keys []StatsKey
	err = readOnly(ctx, side.Umami, func(tx pgx.Tx) error {
		for _, w := range websites {
			rows, err := tx.Query(ctx, `
				select distinct (created_at at time zone 'UTC')::date::text d from website_event
				where website_id = $1::uuid order by d desc limit $2`, w, sample.Days)
			if err != nil {
				return err
			}
			for rows.Next() {
				var d string
				if err := rows.Scan(&d); err != nil {
					rows.Close()
					return err
				}
				keys = append(keys, StatsKey{Website: w, Day: d})
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pick sample days: %w", err)
	}
	return keys, nil
}

// Difference is one value that is not equal on both sides.
type Difference struct {
	Kind   string
	Key    string
	Source string
	Target string
}

func (d Difference) String() string {
	return fmt.Sprintf("%s %s: source %s, target %s", d.Kind, d.Key, d.Source, d.Target)
}

// Report is the outcome of Compare.
type Report struct {
	Differences []Difference
	// counts of compared keys per kind, so an empty comparison is visible
	Compared map[string]int
	// excluded tables with their row counts on each side
	Excluded []string
}

// compareMaps compares two maps with string keys and formats both values.
func compareMaps[K comparable, V comparable](kind string, src, dst map[K]V, key func(K) string, r *Report) {
	seen := map[K]bool{}
	for k, sv := range src {
		seen[k] = true
		dv, ok := dst[k]
		switch {
		case !ok:
			r.Differences = append(r.Differences, Difference{kind, key(k), fmt.Sprintf("%+v", sv), "absent"})
		case sv != dv:
			r.Differences = append(r.Differences, Difference{kind, key(k), fmt.Sprintf("%+v", sv), fmt.Sprintf("%+v", dv)})
		}
	}
	for k, dv := range dst {
		if !seen[k] {
			r.Differences = append(r.Differences, Difference{kind, key(k), "absent", fmt.Sprintf("%+v", dv)})
		}
	}
	r.Compared[kind] = len(seen)
}

// Compare reports every difference between the source and target snapshots.
func Compare(src, dst *Snapshot) Report {
	r := Report{Compared: map[string]int{}}
	srcT, dstT := map[string]int64{}, map[string]int64{}
	for k, v := range src.Tables {
		if _, ok := Excluded[k]; ok {
			target := "absent"
			if n, ok := dst.Tables[k]; ok {
				target = strconv.FormatInt(n, 10)
			}
			r.Excluded = append(r.Excluded, k+" source "+strconv.FormatInt(v, 10)+" target "+target+" ("+Excluded[k]+")")
			continue
		}
		srcT[k] = v
	}
	for k, v := range dst.Tables {
		if _, ok := Excluded[k]; !ok {
			dstT[k] = v
		}
	}
	compareMaps("rows", srcT, dstT, func(k string) string { return k }, &r)
	compareMaps("web", src.Web, dst.Web, func(k WebKey) string { return k.Tenant + " " + k.Day + " " + k.Band }, &r)
	compareMaps("web-dim", src.Dims, dst.Dims, func(k DimKey) string {
		return k.Tenant + " " + k.Day + " " + k.Band + " " + k.Dimension
	}, &r)
	compareMaps("api", src.API, dst.API, func(k APIKey) string {
		return k.Table + " " + k.Tenant + " " + k.Day + " " + k.Source
	}, &r)
	compareMaps("umami-stats", src.Stats, dst.Stats, func(k StatsKey) string { return k.Website + " " + k.Day }, &r)
	slices.SortFunc(r.Differences, func(a, b Difference) int {
		return strings.Compare(a.Kind+a.Key, b.Kind+b.Key)
	})
	slices.Sort(r.Excluded)
	return r
}
