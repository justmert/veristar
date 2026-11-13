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

	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// events one load proof carries.
const usesPerProof = 40 // under the replay limit of 50

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
