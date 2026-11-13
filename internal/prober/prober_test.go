package prober

import (
	"crypto/sha256"
	"strconv"
	"testing"
	"time"
)

func TestGatusKey(t *testing.T) {
	// expectations follow gatus 5.37.0 config/key/key_test.go
	cases := map[[2]string]string{
		{"slo", "freshness web"}:              "slo_freshness-web",
		{"nodes", "edge-1 in.example.org"}:    "nodes_edge-1-in-example-org",
		{"Core", "Front_End/ui #1 + api & x"}: "core_front-end-ui--1---api---x",
	}
	for in, want := range cases {
		if got := GatusKey(in[0], in[1]); got != want {
			t.Errorf("GatusKey(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestSolve(t *testing.T) {
	c := challenge{W: "site", IAT: 1700000000, Salt: "s", Bits: 12, Tag: "t"}
	p, err := solve(c)
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Sum256([]byte("site|1700000000|s|" + strconv.FormatUint(p.Nonce, 10)))
	if leadingZeroBits(d[:]) < 12 {
		t.Fatalf("nonce %d does not give 12 zero bits", p.Nonce)
	}
	if _, err := solve(challenge{Bits: 40}); err == nil {
		t.Fatal("a 40 bit challenge was accepted")
	}
}

func TestFreshness(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 20, 0, 0, time.UTC)
	f := Freshness{MaxDelay: time.Minute, Window: 20 * time.Hour}
	// sends before the first baseline are not tracked
	f.Sent(t0.Add(-time.Second))
	from := f.From(t0)
	if !from.Equal(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("first from %s", from)
	}
	if v := f.Observe(t0, from, 40); !v.Baseline || !v.Fresh {
		t.Fatalf("baseline %+v", v)
	}
	f.Sent(t0.Add(10 * time.Second))
	f.Sent(t0.Add(40 * time.Second))
	// at t0+75s only the first send is due
	if v := f.Observe(t0.Add(75*time.Second), f.From(t0), 40); v.Fresh || v.Expected != 41 || v.Backlog() != 1 {
		t.Fatalf("one due and not visible: %+v", v)
	}
	if v := f.Observe(t0.Add(75*time.Second), f.From(t0), 41); !v.Fresh {
		t.Fatalf("one due and visible: %+v", v)
	}
	// duplicates only raise the count
	if v := f.Observe(t0.Add(2*time.Minute), f.From(t0), 45); !v.Fresh || v.Expected != 42 {
		t.Fatalf("both due, more visible: %+v", v)
	}
	// past the window the next read starts a new baseline
	later := t0.Add(21 * time.Hour)
	nf := f.From(later)
	if !nf.Equal(later.Truncate(time.Hour).Add(-time.Hour)) {
		t.Fatalf("rebase from %s", nf)
	}
	if v := f.Observe(later, nf, 3); !v.Baseline {
		t.Fatalf("rebase %+v", v)
	}
}

func TestParseNodes(t *testing.T) {
	n, err := ParseNodes("edge-1@203.0.113.1:443=in.x.org,otlp.x.org; core-1@203.0.113.9:443=app.x.org")
	if err != nil || len(n) != 2 || len(n[0].Hosts) != 2 || n[1].Address != "203.0.113.9:443" {
		t.Fatalf("%+v %v", n, err)
	}
	for _, bad := range []string{"", "edge-1=in.x.org", "a@1.2.3.4=x", "a@1.2.3.4:443=x;a@1.2.3.5:443=y"} {
		if _, err := ParseNodes(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOTLPRequest(t *testing.T) {
	b, err := otlpRequest(time.Unix(100, 0), time.Unix(130, 0))
	if err != nil || len(b) == 0 {
		t.Fatalf("%d bytes, %v", len(b), err)
	}
}
