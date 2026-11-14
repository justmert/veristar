// Package histo holds the canonical latency histogram layout:
// re-bucketing of source histograms onto it, and quantiles read from it.
// the sql function api.histogram_quantile computes the same quantile as
// Quantile, operation for operation; tests check that they agree bit for bit.
package histo

import (
	"errors"
	"fmt"
	"math"
)

// Bounds are the advisory boundaries of http.server.request.duration in
// opentelemetry semantic conventions v1.45.0, in seconds.
var Bounds = [Buckets - 1]float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// Buckets is the number of canonical buckets: one per bound plus the open
// bucket above the last bound. postgresql stores them as bigint[15].
const Buckets = 15

// Open is the index of the open bucket above 10 s.
const Open = Buckets - 1

// Counts are the bucket counts of one histogram on the canonical layout.
// bucket i holds values in (Bounds[i-1], Bounds[i]]; bucket 0 starts at 0
// and bucket Open has no upper bound.
type Counts [Buckets]int64

// Slice returns the counts as a slice, the form pgx sends as bigint[].
func (c Counts) Slice() []int64 { return c[:] }

// Total returns the sum of the bucket counts.
func (c Counts) Total() int64 {
	var t int64
	for _, n := range c {
		t += n
	}
	return t
}

// Rebucket moves a source histogram onto the canonical layout: each source
// bucket goes, whole, to the canonical bucket that contains its upper bound
// (the last source bucket, unbounded, goes to the open bucket).
//
// exact reports whether every canonical bound is also a source bound. only
// then does each source bucket lie entirely inside one canonical bucket, so
// the result is what the canonical layout would have recorded. otherwise the
// counts of a source bucket that spans canonical bounds land in the bucket
// of its upper bound, and the caller flags the row lossy.
//
// bounds must be strictly increasing and not nan, and counts must have one
// more element than bounds, as in otlp. the total must fit an int64.
func Rebucket(bounds []float64, counts []uint64) (out Counts, exact bool, err error) {
	if len(counts) != len(bounds)+1 {
		return Counts{}, false, fmt.Errorf("histo: %d counts for %d bounds, want %d", len(counts), len(bounds), len(bounds)+1)
	}
	for i, b := range bounds {
		if math.IsNaN(b) {
			return Counts{}, false, fmt.Errorf("histo: bound %d is nan", i)
		}
		if i > 0 && b <= bounds[i-1] {
			return Counts{}, false, fmt.Errorf("histo: bounds not strictly increasing at %d (%v after %v)", i, b, bounds[i-1])
		}
	}

	var total uint64
	for i, n := range counts {
		if n > math.MaxInt64-total {
			return Counts{}, false, errors.New("histo: total count overflows int64")
		}
		total += n
		j := Open
		if i < len(bounds) {
			j = canonicalBucket(bounds[i])
		}
		out[j] += int64(n) //nolint:gosec // n <= total <= math.MaxInt64, checked above
	}

	// both lists are sorted, so one merge pass finds every canonical bound
	exact = true
	k := 0
	for _, cb := range Bounds {
		for k < len(bounds) && bounds[k] < cb {
			k++
		}
		if k == len(bounds) || bounds[k] != cb {
			exact = false
			break
		}
	}
	return out, exact, nil
}

// canonicalBucket returns the canonical bucket that contains v: the first
// whose upper bound is at least v, or the open bucket.
func canonicalBucket(v float64) int {
	for j, b := range Bounds {
		if v <= b {
			return j
		}
	}
	return Open
}

// ErrEmpty is returned by Quantile for a histogram without observations,
// where api.histogram_quantile returns null.
var ErrEmpty = errors.New("histo: empty histogram has no quantile")

// Quantile returns the q-quantile of a canonical histogram by linear
// interpolation inside the bucket that holds rank q * total. a quantile in
// the open bucket is +Inf, which callers show as ">10 s" and never as a
// number; IsOpen tells it apart.
//
// counts must have Buckets non-negative elements and q must be in [0, 1].
func Quantile(q float64, counts []int64) (float64, error) {
	if !(q >= 0 && q <= 1) {
		return 0, fmt.Errorf("histo: q %v is outside [0, 1]", q)
	}
	if len(counts) != Buckets {
		return 0, fmt.Errorf("histo: want %d buckets, have %d", Buckets, len(counts))
	}
	var total int64
	for i, c := range counts {
		if c < 0 {
			return 0, fmt.Errorf("histo: bucket %d holds %d", i, c)
		}
		if c > math.MaxInt64-total {
			// postgresql raises bigint out of range at the same point
			return 0, errors.New("histo: total count overflows int64")
		}
		total += c
	}
	if total == 0 {
		return 0, ErrEmpty
	}

	// every step below is one rounded float64 operation in the same order as
	// the sql function; the explicit float64 conversions keep the compiler
	// from fusing a multiply and an add, which would round differently
	rank := float64(q * float64(total))
	var cum int64
	for i, c := range counts {
		prev := cum
		cum += c
		if c > 0 && float64(cum) >= rank {
			if i == Open {
				return math.Inf(1), nil
			}
			lower := 0.0
			if i > 0 {
				lower = Bounds[i-1]
			}
			frac := (rank - float64(prev)) / float64(c)
			return lower + float64((Bounds[i]-lower)*frac), nil
		}
	}
	// unreachable: rank <= total, and the last non-empty bucket reaches total
	return 0, fmt.Errorf("histo: no bucket reached rank %v", rank)
}

// IsOpen reports whether a quantile fell in the open bucket above 10 s.
func IsOpen(v float64) bool { return math.IsInf(v, 1) }

// CoarseSpan is how many canonical bounds a source bucket must contain
// strictly inside it to count as wide: its requests could lie anywhere in
// four or more canonical buckets, yet all of them land in the one of its
// upper bound.
const CoarseSpan = 3

// Coarse reports whether at least half of the requests of a source
// histogram sit in wide source buckets (CoarseSpan). re-bucketing by upper
// bound then shows a percentile that can be wrong by a factor of 10 or
// more, for example a seconds metric recorded with an sdk's default bounds
// for milliseconds, where every request under 5 s lands in (2.5, 5]. the
// lossy flag alone does not convey that. the first source
// bucket starts at 0, because durations are never negative. bounds and
// counts must have the shape Rebucket accepts.
func Coarse(bounds []float64, counts []uint64) bool {
	if len(counts) != len(bounds)+1 {
		return false
	}
	var total, wide uint64
	for i, n := range counts {
		total += n
		if n == 0 {
			continue
		}
		lo, hi := 0.0, math.Inf(1)
		if i > 0 {
			lo = bounds[i-1]
		}
		if i < len(bounds) {
			hi = bounds[i]
		}
		inside := 0
		for _, b := range Bounds {
			if b > lo && b < hi {
				inside++
			}
		}
		if inside >= CoarseSpan {
			wide += n
		}
	}
	return total > 0 && wide >= total-wide
}
