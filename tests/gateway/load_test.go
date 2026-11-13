//go:build gatewayit

package gatewayit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

// load shape: 20 sites keep the 200/s per-site bucket of one node above the
// target rate, and one random address per request (10.0.0.0/8) spreads the
// per-prefix buckets, so the run measures the request path, not the limiter.
const (
	loadSites      = 20
	usesPerProof   = 40 // under the replay limit of 50
	k6Rate         = 2500
	k6Duration     = 30 * time.Second
	loadP99Ceiling = 50 * time.Millisecond
)

type loadProof struct {
	Site   string         `json:"site"`
	Origin string         `json:"origin"`
	Proof  gwclient.Proof `json:"proof"`
}

// loadSetup registers the sites and solves enough proofs for n requests.
func loadSetup(t testing.TB, s *Stack, g *Gateway, n, siteCount int) []loadProof {
	t.Helper()
	var sites []Site
	for i := range siteCount {
		sites = append(sites, s.PutSite(t, "", fmt.Sprintf("https://load%03d.sa-tenant.example", i), "active"))
	}
	count := n/usesPerProof + siteCount
	out := make([]loadProof, count)
	var wg sync.WaitGroup
	var next atomic.Int64
	for range 16 {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= count {
					return
				}
				site := sites[i%siteCount]
				c := direct(g, site.Origin, firefoxUA, "192.0.2.1")
				out[i] = loadProof{Site: site.ID, Origin: site.Origin, Proof: *proofFor(t, c, site.ID)}
			}
		})
	}
	wg.Wait()
	return out
}

const k6Script = `
import http from "k6/http";
import { check } from "k6";
import exec from "k6/execution";

const proofs = JSON.parse(open("/load/proofs.json"));
const USES = %d;

export const options = {
  scenarios: {
    events: {
      executor: "constant-arrival-rate",
      rate: %d,
      timeUnit: "1s",
      duration: "%ds",
      preAllocatedVUs: 300,
      maxVUs: 1000,
    },
  },
  thresholds: {
    http_req_failed: ["rate==0"],
    http_req_duration: ["p(99)<%d"],
  },
  summaryTrendStats: ["avg", "med", "p(90)", "p(95)", "p(99)", "max"],
};

export default function () {
  const i = exec.scenario.iterationInTest;
  const p = proofs[Math.floor(i / USES) %% proofs.length];
  const host = p.origin.replace("https://", "");
  const body = JSON.stringify({
    v: 1, w: p.site, t: "pv", h: host, u: "/load/" + (i %% 50), r: "", ti: "load", s: "1920x1080",
    l: "en-US", ts: Date.now(), p: p.proof,
  });
  const o = () => Math.floor(Math.random() * 256);
  const ip = "10." + o() + "." + o() + "." + o();
  const res = http.post("%s/v1/e", body, {
    headers: {
      "Content-Type": "text/plain;charset=UTF-8",
      Origin: p.origin,
      "User-Agent": "k6-load (" + (i %% 7) + ")",
      "%s": ip,
    },
  });
  check(res, { "202": (r) => r.status === 202 });
}
`

// k6Summary is the part of k6's --summary-export file the test reads.
// metric values are numbers; the thresholds entry is an object, which the
// per-field number decoding skips.
type k6Summary struct {
	Metrics map[string]map[string]json.RawMessage `json:"metrics"`
}

// readK6Summary reads the summary a k6 run exported into dir. k6 exits 99
// when a threshold is crossed and still writes the summary, so a run error
// alone is not fatal here: the numbers of a failed run are reported too.
// when the summary is missing or unreadable the run itself failed before
// writing it (docker, image pull, oom, script error), and that run error is
// the cause the test must report, not the missing file.
func readK6Summary(dir string, runErr error) (k6Summary, error) {
	var sum k6Summary
	sb, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		if runErr != nil {
			return sum, fmt.Errorf("k6 run failed before writing its summary: %w (summary: %w)", runErr, err)
		}
		return sum, err
	}
	if err := json.Unmarshal(sb, &sum); err != nil {
		if runErr != nil {
			return sum, fmt.Errorf("k6 run failed with an unreadable summary: %w (summary: %w)", runErr, err)
		}
		return sum, err
	}
	return sum, nil
}

