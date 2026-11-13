package migration

import (
	"strings"
	"testing"

	"github.com/justmert/stellar-analytics/internal/umami"
)

func TestParseEnv(t *testing.T) {
	m, err := ParseEnv(strings.NewReader("# comment\n\nA=1\nB=x=y\nC=\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "1" || m["B"] != "x=y" || m["C"] != "" || len(m) != 3 {
		t.Fatalf("parsed %v", m)
	}
	for _, bad := range []string{"A=1\nA=2\n", "no equals\n", "=v\n", "A B=1\n"} {
		if _, err := ParseEnv(strings.NewReader(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func snap() *Snapshot {
	return &Snapshot{
		Tables: map[string]int64{"platform.cp.sites": 3, "umami.public.website_event": 40, "platform.cp.leader_fence": 2},
		Web:    map[WebKey]WebTotals{{"t1", "2026-11-13", "primary"}: {Sites: 1, Pageviews: 10}},
		Dims:   map[DimKey]DimTotals{{"t1", "2026-11-13", "primary", "country"}: {Rows: 1, Value: 4}},
		API:    map[APIKey]APITotals{{"metrics_daily", "t1", "2026-11-13", "metric"}: {Rows: 2, Count: 7, Sum: 0.5}},
		Stats:  map[StatsKey]umami.StatsValues{{"w1", "2026-11-13"}: {Pageviews: 10, Visitors: 4}},
	}
}

func TestCompareEqual(t *testing.T) {
	src, dst := snap(), snap()
	// the excluded table may differ without a difference being reported
	dst.Tables["platform.cp.leader_fence"] = 9
	r := Compare(src, dst)
	if len(r.Differences) != 0 {
		t.Fatalf("differences on equal snapshots: %v", r.Differences)
	}
	if r.Compared["rows"] != 2 || r.Compared["umami-stats"] != 1 || len(r.Excluded) != 1 {
		t.Fatalf("compared %v excluded %v", r.Compared, r.Excluded)
	}
}

func TestCompareFindsEveryKind(t *testing.T) {
	src, dst := snap(), snap()
	dst.Tables["umami.public.website_event"] = 39
	delete(dst.Tables, "platform.cp.sites")
	dst.Tables["platform.cp.extra"] = 1
	dst.Web[WebKey{"t1", "2026-11-13", "primary"}] = WebTotals{Sites: 1, Pageviews: 11}
	dst.Dims[DimKey{"t1", "2026-11-13", "primary", "country"}] = DimTotals{Rows: 1, Value: 5}
	at := dst.API[APIKey{"metrics_daily", "t1", "2026-11-13", "metric"}]
	at.Counts[3] = 1
	dst.API[APIKey{"metrics_daily", "t1", "2026-11-13", "metric"}] = at
	dst.Stats[StatsKey{"w1", "2026-11-13"}] = umami.StatsValues{Pageviews: 10, Visitors: 5}
	r := Compare(src, dst)
	kinds := map[string]int{}
	for _, d := range r.Differences {
		kinds[d.Kind]++
	}
	want := map[string]int{"rows": 3, "web": 1, "web-dim": 1, "api": 1, "umami-stats": 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s: %d differences, want %d (%v)", k, kinds[k], n, r.Differences)
		}
	}
}
