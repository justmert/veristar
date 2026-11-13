package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

// the browser user agents of the web profile. a few per site keep each
// site's distinct visitors per address prefix under IPCLUSTER_THRESHOLD (20),
// so the load is not quarantined and every event lands in the primary band
// (the ledger checks both bands anyway).
var loadAgents = []string{
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:150.0) Gecko/20100101 Firefox/150.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:150.0) Gecko/20100101 Firefox/150.0",
}

// proofUses stays under PROOF_REPLAY_LIMIT (50) even if every use of a proof
// lands on one node
const proofUses = 40

type latencies struct {
	mu sync.Mutex
	ms []float64
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.ms = append(l.ms, float64(d.Microseconds())/1000)
	l.mu.Unlock()
}

type latencySummary struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

func (l *latencies) summary() latencySummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := slices.Clone(l.ms)
	slices.Sort(s)
	if len(s) == 0 {
		return latencySummary{}
	}
	q := func(p float64) float64 { return s[min(len(s)-1, int(p*float64(len(s))))] }
	return latencySummary{N: len(s), P50: q(0.50), P95: q(0.95), P99: q(0.99), Max: s[len(s)-1]}
}

// counters keeps outcome counts in total and per second of the run
type counters struct {
	mu      sync.Mutex
	samples map[string]string
	start   time.Time
	total   map[string]int64
	series  map[int64]map[string]int64
}

func newCounters(start time.Time) *counters {
	return &counters{start: start, total: map[string]int64{}, series: map[int64]map[string]int64{}, samples: map[string]string{}}
}

// sample keeps the first error text of an outcome, so a failure is
// explained in the ledger and not only counted
func (c *counters) sample(k string, err error) {
	c.mu.Lock()
	if _, ok := c.samples[k]; !ok {
		c.samples[k] = err.Error()
	}
	c.mu.Unlock()
}

func (c *counters) inc(k string) {
	sec := int64(time.Since(c.start) / time.Second)
	c.mu.Lock()
	c.total[k]++
	if c.series[sec] == nil {
		c.series[sec] = map[string]int64{}
	}
	c.series[sec][k]++
	c.mu.Unlock()
}

// failureSeconds lists the seconds of the run in which any outcome other
// than an acknowledgement happened, with those counts
func (c *counters) failureSeconds(ok string) map[string]map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[string]int64{}
	for sec, m := range c.series {
		for k, v := range m {
			if k == ok || k == "failover" || k == "retry" {
				continue
			}
			if out[strconv.FormatInt(sec, 10)] == nil {
				out[strconv.FormatInt(sec, 10)] = map[string]int64{}
			}
			out[strconv.FormatInt(sec, 10)][k] = v
		}
	}
	return out
}

// dnsStandIn plays the health-checked record set: it checks port 80 of
// every address on an interval and answers the healthy ones, or all of them
// when none is healthy, which is what bunny.net documents. it is a stand-in:
// it has no resolver caching, no ttl and no checks from three regions, so
// the removal times it shows are not bunny.net's an operator input.
type dnsStandIn struct {
	all      []string
	interval time.Duration
	healthy  atomic.Pointer[[]string]
	mu       sync.Mutex
	events   []dnsEvent
	start    time.Time
}

type dnsEvent struct {
	AtS     float64 `json:"at_s"`
	Addr    string  `json:"addr"`
	Healthy bool    `json:"healthy"`
}

func newDNS(all []string, interval time.Duration, start time.Time) *dnsStandIn {
	d := &dnsStandIn{all: all, interval: interval, start: start}
	h := slices.Clone(all)
	d.healthy.Store(&h)
	return d
}

func (d *dnsStandIn) run(ctx context.Context) {
	hc := &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	state := map[string]bool{}
	for _, a := range d.all {
		state[a] = true
	}
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var ok []string
		for _, a := range d.all {
			up := false
			if resp, err := httpGet(ctx, hc, "http://"+a+"/"); err == nil {
				_ = resp.Body.Close()
				up = resp.StatusCode < 500
			}
			if up {
				ok = append(ok, a)
			}
			if up != state[a] {
				state[a] = up
				d.mu.Lock()
				d.events = append(d.events, dnsEvent{AtS: time.Since(d.start).Seconds(), Addr: a, Healthy: up})
				d.mu.Unlock()
			}
		}
		if len(ok) == 0 {
			ok = slices.Clone(d.all)
		}
		d.healthy.Store(&ok)
	}
}

