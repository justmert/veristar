package histo_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/histo"
)

var conn *pdb.DB

// the cross-check runs against the real api.histogram_quantile in a migrated
// platform database, called as cp_tenant like the dashboard will
var tenantConn *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = pg.Terminate(context.Background()) }()
	if err := setup(ctx, pg); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	defer conn.Close()
	return m.Run()
}

func setup(ctx context.Context, pg *dbtest.Postgres) error {
	cfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if _, err := pdb.Migrate(ctx, cfg, db.Migrations(), slog.New(slog.DiscardHandler)); err != nil {
		return err
	}
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		return err
	}
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPTenant)
	if err != nil {
		return err
	}
	if conn, err = pdb.Open(ctx, pcfg, "histo-test"); err != nil {
		return err
	}
	tenantConn, err = conn.Pool(pdb.RoleCPTenant)
	return err
}

// sqlQuantile returns the sql result; ok is false for null
func sqlQuantile(t *testing.T, q float64, counts []int64) (v float64, ok bool, err error) {
	t.Helper()
	var p *float64
	err = tenantConn.QueryRow(context.Background(), `select api.histogram_quantile($1, $2)`, q, counts).Scan(&p)
	if err != nil || p == nil {
		return 0, false, err
	}
	return *p, true, nil
}

// randomCounts draws histograms of several shapes: sparse, dense, a single
// bucket, heavy in the open bucket, and huge counts near 2^53 where float64
// conversion starts to round
func randomCounts(r *rand.Rand) []int64 {
	c := make([]int64, histo.Buckets)
	switch r.IntN(5) {
	case 0:
		for range 1 + r.IntN(3) {
			c[r.IntN(histo.Buckets)] += 1 + r.Int64N(5)
		}
	case 1:
		for i := range c {
			c[i] = r.Int64N(1000)
		}
	case 2:
		c[r.IntN(histo.Buckets)] = 1 + r.Int64N(1<<20)
	case 3:
		for i := range c {
			c[i] = r.Int64N(10)
		}
		c[histo.Open] = r.Int64N(1000)
	default:
		for i := range c {
			if r.IntN(2) == 0 {
				c[i] = r.Int64N(1 << 49)
			}
		}
	}
	return c
}

func TestQuantileMatchesSQL(t *testing.T) {
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	qs := []float64{0, 0.5, 0.9, 0.95, 0.99, 0.999, 1}
	const n = 3000
	var open, empty int
	for i := range n {
		counts := randomCounts(r)
		if i%250 == 0 {
			counts = make([]int64, histo.Buckets)
		}
		q := qs[i%len(qs)]
		if i%3 == 0 {
			q = r.Float64()
		}
		want, err := histo.Quantile(q, counts)
		got, ok, sqlErr := sqlQuantile(t, q, counts)
		require.NoError(t, sqlErr, "q %v counts %v", q, counts)
		if errors.Is(err, histo.ErrEmpty) {
			empty++
			require.False(t, ok, "sql returns null for an empty histogram: %v", counts)
			continue
		}
		require.NoError(t, err)
		require.True(t, ok)
		if histo.IsOpen(want) {
			open++
		}
		// bit for bit: the two implementations do the same float64 steps
		require.Equal(t, math.Float64bits(want), math.Float64bits(got),
			"q %v counts %v: go %v sql %v", q, counts, want, got)
	}
	t.Logf("%d histograms, %d with an open-bucket quantile, %d empty", n, open, empty)
	require.Positive(t, open, "the draws must reach the open bucket")
	require.Positive(t, empty, "the draws must include empty histograms")
}

func TestQuantileRefusalsMatchSQL(t *testing.T) {
	ok := make([]int64, histo.Buckets)
	ok[3] = 1
	neg := make([]int64, histo.Buckets)
	neg[2] = -1
	over := make([]int64, histo.Buckets)
	over[0], over[1] = math.MaxInt64, 1
	cases := []struct {
		q      float64
		counts []int64
		state  string
	}{
		{-0.5, ok, "22023"},
		{1.5, ok, "22023"},
		{math.NaN(), ok, "22023"},
		{0.5, []int64{1, 2}, "2202E"},
		{0.5, neg, "22023"},
		{0.5, over, "22003"},
	}
	for _, tc := range cases {
		_, goErr := histo.Quantile(tc.q, tc.counts)
		require.Error(t, goErr, "go refuses q %v counts %v", tc.q, tc.counts)
		_, _, err := sqlQuantile(t, tc.q, tc.counts)
		var pe *pgconn.PgError
		require.ErrorAs(t, err, &pe, "sql refuses q %v counts %v", tc.q, tc.counts)
		require.Equal(t, tc.state, pe.Code, pe.Message)
	}
}

// the sql merge of bucket arrays agrees with adding the counts in go, and
// refuses arrays it cannot add
func TestAddBucketsSQL(t *testing.T) {
	ctx := context.Background()
	r := rand.New(rand.NewPCG(1, 2))
	var parts [][]int64
	var want histo.Counts
	for range 20 {
		c := randomCounts(r)
		for i := range c {
			c[i] %= 1 << 40
			want[i] += c[i]
		}
		parts = append(parts, c)
	}
	// each row of the 2-d parameter becomes one 1-d array for the aggregate
	var got []int64
	require.NoError(t, tenantConn.QueryRow(ctx, `
		select api.sum_buckets(array(select unnest(($1::bigint[][])[i:i][1:15])))
		from generate_subscripts($1::bigint[][], 1) i`, parts).Scan(&got))
	require.Equal(t, want.Slice(), got)

	_, err := tenantConn.Exec(ctx, `select api.add_buckets('{1,2}'::bigint[], '{1}'::bigint[])`)
	require.ErrorContains(t, err, "different shape")
	_, err = tenantConn.Exec(ctx, `select api.add_buckets('{1,null}'::bigint[], '{1,2}'::bigint[])`)
	require.ErrorContains(t, err, "null bucket count")
	_, err = tenantConn.Exec(ctx, `select api.add_buckets(array[9223372036854775807]::bigint[], '{1}'::bigint[])`)
	require.ErrorContains(t, err, "out of range")
}
