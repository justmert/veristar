package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// runSetup creates the drill tenants through the console api: web tenants
// with their sites, and an otlp token on every tenant. tenants are spread
// over several fixture users because enrollment allows 10 tenants per user
// per day, and the api load is spread over many tenants because the
// collector's rate limit is per tenant (docs/reference/collector.md).
func runSetup(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	out := fs.String("out", "", "state file to write")
	webTenants := fs.Int("web-tenants", 4, "tenants that get sites")
	sitesPer := fs.Int("sites-per-tenant", 10, "sites per web tenant")
	tenants := fs.Int("tenants", 24, "tenants in total, each with an otlp token")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	core := inv.group("core")[0]
	st := state{
		Suffix:   strconv.FormatInt(time.Now().Unix(), 36),
		Domain:   inv.All.Vars.Domain,
		InHost:   inv.All.Vars.Hostnames["in"],
		OTLPHost: inv.All.Vars.Hostnames["otlp"],
		AppHost:  inv.All.Vars.Hostnames["app"],
	}
	// fresh fixture ids per setup, so the daily enrollment limit of an
	// earlier setup does not apply
	baseID := 500000 + rand.IntN(400000) //nolint:gosec // a fixture user id, not a secret
	// cur creates the current batch of tenants; first keeps the session the
	// dashboard probe reuses
	var first, cur *console
	var curTerms string
	for i := range *tenants {
		if i%8 == 0 {
			id := baseID + i/8
			c, err := signIn(ctx, st.AppHost, core.Public, id, fmt.Sprintf("drill-%d", id))
			if err != nil {
				return nil, fmt.Errorf("sign in %d: %w", id, err)
			}
			if first == nil {
				first = c
			}
			var terms struct {
				Version string `json:"version"`
			}
			if s, b, err := c.do(http.MethodGet, "/api/terms", nil, &terms); err != nil || s != http.StatusOK {
				return nil, fmt.Errorf("terms: %d %s: %w", s, b, err)
			}
			cur = c
			curTerms = terms.Version
		}
		var t struct {
			ID string `json:"id"`
		}
		s, b, err := cur.do(http.MethodPost, "/api/tenants", map[string]any{
			"name": fmt.Sprintf("Drill %s %02d", st.Suffix, i),
			"enrollment": map[string]any{
				"website":       fmt.Sprintf("https://t%02d-%s.%s", i, st.Suffix, st.Domain),
				"stellar_usage": "this stage drill load",
			},
			"accepted_terms_version": curTerms,
		}, &t)
		if err != nil || s != http.StatusCreated {
			return nil, fmt.Errorf("tenant %d: %d %s: %w", i, s, b, err)
		}
		var tok struct {
			Token string `json:"token"`
		}
		s, b, err = cur.do(http.MethodPost, "/api/tenants/"+t.ID+"/otlp-tokens", map[string]any{"name": "drill"}, &tok)
		if err != nil || s != http.StatusCreated {
			return nil, fmt.Errorf("token %d: %d %s: %w", i, s, b, err)
		}
		st.Tenants = append(st.Tenants, tenantState{ID: t.ID, Token: tok.Token})
		if i < *webTenants {
			for j := range *sitesPer {
				origin := fmt.Sprintf("https://s%02d%02d-%s.%s", i, j, st.Suffix, st.Domain)
				var site struct {
					ID string `json:"id"`
				}
				s, b, err := cur.do(http.MethodPost, "/api/tenants/"+t.ID+"/sites", map[string]any{
					"name": fmt.Sprintf("Drill site %02d %02d", i, j), "domain": strings.TrimPrefix(origin, "https://"), "origins": []string{origin},
				}, &site)
				if err != nil || s != http.StatusCreated {
					return nil, fmt.Errorf("site %d/%d: %d %s: %w", i, j, s, b, err)
				}
				st.Sites = append(st.Sites, siteState{ID: site.ID, Tenant: t.ID, Origin: origin})
			}
		}
	}
	appURL, _ := url.Parse("https://" + st.AppHost)
	for _, ck := range first.http.Jar.Cookies(appURL) {
		st.Cookies = append(st.Cookies, cookieState{Name: ck.Name, Value: ck.Value})
	}
	st.DashProbe = dashProbeState{Tenant: st.Tenants[0].ID}

	// every site and token must be known to every edge node before a drill
	// starts, so the drill measures failures, not registry propagation
	deadline := time.Now().Add(3 * time.Minute)
	for _, e := range inv.group("edge") {
		for _, site := range st.Sites {
			gw := gwclient.New("https://"+st.InHost, true)
			gw.HTTP.Transport = pinnedTransport(e.Public, 5*time.Second)
			gw.Header.Set("Origin", site.Origin)
			for {
				if _, err := gw.Challenge(ctx, site.ID); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("site %s never reached %s", site.ID, e.Name)
				}
				time.Sleep(time.Second)
			}
		}
		for _, t := range st.Tenants {
			hc := &http.Client{Transport: pinnedTransport(e.Public, 5*time.Second), Timeout: 10 * time.Second}
			for {
				s, err := postOTLP(ctx, hc, st.OTLPHost, t.Token, otlpBody("drill-setup-"+st.Suffix, time.Now()))
				if err == nil && s == http.StatusOK {
					break
				}
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("token of %s never reached %s (%d: %w)", t.ID, e.Name, s, err)
				}
				time.Sleep(time.Second)
			}
		}
	}
	return st, writeJSON(*out, st)
}
