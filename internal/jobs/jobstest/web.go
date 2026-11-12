package jobstest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// WebSite is a site provisioned the way the reconciler leaves it: rows in
// cp.tenants and cp.sites, an umami team with the primary and quarantine
// websites, and an active registry_sites entry.
type WebSite struct {
	Tenant     uuid.UUID
	Site       registry.Site
	Domain     string
	Quarantine string
}

// NewWebTenant creates an active tenant named name with n active sites.
func NewWebTenant(t testing.TB, pg *dbtest.Postgres, um *umamictr.Stack, js jetstream.JetStream, name string, n int) (uuid.UUID, []WebSite) {
	t.Helper()
	ctx := context.Background()
	team, _, err := um.Admin.CreateTeam(ctx, name, "")
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.Must(uuid.NewV7())
	c, err := pg.Superuser(ctx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	slug := fmt.Sprintf("p-%s", tenant.String()[24:])
	if _, err := c.Exec(ctx, `insert into cp.tenants (id, slug, name, status, umami_team_id) values ($1, $2, $3, 'active', $4)`,
		tenant, slug, name, team.ID); err != nil {
		t.Fatal(err)
	}
	pub, err := registry.NewPublisher(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]WebSite, 0, n)
	for i := range n {
		site, quarantine := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		domain := fmt.Sprintf("s%d.%s.example", i, slug)
		for _, id := range []uuid.UUID{site, quarantine} {
			if _, err := um.Admin.CreateWebsite(ctx, umami.NewWebsite{ID: id.String(), Name: domain, Domain: domain, TeamID: team.ID}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.Exec(ctx, `insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
			values ($1, $2, $3, $3, $4, $5, 'active', 'token')`, site, tenant, domain, []string{"https://" + domain}, quarantine); err != nil {
			t.Fatal(err)
		}
		rs := registry.Site{ID: site.String(), TenantID: tenant.String(), Status: registry.StatusActive,
			Origins: []string{"https://" + domain}, QuarantineWebsiteID: quarantine.String()}
		if err := pub.PutSite(ctx, rs); err != nil {
			t.Fatal(err)
		}
		out = append(out, WebSite{Tenant: tenant, Site: rs, Domain: domain, Quarantine: quarantine.String()})
	}
	return tenant, out
}

var (
	userAgents = []string{
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
	}
	countries = []string{"DE", "US", "FR", "BR", "NG", ""}
	screens   = []string{"1440x900", "2560x1440", "390x844"}
)

// referrers for a site: other sites, the site's own host with and without
// www (umami stores both as no referrer) and none
func referrers(domain string) []string {
	return []string{
		"https://www.google.com/search?q=stellar", "https://github.com/stellar", "https://GitHub.com/x",
		"https://news.ycombinator.com/item?id=1", "https://duckduckgo.com/", "https://" + domain + "/",
		"https://www." + domain + "/a", "",
	}
}

// SendWebTraffic sends a deterministic random day of visits to website for
// every day: visitors with a client salt, user agent and country each, one
// to four page views a few minutes apart, some custom events, and events
// at exactly 00:00:00 and 23:59:59 so that day boundaries are exercised.
// it returns how many events umami stored.
func SendWebTraffic(t testing.TB, um *umamictr.Stack, website, domain string, days []time.Time, seed uint64, visitors int) int {
	t.Helper()
	ctx := context.Background()
	//nolint:gosec // seeded on purpose: the same traffic on every run, nothing secret
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	refs := referrers(domain)
	sent := 0
	send := func(at time.Time, salt, ua, country, path, ref, name string) {
		r, err := um.Client.Send(ctx, umami.SendRequest{
			Event: umami.Event{Website: website, Hostname: domain, URL: path, Referrer: ref, Title: "page",
				Screen: screens[rng.IntN(len(screens))], Language: "en-US", Name: name, Timestamp: at.Unix(), Salt: salt},
			UserAgent: ua, Country: country,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !r.Bot {
			sent++
		}
	}
	for _, day := range days {
		for v := range visitors {
			salt := fmt.Sprintf("vt-%d-%d", seed, v)
			ua := userAgents[rng.IntN(len(userAgents))]
			country := countries[rng.IntN(len(countries))]
			at := day.Add(time.Duration(rng.IntN(23*3600)) * time.Second)
			switch v {
			case 0:
				at = day
			case 1:
				at = day.Add(24*time.Hour - time.Second)
			}
			views := 1 + rng.IntN(4)
			for p := range views {
				ts := at.Add(time.Duration(p*(1+rng.IntN(9))) * time.Minute)
				if ts.Day() != at.Day() {
					break
				}
				send(ts, salt, ua, country, fmt.Sprintf("/p%d", rng.IntN(6)), refs[rng.IntN(len(refs))], "")
			}
			if rng.IntN(4) == 0 {
				send(at, salt, ua, country, "/", "", "signup")
			}
		}
	}
	return sent
}
