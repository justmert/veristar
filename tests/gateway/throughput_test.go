//go:build gatewayit && gatewayload

package gatewayit

// the throughput measurement of docs/reference/gateway-limits.md. it is a
// capacity benchmark of one gateway, so it is not part of make gateway-it:
// k6, the gateway and nats share the host, and the result is only a
// measurement of the gateway on a host with cores to spare for the load
// generator (the reference run used 10 cores). github's standard runner for
// private repositories has 2 vcpus, where k6 alone takes about one of them;
// two runs there gave p99 174 ms and 222 ms (e2e runs 43437576423 and
// 43454017432). run it with make gateway-load.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// load shape: 20 sites keep the 200/s per-site bucket of one node above the
// target rate, and one random address per request (10.0.0.0/8) spreads the
// per-prefix buckets, so the run measures the request path, not the limiter.
const (
	loadSites      = 20
	k6Rate         = 2500
	k6Duration     = 30 * time.Second
	loadP99Ceiling = 50 * time.Millisecond
	// the pinned load tool
	k6Image = "grafana/k6:2.3.0@sha256:2dd4756ec6e14e62778916e1eb850108e4b499304c02df56760fa0bdb0fba0b8"
)

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
