package db_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// api.sum_buckets_read (00049) sums histograms whose bucket totals pass
// bigint, where api.sum_buckets fails with 22003 and so fails every
// percentile of a long range
func TestSumBucketsReadPastBigint(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	// 4 rows of 2^61 in bucket 3 and 2^61 in bucket 9: each bucket sums to
	// 2^63, past bigint, and the total to 2^64
	const rows = `(select array[0,0,2305843009213693952,0,0,0,0,0,2305843009213693952,0,0,0,0,0,0]::bigint[] as counts
	               from generate_series(1, 4))`

	_, err := c.Exec(ctx, `select api.sum_buckets(counts) from `+rows+` r`)
	require.ErrorContains(t, err, "out of range", "the exact sum overflows; the read routes need the safe one")

	var got []int64
	require.NoError(t, c.QueryRow(ctx, `select api.sum_buckets_read(counts) from `+rows+` r`).Scan(&got))
	require.Len(t, got, 15)
	var total float64
	for _, v := range got {
		require.GreaterOrEqual(t, v, int64(0))
		total += float64(v)
	}
	require.LessOrEqual(t, total, math.Exp2(62), "the scaled total fits histogram_quantile's bigint sum")
	require.Equal(t, got[2], got[8], "the scale keeps the proportions")
	require.Positive(t, got[2])

	var p50, p95 float64
	require.NoError(t, c.QueryRow(ctx, `select api.histogram_quantile(0.5, api.sum_buckets_read(counts)),
		api.histogram_quantile(0.95, api.sum_buckets_read(counts)) from `+rows+` r`).Scan(&p50, &p95))
	// bucket 3 is (0.01, 0.025], bucket 9 (0.5, 0.75]
	require.InDelta(t, 0.025, p50, 1e-9)
	require.InDelta(t, 0.725, p95, 1e-6)

	// below 2^62 the result is api.sum_buckets exactly, nulls are skipped,
	// no row gives null, and a shape mismatch is an error
	var same bool
	require.NoError(t, c.QueryRow(ctx, `
		with r(counts) as (values (array[1,2,3,0,0,0,0,0,0,0,0,0,0,0,5]::bigint[]), (null),
		                          (array[4,0,0,0,0,0,0,0,0,0,0,0,0,7,1]::bigint[]))
		select api.sum_buckets_read(counts) = api.sum_buckets(counts) from r`).Scan(&same))
	require.True(t, same)
	var none []int64
	require.NoError(t, c.QueryRow(ctx, `select api.sum_buckets_read(counts) from (select null::bigint[] as counts where false) r`).Scan(&none))
	require.Nil(t, none)
	_, err = c.Exec(ctx, `select api.sum_buckets_read(counts) from (values (array[1,2]::bigint[]), (array[1]::bigint[])) r(counts)`)
	require.ErrorContains(t, err, "different shape")
}
