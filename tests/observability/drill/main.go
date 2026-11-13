// Command drill is a TEST FIXTURE of the this stage local stack
// (tests/observability). it runs inside the stack's network:
//
//	drill signin -ca <ca>        signs in as the fixture operator (github id
//	                             1001) through the test-only oauth fixture, so
//	                             cp.users has the operator platformctl
//	                             probe-tenant create needs
//	drill views -ca <ca> -probe <tenant id>
//	                             signs in, reads every operator view and,
//	                             with a new key for all tenants, the machine
//	                             api tenant list, and fails if the probe
//	                             tenant appears in any of them
//	drill routes -ca <ca> -token <otlp token> -n <routes>
//	                             sends one otlp point per distinct route, to
//	                             push a tenant over the metrics writer's cap
//	drill otlp -ca <ca> -token <otlp token> -n <requests>
//	                             sends n otlp requests, one queue message each
//	drill events -ca <ca> -site <site id> -origin <origin> -n <events>
//	                             sends n web events through the tracker
//	                             protocol (challenge, proof, event)
//	drill dlq -nats <url> -action publish|purge
//	                             puts one message on dlq.web.drill, or purges
//	                             WEB_DLQ, to fire and resolve DLQNotEmpty
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"

	"github.com/justmert/veristar/internal/histo"
)

const app = "https://app.p17.test"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: drill signin|views|routes [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	ca := fs.String("ca", "", "test ca file")
	probe := fs.String("probe", "", "probe tenant id")
	token := fs.String("token", "", "otlp token")
	n := fs.Int("n", 0, "number of routes, requests or events")
	site := fs.String("site", "", "site id")
	origin := fs.String("origin", "", "site origin")
	natsURL := fs.String("nats", "", "nats url")
	action := fs.String("action", "", "publish or purge")
	_ = fs.Parse(os.Args[2:])
	if os.Args[1] == "dlq" {
		if err := dlq(*natsURL, *action); err != nil {
			fmt.Fprintln(os.Stderr, "drill:", err)
			os.Exit(1)
		}
		return
	}
	c, err := client(*ca)
	if err == nil {
		switch os.Args[1] {
		case "signin":
			err = signin(c)
		case "views":
			err = views(c, *probe)
		case "routes":
			err = routes(c, *token, *n)
		case "otlp":
			err = otlpMany(c, *token, *n)
		case "events":
			err = events(c, *site, *origin, *n)
		default:
			err = fmt.Errorf("unknown command %s", os.Args[1])
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "drill:", err)
		os.Exit(1)
	}
}

