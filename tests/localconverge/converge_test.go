//go:build localconverge

// Package localconverge runs the web path and api path end to end against
// the stack that infra/ansible converged on the local topology
// (docs/deploy/local-converge.md): sign-in through the core caddy and the
// test-only oauth fixture, a tenant and a site created through the console
// api, pageviews through every edge node's caddy and gateway into umami, and
// an otlp metric through every edge node's collector into api.metrics_minute.
//
//	SA_LC_INVENTORY=infra/ansible/inventories/local/hosts.json \
//	  go test -tags localconverge -count=1 -v ./tests/localconverge/
//
// the event client is the scripted gwclient (it solves the proof in go), not
// a browser; the browser matrices stay with their own suites.
package localconverge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/tests/gateway/gwclient"
)

type host struct {
	Public  string `json:"sa_public_ipv4"`
	Private string `json:"sa_private_ip"`
}

type inventory struct {
	All struct {
		Vars struct {
			Domain    string            `json:"sa_base_domain"`
			Hostnames map[string]string `json:"sa_hostnames"`
		} `json:"vars"`
		Children map[string]struct {
			Hosts map[string]host `json:"hosts"`
		} `json:"children"`
	} `json:"all"`
}

func loadInventory(t *testing.T) inventory {
	t.Helper()
	p := os.Getenv("SA_LC_INVENTORY")
	if p == "" {
		t.Skip("SA_LC_INVENTORY is not set; this suite runs against a converged local topology")
	}
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	var inv inventory
	require.NoError(t, json.Unmarshal(b, &inv))
	return inv
}

// transport resolves every platform hostname to the given address, as the
// traffic manager's answers would, and trusts caddy's local ca without checking
// (the local converge has no public certificates).
func transport(addr string) *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			h, port, err := net.SplitHostPort(a)
			if err != nil {
				return nil, err
			}
			if net.ParseIP(h) != nil {
				return d.DialContext(ctx, network, a)
			}
			return d.DialContext(ctx, network, net.JoinHostPort(addr, port))
		},
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // caddy's local ca in the local converge
		ForceAttemptHTTP2: true,
	}
}

type console struct {
	base string
	http *http.Client
}

func (c *console) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	require.NoError(t, err)
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.base)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && resp.StatusCode < 300 {
		require.NoError(t, json.Unmarshal(b, out), string(b))
	}
	if resp.StatusCode >= 300 {
		t.Logf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return resp.StatusCode
}

// signIn walks the oauth web flow as a browser would: start on the console,
// consent on the fixture as the given github user, back to the callback.
func signIn(t *testing.T, inv inventory, core host, githubID int, login string) *console {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := &http.Client{Transport: transport(core.Public), Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "https://" + inv.All.Vars.Hostnames["app"]

	resp, err := hc.Get(base + "/api/auth/github/start")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	authorize, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, core.Public, authorize.Hostname(), "sign-in must go to the fixture on the core node")

	form := url.Values{}
	for k, vs := range authorize.Query() {
		form[k] = vs
	}
	form.Set("fixture_user_id", strconv.Itoa(githubID))
	form.Set("fixture_login", login)
	form.Set("fixture_decision", "authorize")
	resp, err = hc.PostForm(authorize.Scheme+"://"+authorize.Host+authorize.Path, form)
	require.NoError(t, err)
	resp.Body.Close()
	require.Contains(t, []int{http.StatusFound, http.StatusSeeOther}, resp.StatusCode)
	callback := resp.Header.Get("Location")
	require.True(t, strings.HasPrefix(callback, base+"/api/auth/github/callback?"), callback)

	resp, err = hc.Get(callback)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.NotContains(t, resp.Header.Get("Location"), "error=", "sign-in failed")
	return &console{base: base, http: hc}
}

