package histo_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/histo"
)

func TestBoundsMatchPlan(t *testing.T) {
	// the design; the collector converts legacy milliseconds by scaling
	// 5 ... 10000 by 0.001, which must give these exact float64 values
	ms := []float64{5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
	require.Len(t, histo.Bounds, len(ms))
	for i, m := range ms {
		require.Equal(t, histo.Bounds[i], m*0.001, "bound %d", i)
	}
	require.Equal(t, 15, histo.Buckets)
}

func TestRebucketCanonicalIsExactIdentity(t *testing.T) {
	counts := make([]uint64, histo.Buckets)
	for i := range counts {
		counts[i] = uint64(i*7 + 1)
	}
	got, exact, err := histo.Rebucket(histo.Bounds[:], counts)
	require.NoError(t, err)
	require.True(t, exact)
	for i := range counts {
		require.Equal(t, int64(counts[i]), got[i])
	}
}

func TestRebucket(t *testing.T) {
	cases := []struct {
		name   string
		bounds []float64
		counts []uint64
		want   map[int]int64
		exact  bool
	}{
		{
			// finer than canonical and containing every canonical bound:
			// every source bucket lies inside one canonical bucket
			name:   "finer superset",
			bounds: []float64{0.001, 0.005, 0.0075, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10, 30},
			counts: []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18},
			want:   map[int]int64{0: 3, 1: 7, 2: 5, 3: 6, 4: 7, 5: 8, 6: 9, 7: 10, 8: 11, 9: 12, 10: 13, 11: 14, 12: 15, 13: 16, 14: 35},
			exact:  true,
		},
		{
			// coarser: (0.1, 1] lands whole in the bucket of its upper bound
			name:   "coarser subset",
			bounds: []float64{0.1, 1},
			counts: []uint64{5, 6, 7},
			want:   map[int]int64{5: 5, 9: 6, 14: 7},
			exact:  false,
		},
		{
			// a bound between canonical bounds goes to the next canonical one
			name:   "foreign bounds",
			bounds: []float64{0.003, 0.02, 0.2, 2, 20},
			counts: []uint64{1, 1, 1, 1, 1, 1},
			want:   map[int]int64{0: 1, 2: 1, 6: 1, 10: 1, 14: 2},
			exact:  false,
		},
		{
			name:   "no bounds",
			bounds: nil,
			counts: []uint64{9},
			want:   map[int]int64{14: 9},
			exact:  false,
		},
		{
			// a bound exactly on a canonical bound belongs to that bucket,
			// because buckets are closed above
			name:   "upper bound inclusive",
			bounds: []float64{0.005, 10},
			counts: []uint64{2, 3, 4},
			want:   map[int]int64{0: 2, 13: 3, 14: 4},
			exact:  false,
		},
		{
			name:   "negative and infinite bounds",
			bounds: []float64{-1, 0.004, math.Inf(1)},
			counts: []uint64{1, 2, 3, 4},
			want:   map[int]int64{0: 3, 14: 7},
			exact:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, exact, err := histo.Rebucket(tc.bounds, tc.counts)
			require.NoError(t, err)
			require.Equal(t, tc.exact, exact)
			var want histo.Counts
			for i, n := range tc.want {
				want[i] = n
			}
			require.Equal(t, want, got)
			var in uint64
			for _, n := range tc.counts {
				in += n
			}
			require.Equal(t, int64(in), got.Total(), "re-bucketing keeps every observation")
		})
	}
}

func TestRebucketRefuses(t *testing.T) {
	_, _, err := histo.Rebucket([]float64{1, 2}, []uint64{1, 2})
	require.ErrorContains(t, err, "2 counts for 2 bounds")
	_, _, err = histo.Rebucket([]float64{1, 1}, []uint64{1, 2, 3})
	require.ErrorContains(t, err, "not strictly increasing")
	_, _, err = histo.Rebucket([]float64{2, 1}, []uint64{1, 2, 3})
	require.ErrorContains(t, err, "not strictly increasing")
	_, _, err = histo.Rebucket([]float64{math.NaN()}, []uint64{1, 2})
	require.ErrorContains(t, err, "nan")
	_, _, err = histo.Rebucket([]float64{1}, []uint64{math.MaxInt64, 1})
	require.ErrorContains(t, err, "overflows")
}

func TestQuantile(t *testing.T) {
	c := func(m map[int]int64) []int64 {
		var x histo.Counts
		for i, n := range m {
			x[i] = n
		}
		return x.Slice()
	}
	cases := []struct {
		name   string
		q      float64
		counts []int64
		want   float64
	}{
		// 10 observations in (0.1, 0.25]: rank 5 is half way through it
		{"median inside one bucket", 0.5, c(map[int]int64{6: 10}), 0.1 + (0.25-0.1)*0.5},
		{"first bucket starts at zero", 0.5, c(map[int]int64{0: 4}), 0.0025},
		{"q 1 is the upper bound of the last bucket with data", 1, c(map[int]int64{2: 1, 7: 3}), 0.5},
		{"q 0 is the lower bound of the first bucket with data", 0, c(map[int]int64{3: 2, 9: 1}), 0.025},
		{"open bucket", 0.99, c(map[int]int64{0: 90, 14: 10}), math.Inf(1)},
		{"just below the open bucket", 0.9, c(map[int]int64{13: 90, 14: 10}), 10},
		{"empty buckets between", 0.75, c(map[int]int64{1: 2, 12: 2}), 5 + (7.5-5)*0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := histo.Quantile(tc.q, tc.counts)
			require.NoError(t, err)
			require.InDelta(t, tc.want, got, 1e-12)
			require.Equal(t, math.IsInf(tc.want, 1), histo.IsOpen(got))
		})
	}

	_, err := histo.Quantile(0.5, make([]int64, histo.Buckets))
	require.ErrorIs(t, err, histo.ErrEmpty)
	for _, q := range []float64{-0.01, 1.01, math.NaN()} {
		_, err = histo.Quantile(q, c(map[int]int64{0: 1}))
		require.ErrorContains(t, err, "outside [0, 1]")
	}
	_, err = histo.Quantile(0.5, []int64{1})
	require.ErrorContains(t, err, "want 15 buckets")
	_, err = histo.Quantile(0.5, c(map[int]int64{3: -1}))
	require.ErrorContains(t, err, "holds -1")
}