func (d *dnsStandIn) answer() []string { return *d.healthy.Load() }

// clientPool keeps one http client per address, as a browser keeps one
// connection pool per resolved address
type clientPool struct {
	mu      sync.Mutex
	clients map[string]*http.Client
	timeout time.Duration
}

func (p *clientPool) get(addr string) *http.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.clients[addr]
	if !ok {
		c = &http.Client{Transport: pinnedTransport(addr, 2*time.Second), Timeout: p.timeout}
		p.clients[addr] = c
	}
	return c
}

func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

type webSite struct {
	site   siteState
	host   string
	mu     sync.Mutex
	proof  *gwclient.Proof
	uses   int
	agents []string
}

type ledger struct {
	Run        string                      `json:"run"`
	Start      time.Time                   `json:"start"`
	End        time.Time                   `json:"end"`
	WebRate    float64                     `json:"web_rate"`
	APIRate    float64                     `json:"api_rate"`
	Web        webLedger                   `json:"web"`
	API        apiLedger                   `json:"api"`
	DNS        []dnsEvent                  `json:"dns_events"`
	WebFailSec map[string]map[string]int64 `json:"web_failure_seconds"`
	APIFailSec map[string]map[string]int64 `json:"api_failure_seconds"`
}

type webLedger struct {
	Outcomes  map[string]int64  `json:"outcomes"`
	Samples   map[string]string `json:"error_samples"`
	Latency   latencySummary    `json:"latency_acked"`
	Acked     []string          `json:"acked_paths"`
	Ambiguous []string          `json:"ambiguous_paths"`
}

type apiLedger struct {
	Service  string            `json:"service"`
	Outcomes map[string]int64  `json:"outcomes"`
	Samples  map[string]string `json:"error_samples"`
	Latency  latencySummary    `json:"latency_acked"`
	// requests carried by acknowledged exports, per tenant
	Acked map[string]int64 `json:"acked_requests"`
	// exports whose outcome is unknown (sent, then no answer); their bytes
	// are deduplicated by content in API_METRICS if a retry was acked
	Ambiguous map[string]int64 `json:"ambiguous_requests"`
}