// sql runs one query on the core node's postgresql and returns the trimmed
// output. it reads the stored result; nothing is written through it.
func sql(t *testing.T, db, q string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", "sa-lc-core-1", "docker", "exec", "veristar-core-postgres-1",
		"psql", "-U", "postgres", "-d", db, "-tAX", "-c", q).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func TestConvergedStack(t *testing.T) {
	inv := loadInventory(t)
	edges := inv.All.Children["edge"].Hosts
	cores := inv.All.Children["core"].Hosts
	require.Len(t, edges, 3)
	require.NotEmpty(t, cores)
	var core host
	for _, h := range cores {
		core = h
	}

	// 1001 is the operator id the local inventory configures
	c := signIn(t, inv, core, 1001, "lc-operator")
	var me map[string]any
	require.Equal(t, http.StatusOK, c.do(t, http.MethodGet, "/api/me", nil, &me))
	t.Logf("signed in: %v", me)

	var terms struct {
		Version string `json:"version"`
	}
	require.Equal(t, http.StatusOK, c.do(t, http.MethodGet, "/api/terms", nil, &terms))

	suffix := strconv.FormatInt(time.Now().Unix(), 36)
	var tenant struct {
		ID string `json:"id"`
	}
	require.Equal(t, http.StatusCreated, c.do(t, http.MethodPost, "/api/tenants", map[string]any{
		"name": "Local converge " + suffix,
		"enrollment": map[string]any{
			"website":       "https://shop." + inv.All.Vars.Domain,
			"stellar_usage": "local converge end-to-end check",
		},
		"accepted_terms_version": terms.Version,
	}, &tenant))

	origin := "https://shop-" + suffix + "." + inv.All.Vars.Domain
	var site struct {
		ID string `json:"id"`
	}
	require.Equal(t, http.StatusCreated, c.do(t, http.MethodPost, "/api/tenants/"+tenant.ID+"/sites", map[string]any{
		"name": "Shop " + suffix, "domain": strings.TrimPrefix(origin, "https://"), "origins": []string{origin},
	}, &site))
	t.Logf("tenant %s site %s", tenant.ID, site.ID)

	t.Run("web path through every edge node into umami", func(t *testing.T) {
		names := make([]string, 0, len(edges))
		for name, e := range edges {
			names = append(names, name)
			gw := gwclient.New("https://"+inv.All.Vars.Hostnames["in"], true)
			gw.HTTP.Transport = transport(e.Public)
			gw.Header.Set("Origin", origin)
			gw.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36")
			gw.Header.Set("Sec-Fetch-Site", "cross-site")
			gw.Header.Set("Sec-Fetch-Mode", "cors")
			gw.Header.Set("Sec-Fetch-Dest", "empty")

			// the site reaches every gateway through registry_sites once the
			// reconciler published it
			var ch gwclient.Challenge
			require.Eventually(t, func() bool {
				var err error
				ch, err = gw.Challenge(context.Background(), site.ID)
				return err == nil
			}, 90*time.Second, time.Second, "challenge on %s", name)
			p := gwclient.Solve(ch)
			ev := gwclient.Pageview(site.ID, strings.TrimPrefix(origin, "https://"), "/converge/"+name, &p)
			status, err := gw.PostEvent(context.Background(), ev, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, status, "event through %s", name)
		}
		// three pageviews, one per edge node, replayed into umami
		q := fmt.Sprintf("select count(*) from website_event where website_id = '%s' and url_path like '/converge/%%'", site.ID)
		require.Eventually(t, func() bool { return sql(t, "umami", q) == "3" }, 2*time.Minute, 2*time.Second,
			"umami did not store one pageview per edge node %v", names)
		paths := sql(t, "umami", fmt.Sprintf("select string_agg(url_path, ',' order by url_path) from website_event where website_id = '%s'", site.ID))
		t.Logf("umami website_event paths: %s", paths)
	})

	// the console image carries the umami dashboard theme (docs/adr/0004);
	// the core caddy serves it and links it into umami's html
	t.Run("umami dashboard theme through the core caddy", func(t *testing.T) {
		hc := &http.Client{Transport: transport(core.Public), Timeout: 30 * time.Second}
		get := func(path string) (*http.Response, []byte) {
			resp, err := hc.Get("https://" + inv.All.Vars.Hostnames["web"] + path)
			require.NoError(t, err)
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			return resp, b
		}
		for _, f := range []string{"_veristar/theme.css", "_veristar/fonts/fonts.css", "favicon.ico", "site.webmanifest"} {
			want, err := os.ReadFile("../../deploy/caddy/umami-theme/site/" + f)
			require.NoError(t, err)
			resp, got := get("/" + f)
			require.Equal(t, http.StatusOK, resp.StatusCode, f)
			require.Equal(t, want, got, "%s differs from the repository's theme", f)
		}
		resp, page := get("/websites/" + site.ID)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, 1, strings.Count(string(page), `<link rel="stylesheet" href="/_veristar/theme.css"></head>`))
	})

	t.Run("otlp metrics through every edge node into the platform database", func(t *testing.T) {
		var tok struct {
			Token string `json:"token"`
		}
		require.Equal(t, http.StatusCreated, c.do(t, http.MethodPost, "/api/tenants/"+tenant.ID+"/otlp-tokens",
			map[string]any{"name": "converge " + suffix}, &tok))
		require.True(t, strings.HasPrefix(tok.Token, "sa_otlp_"))

		now := time.Now()
		for name, e := range edges {
			hc := &http.Client{Transport: transport(e.Public), Timeout: 30 * time.Second}
			body := otlpHistogram(name, now)
			require.Eventually(t, func() bool {
				req, err := http.NewRequest(http.MethodPost, "https://"+inv.All.Vars.Hostnames["otlp"]+"/v1/metrics", bytes.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+tok.Token)
				resp, err := hc.Do(req)
				if err != nil {
					return false
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Logf("otlp via %s: %d %s", name, resp.StatusCode, b)
				}
				// a new token reaches each collector through registry_otlp
				return resp.StatusCode == http.StatusOK
			}, 90*time.Second, 2*time.Second, "otlp through %s", name)
		}
		q := fmt.Sprintf("select coalesce(sum(count), 0) || ':' || count(distinct service) from api.metrics_minute where tenant_id = '%s'", tenant.ID)
		require.Eventually(t, func() bool { return sql(t, "platform", q) == "15:3" }, 2*time.Minute, 2*time.Second,
			"api.metrics_minute did not get 5 requests from each of the 3 services")
	})
}

