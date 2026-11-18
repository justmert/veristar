//go:build localconverge

package localconverge

// TestRollingTraffic sends one pageview every 250 ms, round robin through the
// edge nodes, while a rolling deploy.yml runs from another shell
// (docs/deploy/local-converge.md, "Rolling deploy rehearsal"). it stops when
// the file SA_LC_STOP names exists, then requires that umami holds every
// accepted event exactly once. the run's paths are unique, so the count spans
// both umami websites of the site: the per-visitor volume check moves a
// fast single visitor into the quarantine band, which is still stored.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/tests/gateway/gwclient"
)

func TestRollingTraffic(t *testing.T) {
	stop := os.Getenv("SA_LC_STOP")
	if stop == "" {
		t.Skip("SA_LC_STOP is not set; this test runs beside a rolling deploy of the local converge")
	}
	inv := loadInventory(t)
	edges := inv.All.Children["edge"].Hosts
	var core host
	for _, h := range inv.All.Children["core"].Hosts {
		core = h
	}
	c := signIn(t, inv, core, 1001, "lc-operator")
	var terms struct {
		Version string `json:"version"`
	}
	require.Equal(t, http.StatusOK, c.do(t, http.MethodGet, "/api/terms", nil, &terms))
	suffix := strconv.FormatInt(time.Now().Unix(), 36)
	var tenant struct{ ID string }
	require.Equal(t, http.StatusCreated, c.do(t, http.MethodPost, "/api/tenants", map[string]any{
		"name":                   "Rolling " + suffix,
		"enrollment":             map[string]any{"website": "https://shop." + inv.All.Vars.Domain, "stellar_usage": "rolling deploy rehearsal"},
		"accepted_terms_version": terms.Version,
	}, &tenant))
	origin := "https://roll-" + suffix + "." + inv.All.Vars.Domain
	var site struct{ ID string }
	require.Equal(t, http.StatusCreated, c.do(t, http.MethodPost, "/api/tenants/"+tenant.ID+"/sites", map[string]any{
		"name": "Roll " + suffix, "domain": strings.TrimPrefix(origin, "https://"), "origins": []string{origin},
	}, &site))
	t.Logf("site %s", site.ID)

	type gwc struct {
		name string
		gw   *gwclient.Client
	}
	var gws []gwc
	for name, e := range edges {
		gw := gwclient.New("https://"+inv.All.Vars.Hostnames["in"], true)
		gw.HTTP.Transport = transport(e.Public)
		gw.HTTP.Timeout = 10 * time.Second
		gw.Header.Set("Origin", origin)
		gw.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36")
		gw.Header.Set("Sec-Fetch-Site", "cross-site")
		gw.Header.Set("Sec-Fetch-Mode", "cors")
		gw.Header.Set("Sec-Fetch-Dest", "empty")
		gws = append(gws, gwc{name, gw})
	}
	accepted, failed := 0, map[string]int{}
	start := time.Now()
	for i := 0; ; i++ {
		if _, err := os.Stat(stop); err == nil {
			break
		}
		g := gws[i%len(gws)]
		ch, err := g.gw.Challenge(context.Background(), site.ID)
		if err != nil {
			failed[g.name+" challenge"]++
			time.Sleep(300 * time.Millisecond)
			continue
		}
		p := gwclient.Solve(ch)
		ev := gwclient.Pageview(site.ID, strings.TrimPrefix(origin, "https://"), fmt.Sprintf("/roll-%s/%06d", suffix, i), &p)
		status, err := g.gw.PostEvent(context.Background(), ev, nil)
		if err != nil || status != http.StatusAccepted {
			failed[fmt.Sprintf("%s post %d %v", g.name, status, err != nil)]++
		} else {
			accepted++
		}
		if i%200 == 0 {
			t.Logf("%s sent %d accepted %d failed %v", time.Since(start).Round(time.Second), i+1, accepted, failed)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("stopped: accepted %d failed %v", accepted, failed)
	q := fmt.Sprintf("select count(*)||' '||count(distinct url_path) from website_event where url_path like '/roll-%s/%%'", suffix)
	want := fmt.Sprintf("%d %d", accepted, accepted)
	deadline := time.Now().Add(15 * time.Minute)
	var got string
	for time.Now().Before(deadline) {
		got = sql(t, "umami", q)
		if got == want {
			break
		}
		time.Sleep(5 * time.Second)
	}
	t.Logf("umami rows/distinct %s, accepted %d", got, accepted)
	require.Equal(t, want, got)
}