func runLoad(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	statePath := fs.String("state", "", "state from setup")
	out := fs.String("out", "", "ledger file")
	run := fs.String("run", "", "run name, part of every event path")
	webRate := fs.Float64("web", 500, "web events per second")
	apiRate := fs.Float64("api", 1160, "otlp exports per second, one api request each")
	dur := fs.Duration("duration", 2*time.Minute, "how long to send")
	dnsInterval := fs.Duration("dns-interval", 30*time.Second, "port 80 check interval of the dns stand-in")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	stopFile := fs.String("stop-file", "", "stop sending once this file exists (before -duration)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *run == "" {
		return nil, errors.New("-run is required")
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	var st state
	if err := readJSON(*statePath, &st); err != nil {
		return nil, err
	}
	var addrs []string
	for _, e := range inv.group("edge") {
		addrs = append(addrs, e.Public)
	}
	start := time.Now()
	dns := newDNS(addrs, *dnsInterval, start)
	dctx, dcancel := context.WithCancel(ctx)
	defer dcancel()
	go dns.run(dctx)
	pool := &clientPool{clients: map[string]*http.Client{}, timeout: *timeout}

	lg := &ledger{Run: *run, Start: start, WebRate: *webRate, APIRate: *apiRate}
	webC, apiC := newCounters(start), newCounters(start)
	var webLat, apiLat latencies
	var mu sync.Mutex
	var acked, ambiguous []string
	apiAcked, apiAmb := map[string]int64{}, map[string]int64{}
	service := "drill-" + *run

	sites := make([]*webSite, len(st.Sites))
	for i, s := range st.Sites {
		sites[i] = &webSite{site: s, host: s.Origin[len("https://"):], agents: loadAgents}
	}
	var rr atomic.Uint64

	// one attempt per address of the answer, starting at a rotating offset,
	// moving on only when the connection could not be made: what a browser
	// does with several addresses for one name. the tracker itself never
	// resends an event that got an answer (packages/tracker/src/index.ts).
	webOne := func(seq int64) {
		ws := sites[seq%int64(len(sites))]
		path := fmt.Sprintf("/d/%s/%d", *run, seq)
		agent := ws.agents[(seq/int64(len(sites)))%int64(len(ws.agents))]
		answer := dns.answer()
		off := int(rr.Add(1) % 1024)
		t0 := time.Now()
		for i := range answer {
			addr := answer[(off+i)%len(answer)]
			p, err := ws.nextProof(ctx, pool.get(addr), st.InHost, agent)
			if err != nil {
				if isDialError(err) {
					webC.inc("failover")
					continue
				}
				webC.inc("challenge_error")
				webC.sample("challenge_error", err)
				return
			}
			body, _ := json.Marshal(gwclient.Pageview(ws.site.ID, ws.host, path, &p))
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+st.InHost+"/v1/e", bytes.NewReader(body))
			req.Header.Set("Origin", ws.site.Origin)
			req.Header.Set("User-Agent", agent)
			req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Sec-Fetch-Mode", "cors")
			req.Header.Set("Sec-Fetch-Dest", "empty")
			resp, err := pool.get(addr).Do(req)
			if err != nil {
				if isDialError(err) {
					webC.inc("failover")
					continue
				}
				// sent, no answer: the event may or may not be queued
				webC.inc("no_answer")
				webC.sample("no_answer", err)
				mu.Lock()
				ambiguous = append(ambiguous, path)
				mu.Unlock()
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusAccepted {
				webC.inc("acked")
				webLat.add(time.Since(t0))
				mu.Lock()
				acked = append(acked, path)
				mu.Unlock()
			} else {
				webC.inc("status_" + strconv.Itoa(resp.StatusCode))
			}
			return
		}
		webC.inc("unreachable")
	}

	// otlp exporters retry connection errors and 429, 502, 503 and 504 with
	// backoff (otlp specification), here up to 6 attempts over the answer
	apiOne := func(seq int64) {
		t := st.Tenants[seq%int64(len(st.Tenants))]
		body := otlpBody(service, time.Now())
		t0 := time.Now()
		answer := dns.answer()
		off := int(rr.Add(1) % 1024)
		sent := false
		for attempt := range 6 {
			if attempt > 0 {
				apiC.inc("retry")
				time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
				answer = dns.answer()
			}
			addr := answer[(off+attempt)%len(answer)]
			status, err := postOTLP(ctx, pool.get(addr), st.OTLPHost, t.Token, body)
			if err != nil {
				if !isDialError(err) {
					sent = true
					apiC.sample("no_answer", err)
				}
				continue
			}
			if status == http.StatusOK {
				apiC.inc("acked")
				apiLat.add(time.Since(t0))
				mu.Lock()
				apiAcked[t.ID]++
				mu.Unlock()
				return
			}
			if status != 429 && status != 502 && status != 503 && status != 504 {
				apiC.inc("status_" + strconv.Itoa(status))
				return
			}
		}
		if sent {
			apiC.inc("no_answer")
			mu.Lock()
			apiAmb[t.ID]++
			mu.Unlock()
			return
		}
		apiC.inc("gave_up")
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 6000)
	pace := func(rate float64, kind string, c *counters, f func(int64)) {
		defer wg.Done()
		if rate <= 0 {
			return
		}
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		var seq int64
		end := start.Add(*dur)
		for now := range tick.C {
			if now.After(end) || ctx.Err() != nil {
				return
			}
			if *stopFile != "" && now.UnixMilli()%1000 < 10 {
				if _, err := os.Stat(*stopFile); err == nil {
					return
				}
			}
			due := int64(rate * now.Sub(start).Seconds())
			for ; seq < due; seq++ {
				select {
				case sem <- struct{}{}:
					wg.Add(1)
					go func(s int64) {
						defer wg.Done()
						defer func() { <-sem }()
						f(s)
					}(seq)
				default:
					// the generator itself is saturated; counted, never hidden
					c.inc("generator_overflow_" + kind)
				}
			}
		}
	}
	wg.Add(2)
	go pace(*webRate, "web", webC, webOne)
	go pace(*apiRate, "api", apiC, apiOne)
	wg.Wait()
	lg.End = time.Now()
	dcancel()

	lg.Web = webLedger{Outcomes: webC.total, Samples: webC.samples, Latency: webLat.summary(), Acked: acked, Ambiguous: ambiguous}
	lg.API = apiLedger{Service: service, Outcomes: apiC.total, Samples: apiC.samples, Latency: apiLat.summary(), Acked: apiAcked, Ambiguous: apiAmb}
	dns.mu.Lock()
	lg.DNS = dns.events
	dns.mu.Unlock()
	lg.WebFailSec = webC.failureSeconds("acked")
	lg.APIFailSec = apiC.failureSeconds("acked")
	if err := writeJSON(*out, lg); err != nil {
		return nil, err
	}
	// stdout carries the summary; the paths stay in the ledger file
	sum := *lg
	sum.Web.Acked, sum.Web.Ambiguous = nil, nil
	return sum, nil
}

// nextProof returns a proof for the site, fetching and solving a new
// challenge every proofUses events, through the address the event goes to
func (ws *webSite) nextProof(ctx context.Context, hc *http.Client, inHost, agent string) (gwclient.Proof, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.proof != nil && ws.uses < proofUses && time.Now().Unix()-ws.proof.IAT < 3600 {
		ws.uses++
		return *ws.proof, nil
	}
	gw := gwclient.New("https://"+inHost, true)
	gw.HTTP = hc
	gw.Header.Set("Origin", ws.site.Origin)
	gw.Header.Set("User-Agent", agent)
	// a browser's fetch of the challenge carries fetch metadata, which the
	// gateway requires from browser versions known to send it
	gw.Header.Set("Sec-Fetch-Site", "cross-site")
	gw.Header.Set("Sec-Fetch-Mode", "cors")
	gw.Header.Set("Sec-Fetch-Dest", "empty")
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ch, err := gw.Challenge(cctx, ws.site.ID)
	if err != nil {
		return gwclient.Proof{}, err
	}
	p := gwclient.Solve(ch)
	ws.proof, ws.uses = &p, 1
	return p, nil
}

func postOTLP(ctx context.Context, hc *http.Client, otlpHost, token string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+otlpHost+"/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// otlpBody is one delta http.server.request.duration histogram holding one
// api request, in otlp/json. the nanosecond timestamps make every export's
// bytes unique, so the collector's content-hash message id deduplicates a
// retried export and nothing else.
func otlpBody(service string, now time.Time) []byte {
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	attr := func(k string, v any) map[string]any {
		if x, ok := v.(int); ok {
			return map[string]any{"key": k, "value": map[string]any{"intValue": strconv.Itoa(x)}}
		}
		return map[string]any{"key": k, "value": map[string]any{"stringValue": fmt.Sprint(v)}}
	}
	doc := map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{attr("service.name", service)}},
		"scopeMetrics": []any{map[string]any{
			"scope": map[string]any{"name": "drillctl"},
			"metrics": []any{map[string]any{
				"name": "http.server.request.duration", "unit": "s",
				"histogram": map[string]any{
					"aggregationTemporality": 1,
					"dataPoints": []any{map[string]any{
						"startTimeUnixNano": ns(now.Add(-time.Millisecond)), "timeUnixNano": ns(now),
						"count": "1", "sum": 0.02,
						"explicitBounds": []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
						"bucketCounts":   []string{"0", "0", "1", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"},
						"attributes": []any{
							attr("http.request.method", "GET"), attr("http.route", "/v1/items/{id}"),
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