// otlpHistogram is one delta http.server.request.duration histogram with 5
// requests, in otlp/json, from a service named after the edge node it is
// sent through.
func otlpHistogram(service string, now time.Time) []byte {
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	attr := func(k string, v any) map[string]any {
		switch x := v.(type) {
		case int:
			return map[string]any{"key": k, "value": map[string]any{"intValue": strconv.Itoa(x)}}
		default:
			return map[string]any{"key": k, "value": map[string]any{"stringValue": fmt.Sprint(x)}}
		}
	}
	doc := map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{attr("service.name", "converge-"+service)}},
		"scopeMetrics": []any{map[string]any{
			"scope": map[string]any{"name": "localconverge"},
			"metrics": []any{map[string]any{
				"name": "http.server.request.duration",
				"unit": "s",
				"histogram": map[string]any{
					"aggregationTemporality": 1,
					"dataPoints": []any{map[string]any{
						"startTimeUnixNano": ns(now.Add(-time.Minute)),
						"timeUnixNano":      ns(now),
						"count":             "5",
						"sum":               0.25,
						"explicitBounds":    []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
						"bucketCounts":      []string{"0", "0", "1", "2", "1", "1", "0", "0", "0", "0", "0", "0", "0", "0", "0"},
						"attributes": []any{
							attr("http.request.method", "GET"),
							attr("http.route", "/v1/items/{id}"),
							attr("http.response.status_code", 200),
						},
					}},
				},
			}},
		}},
	}}}
	b, _ := json.Marshal(doc)
	return b
}
