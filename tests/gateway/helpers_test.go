//go:build gatewayit

package gatewayit

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

func httpsClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: gatewayHost},
	}}
}

// direct returns a client that talks to the gateway's public port as caddy
// would, with the client address header set to addr. it sends the fetch
// metadata a browser sends on the tracker's requests, so the browser user
// agents the tests use pass the signals check.
func direct(g *Gateway, origin, agent, addr string) *gwclient.Client {
	return browserLike(g.URL, origin, agent, addr)
}

func browserLike(base, origin, agent, addr string) *gwclient.Client {
	c := gwclient.New(base, false)
	c.Header.Set("Origin", origin)
	c.Header.Set("User-Agent", agent)
	c.Header.Set(clientIPHdr, addr)
	c.Header.Set("Sec-Fetch-Site", "cross-site")
	c.Header.Set("Sec-Fetch-Mode", "cors")
	c.Header.Set("Sec-Fetch-Dest", "empty")
	return c
}

// waitSite waits until the gateway's registry snapshot has the site, by
// asking for a challenge until it is issued.
func waitSite(t testing.TB, c *gwclient.Client, site string) gwclient.Challenge {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ch, err := c.Challenge(context.Background(), site)
		if err == nil {
			return ch
		}
		if time.Now().After(deadline) {
			t.Fatalf("site %s never served a challenge: %v", site, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// proofFor fetches and solves one challenge.
func proofFor(t testing.TB, c *gwclient.Client, site string) *gwclient.Proof {
	t.Helper()
	p := gwclient.Solve(waitSite(t, c, site))
	return &p
}

// queued is one WEB_EVENTS message: the raw bytes and the decoded event.
type queued struct {
	Raw   []byte
	Event *events.WebEvent
}

// lastSeq is the last stream sequence of WEB_EVENTS, the mark a test reads
// from.
func lastSeq(t testing.TB, js jetstream.JetStream) uint64 {
	t.Helper()
	st, err := js.Stream(context.Background(), natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.LastSeq
}

// queuedAfter waits until at least want messages follow mark, then returns
// every message after mark. it reads by sequence, which does not consume.
func queuedAfter(t testing.TB, js jetstream.JetStream, mark uint64, want int) []queued {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		last := lastSeq(t, js)
		if int(last-mark) >= want || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, err := js.Stream(context.Background(), natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	last := lastSeq(t, js)
	var out []queued
	for seq := mark + 1; seq <= last; seq++ {
		m, err := st.GetMsg(context.Background(), seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		ev, err := events.DecodeWebEvent(m.Data)
		if err != nil {
			t.Fatalf("seq %d is not a valid WebEvent: %v", seq, err)
		}
		if m.Header.Get(jetstream.MsgIDHeader) != ev.ID {
			t.Fatalf("seq %d: Nats-Msg-Id %q differs from event id %s", seq, m.Header.Get(jetstream.MsgIDHeader), ev.ID)
		}
		out = append(out, queued{Raw: m.Data, Event: ev})
	}
	return out
}

var metricLine = regexp.MustCompile(`(?m)^gateway_responses_total\{outcome="([a-z_]+)",route="([a-z]+)"\} ([0-9.e+]+)$`)

// responses scrapes gateway_responses_total for one route.
func responses(t testing.TB, g *Gateway, route string) map[string]float64 {
	t.Helper()
	resp, err := http.Get(g.OpsURL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, m := range metricLine.FindAllStringSubmatch(string(b), -1) {
		if m[2] == route {
			v, err := strconv.ParseFloat(m[3], 64)
			if err != nil {
				t.Fatal(err)
			}
			out[m[1]] = v
		}
	}
	return out
}

// edgeStats drains EDGE_STATS with the jobs consumer and sums counts per
// site and outcome. tests that check counters own their stack, so nothing
// else reads the stream.
func edgeStats(t testing.TB, js jetstream.JetStream) map[[2]string]int64 {
	t.Helper()
	cons, err := js.Consumer(context.Background(), natsx.StreamEdgeStats, natsx.ConsumerJobs)
	if err != nil {
		t.Fatal(err)
	}
	out := map[[2]string]int64{}
	for {
		batch, err := cons.FetchNoWait(100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for m := range batch.Messages() {
			n++
			stats, err := events.DecodeEdgeStats(m.Data())
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range stats {
				out[[2]string{s.SiteID, s.Outcome}] += s.Count
			}
			if err := m.Ack(); err != nil {
				t.Fatal(err)
			}
		}
		if batch.Error() != nil {
			t.Fatal(batch.Error())
		}
		if n == 0 {
			return out
		}
	}
}