// TestThroughputK6 drives one gateway container with k6 2.3.0 (its docker
// image) at 2,500 events per second for 30 s, every request a full event
// path ending in a jetstream acknowledgement. the result goes into
// docs/reference/gateway-limits.md.
func TestThroughputK6(t *testing.T) {
	s := newStack(t)
	g := s.AddGateway(t, "edge-load", nil)
	n := k6Rate * int(k6Duration/time.Second)
	proofs := loadSetup(t, s, g, n, loadSites)

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(proofs)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(k6Script, usesPerProof, k6Rate, int(k6Duration/time.Second), loadP99Ceiling.Milliseconds(),
		"http://"+g.Node+":8080", clientIPHdr)
	for name, data := range map[string][]byte{"proofs.json": b, "script.js": []byte(script)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mark := lastSeq(t, s.JS)
	out, err := docker("run", "--rm", "--network", s.Net, "-v", dir+":/load", k6Image,
		"run", "--quiet", "--summary-export", "/load/summary.json", "/load/script.js")
	t.Logf("k6 output:\n%s", out)
	runErr := err
	sum, err := readK6Summary(dir, runErr)
	if err != nil {
		t.Fatal(err)
	}
	num := func(metric, field string) float64 {
		var f float64
		if err := json.Unmarshal(sum.Metrics[metric][field], &f); err != nil {
			t.Fatalf("k6 summary %s.%s: %v", metric, field, err)
		}
		return f
	}
	d := map[string]float64{}
	for _, f := range []string{"med", "p(95)", "p(99)", "max"} {
		d[f] = num("http_req_duration", f)
	}
	reqs := map[string]float64{"count": num("http_reqs", "count"), "rate": num("http_reqs", "rate")}
	failed := map[string]float64{"value": num("http_req_failed", "value")}
	t.Logf("k6: %.0f requests, %.1f req/s, failed rate %.4f, latency ms med %.2f p95 %.2f p99 %.2f max %.2f",
		reqs["count"], reqs["rate"], failed["value"], d["med"], d["p(95)"], d["p(99)"], d["max"])
	if runErr != nil {
		t.Fatalf("k6: %v", runErr)
	}
	if reqs["rate"] < 2000 || d["p(99)"] >= float64(loadP99Ceiling.Milliseconds()) || failed["value"] != 0 {
		t.Fatalf("throughput below target: %.1f req/s, p99 %.2f ms, failed %.4f", reqs["rate"], d["p(99)"], failed["value"])
	}
	if q := lastSeq(t, s.JS) - mark; q != uint64(reqs["count"]) {
		t.Fatalf("%d events queued for %.0f accepted requests", q, reqs["count"])
	}
}

// BenchmarkEventPath measures one in-process gateway node (the same
// gateway.Node the binary runs) over loopback http against real nats:
// every iteration is a full POST /v1/e ending in a jetstream
// acknowledgement. run with -bench EventPath -benchtime 20000x.
func BenchmarkEventPath(b *testing.B) {
	s := newStack(b)
	var clk clock
	p := startInProcess(b, s, "edge-bench", clk.Now)
	g := &Gateway{URL: p.URL}
	// unthrottled, one node answers far more than 20 sites' buckets allow,
	// so the benchmark spreads over 400 sites to measure the request path
	proofs := loadSetup(b, s, g, b.N, 400)
	hc := gwclient.New(p.URL, false)
	var idx atomic.Int64
	lat := make([]time.Duration, b.N)
	b.ResetTimer()
	start := time.Now()
	// 20 concurrent clients on the 10 cores: enough to pass 2,000 req/s
	// without queueing every request behind 160 others
	b.SetParallelism(2)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(idx.Add(1)) - 1
			lp := proofs[(i/usesPerProof)%len(proofs)]
			e := gwclient.Pageview(lp.Site, lp.Origin[len("https://"):], fmt.Sprintf("/b/%d", i%50), &lp.Proof)
			h := http.Header{}
			h.Set("Origin", lp.Origin)
			h.Set("User-Agent", fmt.Sprintf("bench (%d)", i%7))
			h.Set(clientIPHdr, fmt.Sprintf("10.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(256)))
			t0 := time.Now()
			status, err := hc.PostEvent(context.Background(), e, h)
			if i < len(lat) {
				lat[i] = time.Since(t0)
			}
			if err != nil || status != http.StatusAccepted {
				b.Errorf("status %d, %v", status, err)
				return
			}
		}
	})
	elapsed := time.Since(start)
	b.StopTimer()
	slices.Sort(lat)
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")
	b.ReportMetric(float64(lat[len(lat)*99/100].Microseconds())/1000, "p99-ms")
	b.ReportMetric(float64(lat[len(lat)/2].Microseconds())/1000, "p50-ms")
}

// TestReadK6SummaryReportsRunError pins the diagnosis order of
// TestThroughputK6: when k6 died before exporting its summary, the failure
// names k6's run error rather than only the missing summary file; a summary
// written by a run that crossed a threshold is still read.
func TestReadK6SummaryReportsRunError(t *testing.T) {
	runErr := errors.New("docker run grafana/k6: exit status 137: oom killed")
	_, err := readK6Summary(t.TempDir(), runErr)
	if !errors.Is(err, runErr) {
		t.Fatalf("missing summary: got %v, want the k6 run error", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "summary.json"),
		[]byte(`{"metrics":{"http_reqs":{"count":3}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := readK6Summary(dir, errors.New("exit status 99"))
	if err != nil {
		t.Fatalf("threshold run with a summary: %v", err)
	}
	if string(sum.Metrics["http_reqs"]["count"]) != "3" {
		t.Fatalf("summary not decoded: %v", sum.Metrics)
	}
}
