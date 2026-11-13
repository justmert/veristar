package apimetrics

import (
	"encoding/json"
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
)

func resolver(now string) Resolver {
	t, err := time.Parse(time.RFC3339, now)
	if err != nil {
		panic(err)
	}
	return Resolver{Now: func() time.Time { return t }, Retention: config.APIRetention{MinuteDays: 30, HourlyMonths: 36}}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// on a bucket boundary, the current bucket is still the last one of a
// preset, so "now" is always inside the range
func TestPresetsOnBoundary(t *testing.T) {
	rv := resolver("2031-01-01T00:00:00Z")
	r, err := rv.Parse(url.Values{"range": {"1h"}})
	require.NoError(t, err)
	require.Equal(t, mustTime("2030-12-31T23:01:00Z"), r.From)
	require.Equal(t, mustTime("2031-01-01T00:01:00Z"), r.To)
	r, err = rv.Parse(url.Values{"range": {"12mo"}})
	require.NoError(t, err)
	require.Equal(t, mustTime("2030-01-02T00:00:00Z"), r.From)
	require.Equal(t, 365, r.Buckets())
}

// a leap day inside 24 months gives 731 daily buckets
func TestTwentyFourMonthsAcrossLeapDay(t *testing.T) {
	r, err := resolver("2028-07-01T12:00:00Z").Parse(url.Values{"range": {"24mo"}})
	require.NoError(t, err)
	require.Equal(t, Day, r.Resolution)
	require.Equal(t, 731, r.Buckets())
}

func TestRetentionStepChains(t *testing.T) {
	// a one-hour range four years back: minute is beyond the minute
	// horizon, hour beyond the hourly one, so it ends at day
	r, err := resolver("2031-06-15T10:30:00Z").Parse(url.Values{
		"range": {"custom"}, "from": {"2027-06-01T10:00:00Z"}, "to": {"2027-06-01T11:00:00Z"},
	})
	require.NoError(t, err)
	require.Equal(t, Day, r.Resolution)
	require.Equal(t, NoteOlderThanHourlyRetention, r.Note)
	require.Equal(t, mustTime("2027-06-01T00:00:00Z"), r.From)
	require.Equal(t, mustTime("2027-06-02T00:00:00Z"), r.To)
}

func TestPreviousCrossesHorizon(t *testing.T) {
	// a previous range inside the minute horizon stays at minutes; one that
	// reaches before it moves to hourly on its own
	rv := resolver("2031-06-15T10:30:00Z")
	r := Range{Preset: PresetCustom, From: mustTime("2031-05-16T02:00:00Z"), To: mustTime("2031-05-16T03:00:00Z"), Resolution: Minute}
	p := rv.Previous(r)
	require.Equal(t, Minute, p.Resolution)
	require.Equal(t, mustTime("2031-05-16T01:00:00Z"), p.From)
	r = Range{Preset: PresetCustom, From: mustTime("2031-05-16T00:00:00Z"), To: mustTime("2031-05-16T00:30:00Z"), Resolution: Minute}
	p = rv.Previous(r)
	require.Equal(t, Hour, p.Resolution)
	require.Equal(t, NoteOlderThanMinuteRetention, p.Note)
	require.Equal(t, mustTime("2031-05-15T23:00:00Z"), p.From)
	require.Equal(t, mustTime("2031-05-16T00:00:00Z"), p.To)
}

func TestPercentileJSON(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, `null`},
		{0.0025, `0.0025`},
		{math.Inf(1), `">10 s"`},
	} {
		p, err := percentile(tc.in)
		require.NoError(t, err)
		b, err := json.Marshal(p)
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(b))
		var back Percentile
		require.NoError(t, json.Unmarshal(b, &back))
		require.Equal(t, p, back)
	}
	for _, bad := range []any{math.NaN(), math.Inf(-1), -1.0, "0.1", int64(1)} {
		_, err := percentile(bad)
		require.Error(t, err, bad)
	}
}

func TestResolverRefusesMissingRetention(t *testing.T) {
	_, err := NewRoutes(nil, resolver("2031-01-01T00:00:00Z"))
	require.Error(t, err)
	rv := resolver("2031-01-01T00:00:00Z")
	rv.Retention.HourlyMonths = 12
	require.Error(t, rv.check())
}