func client(caFile string) (*http.Client, error) {
	b, err := os.ReadFile(caFile) //nolint:gosec // test fixture: the ca file of the local test stack
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("no certificate in ca file")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Jar:       jar,
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		// redirects are followed by hand, so each step is visible
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// reply is what a test request returned; the body is read and closed
type reply struct {
	StatusCode int
	Header     http.Header
}

func do(c *http.Client, method, u string, body io.Reader, h map[string]string) (reply, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, u, body) //nolint:gosec // test fixture: urls are the local test stack's
	if err != nil {
		return reply{}, nil, err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req) //nolint:gosec // test fixture: urls are the local test stack's
	if err != nil {
		return reply{}, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, b, err
}

func signin(c *http.Client) error {
	resp, _, err := do(c, http.MethodGet, app+"/api/auth/github/start", nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("start: %d", resp.StatusCode)
	}
	authz, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return err
	}
	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", "1001")
	form.Set("fixture_login", "the-operator")
	post := *authz
	post.RawQuery = ""
	resp, b, err := do(c, http.MethodPost, post.String(), strings.NewReader(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("fixture authorize: %d %s", resp.StatusCode, b)
	}
	resp, b, err = do(c, http.MethodGet, resp.Header.Get("Location"), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return fmt.Errorf("callback: %d %s", resp.StatusCode, b)
	}
	resp, b, err = do(c, http.MethodGet, app+"/api/me", nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/api/me after sign-in: %d %s", resp.StatusCode, b)
	}
	fmt.Printf("signed in: %s\n", b)
	return nil
}

func views(c *http.Client, probe string) error {
	if probe == "" {
		return errors.New("-probe is required")
	}
	if err := signin(c); err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	week := time.Now().UTC().AddDate(0, 0, -6).Format("2006-01-02")
	paths := []string{
		"/api/operator/tenants",
		"/api/operator/tenants?status=active",
		"/api/operator/web/projects?from=" + week + "&to=" + today,
		"/api/operator/api/projects?from=" + week + "&to=" + today,
		"/api/operator/audit",
	}
	for _, p := range paths {
		resp, b, err := do(c, http.MethodGet, app+p, nil, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %d %s", p, resp.StatusCode, b)
		}
		found := bytes.Contains(b, []byte(probe))
		fmt.Printf("%-60s %d bytes, probe tenant present: %v\n", p, len(b), found)
		// the audit log names the tenant in the rows platformctl wrote; it
		// is the record of who made it, not an operator view of tenants
		if found && p != "/api/operator/audit" {
			return fmt.Errorf("%s lists the probe tenant", p)
		}
	}
	// the detail route answers 404 for an internal tenant
	resp, _, err := do(c, http.MethodGet, app+"/api/operator/tenants/"+probe, nil, nil)
	if err != nil {
		return err
	}
	fmt.Printf("%-60s %d\n", "/api/operator/tenants/<probe>", resp.StatusCode)
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("tenant detail of the probe tenant answered %d, want 404", resp.StatusCode)
	}
	body := `{"name":"p17 drill all tenants","scope":"metrics:read","expires_in_days":1}`
	resp, b, err := do(c, http.MethodPost, app+"/api/operator/api-keys", strings.NewReader(body), map[string]string{
		"Content-Type": "application/json", "X-Requested-With": "sa-console", "Origin": app,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create key: %d %s", resp.StatusCode, b)
	}
	var k struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return err
	}
	for _, p := range []string{"/api/metrics/v1/tenants", "/api/metrics/v1/web/daily", "/api/metrics/v1/api/daily"} {
		resp, b, err = do(c, http.MethodGet, app+p, nil, map[string]string{"Authorization": "Bearer " + k.Key})
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %d %s", p, resp.StatusCode, b)
		}
		found := bytes.Contains(b, []byte(probe))
		fmt.Printf("%-60s %d bytes, probe tenant present: %v\n", p+" (key for all tenants)", len(b), found)
		if found {
			return fmt.Errorf("%s lists the probe tenant", p)
		}
	}
	resp, _, err = do(c, http.MethodGet, app+"/api/metrics/v1/web/daily?tenant="+probe, nil, map[string]string{"Authorization": "Bearer " + k.Key})
	if err != nil {
		return err
	}
	fmt.Printf("%-60s %d\n", "/api/metrics/v1/web/daily?tenant=<probe> (key for all)", resp.StatusCode)
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("a key for all tenants read the probe tenant: %d", resp.StatusCode)
	}
	return nil
}

func routes(c *http.Client, token string, n int) error {
	if token == "" || n < 1 {
		return errors.New("-token and -n are required")
	}
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "prober")
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("http.server.request.duration")
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	now := time.Now()
	for i := range n {
		dp := h.DataPoints().AppendEmpty()
		dp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Second)))
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
		dp.Attributes().PutStr("http.request.method", "GET")
		// letters, not digits: route normalisation folds numeric segments
		// into one route, which the first run of this drill showed
		dp.Attributes().PutStr("http.route", "/drill/cap/"+letters(i))
		dp.Attributes().PutInt("http.response.status_code", 200)
		dp.ExplicitBounds().FromRaw(histo.Bounds[:])
		counts := make([]uint64, histo.Buckets)
		counts[0] = 1
		dp.BucketCounts().FromRaw(counts)
		dp.SetCount(1)
		dp.SetSum(0.001)
	}
	b, err := pmetricotlp.NewExportRequestFromMetrics(md).MarshalProto()
	if err != nil {
		return err
	}
	resp, rb, err := do(c, http.MethodPost, "https://otlp.p17.test/v1/metrics", bytes.NewReader(b), map[string]string{
		"Content-Type": "application/x-protobuf", "Authorization": "Bearer " + token,
	})
	if err != nil {
		return err
	}
	fmt.Printf("otlp: %d (%d routes)\n", resp.StatusCode, n)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("otlp %d %s", resp.StatusCode, rb)
	}
	return nil
}

// letters spells i in base 26 with a to z
func letters(i int) string {
	out := []byte{}
	for {
		out = append([]byte{byte('a' + i%26)}, out...)
		i /= 26
		if i == 0 {
			return string(out)
		}
	}
}
