package webdash

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseDays(t *testing.T) {
	now := time.Date(2026, 11, 16, 15, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		q        string
		from, to string
		bad      bool
	}{
		{q: "", from: "2026-10-18", to: "2026-11-16"},
		{q: "from=2026-11-16&to=2026-11-16", from: "2026-11-16", to: "2026-11-16"},
		{q: "from=2026-11-10", from: "2026-11-10", to: "2026-11-16"},
		{q: "to=2026-11-01", from: "2026-10-03", to: "2026-11-01"},
		{q: "to=2026-11-17", bad: true},
		{q: "from=2026-11-12&to=2026-11-11", bad: true},
		{q: "from=2025-10-12&to=2026-11-16", bad: true},
		{q: "from=2025-10-13&to=2026-11-16", from: "2025-10-13", to: "2026-11-16"},
		{q: "from=16.11.2026", bad: true},
		{q: "from=2026-11-01&from=2026-11-02", bad: true},
	} {
		q, err := url.ParseQuery(tc.q)
		require.NoError(t, err)
		d, err := ParseDays(q, now)
		if tc.bad {
			var he *httpx.Error
			require.ErrorAs(t, err, &he, tc.q)
			require.Equal(t, httpx.CodeInvalidQuery, he.Code, tc.q)
			continue
		}
		require.NoError(t, err, tc.q)
		require.Equal(t, day(tc.from), d.From, tc.q)
		require.Equal(t, day(tc.to), d.To, tc.q)
	}
}

func TestDaysArithmetic(t *testing.T) {
	d := Days{From: day("2026-11-10"), To: day("2026-11-16")}
	require.Equal(t, 7, d.N())
	require.Len(t, d.List(), 7)
	require.Equal(t, Days{From: day("2026-11-03"), To: day("2026-11-09")}, d.Previous())
	one := Days{From: day("2026-11-16"), To: day("2026-11-16")}
	require.Equal(t, Days{From: day("2026-11-15"), To: day("2026-11-15")}, one.Previous())
	// both ends included, and the next midnight is not
	r := one.Umami()
	require.Equal(t, day("2026-11-16"), r.Start)
	require.Equal(t, day("2026-11-17").Add(-time.Millisecond), r.End)
	require.Equal(t, RangeJSON{From: "2026-11-10", To: "2026-11-16", Days: 7}, d.JSON())
}

func TestTop(t *testing.T) {
	a, b := uuid.MustParse("00000000-0000-0000-0000-00000000000a"), uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	m := map[mergeKey]int64{
		{key: "/"}:            5,
		{site: a, key: "/x"}:  7,
		{site: b, key: "/x"}:  7,
		{site: a, key: "/y"}:  5,
		{key: "github.com"}:   1,
		{site: b, key: "/zz"}: 9,
	}
	items, total := top(m, 4)
	require.Equal(t, int64(6), total)
	require.Equal(t, []Item{
		{Key: "/zz", Value: 9, Site: &b},
		{Key: "/x", Value: 7, Site: &a},
		{Key: "/x", Value: 7, Site: &b},
		{Key: "/", Value: 5},
	}, items)
	items, total = top(map[mergeKey]int64{}, 10)
	require.Equal(t, []Item{}, items)
	require.Zero(t, total)
}

func TestFill(t *testing.T) {
	from := day("2026-11-16")
	pts := grid(from, time.Hour, 24)
	var s series
	require.NoError(t, json.Unmarshal([]byte(`{
		"pageviews":[{"x":"2026-11-16T00:00:00Z","y":3},{"x":"2026-11-16 13:00:00","y":"2"}],
		"sessions":[{"x":"2026-11-16T00:00:00Z","y":1},{"x":"2026-11-16T13:00:00","y":2}]}`), &s))
	require.NoError(t, fill(pts, from, time.Hour, s))
	require.Equal(t, int64(3), pts[0].Pageviews)
	require.Equal(t, int64(1), pts[0].Visitors)
	require.Equal(t, int64(2), pts[13].Pageviews)
	require.Equal(t, int64(2), pts[13].Visitors)
	require.Equal(t, httpx.Timestamp(from.Add(23*time.Hour)), pts[23].At)

	// a bucket off the grid is an error, never dropped
	for _, x := range []string{"2026-11-17T00:00:00Z", "2026-11-16T13:30:00Z", "2026-11-15T23:00:00Z", "yesterday"} {
		var off series
		require.NoError(t, json.Unmarshal([]byte(`{"pageviews":[{"x":"`+x+`","y":1}],"sessions":[]}`), &off))
		require.Error(t, fill(grid(from, time.Hour, 24), from, time.Hour, off), x)
	}
}

func TestFlexInt(t *testing.T) {
	for in, want := range map[string]int64{`3`: 3, `"42"`: 42, `null`: 0, `0`: 0} {
		var f flexInt
		require.NoError(t, json.Unmarshal([]byte(in), &f), in)
		require.Equal(t, want, int64(f), in)
	}
	for _, in := range []string{`"x"`, `1.5`, `-1`, `{}`} {
		var f flexInt
		require.Error(t, json.Unmarshal([]byte(in), &f), in)
	}
}

func TestLimiter(t *testing.T) {
	l := &limiter{counts: map[uuid.UUID]*window{}}
	u, v := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for i := range EngineLimit {
		ok, _ := l.allow(u, at.Add(time.Duration(i)*time.Millisecond))
		require.True(t, ok, i)
	}
	ok, wait := l.allow(u, at.Add(10*time.Second))
	require.False(t, ok)
	require.Equal(t, 50*time.Second, wait)
	ok, _ = l.allow(v, at.Add(10*time.Second))
	require.True(t, ok, "another member has its own window")
	ok, _ = l.allow(u, at.Add(time.Minute))
	require.True(t, ok, "a new window starts after a minute")
}

func TestSiteWebsite(t *testing.T) {
	q := uuid.New()
	s := site{ID: uuid.New(), Status: "active", Quarantine: uuid.NullUUID{UUID: q, Valid: true}}
	w, ok := s.website(BandPrimary)
	require.True(t, ok)
	require.Equal(t, s.ID.String(), w)
	w, ok = s.website(BandQuarantine)
	require.True(t, ok)
	require.Equal(t, q.String(), w)
	_, ok = site{ID: uuid.New()}.website(BandQuarantine)
	require.False(t, ok)
	for status, live := range map[string]bool{"active": true, "suspended": true, "removed": false, "pending": false} {
		require.Equal(t, live, site{Status: status}.live(), status)
	}
}

func TestDimensions(t *testing.T) {
	require.Len(t, DimensionNames, len(Dimensions))
	for _, n := range DimensionNames {
		d, ok := Dimensions[n]
		require.True(t, ok, n)
		require.True(t, (d.aggregate == "") != (d.metric == ""), "%s reads exactly one source", n)
	}
}
