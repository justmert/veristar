package replayer_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// poisonValue is an event data string that umami 3.5.0 cannot store: it
// matches umami's date pattern, so umami writes it to the timestamptz column
// event_data.date_value, and postgres refuses year 0 with 22008. umami has
// already inserted the website_event row by then and answers 500, while its
// heartbeat and every other event stay fine. the gateway refuses this value
// now, so here it stands for an event queued by an older gateway or a
// failure nobody has found yet.
const poisonValue = "0000-01-01T00:00:00Z"

// a poison event on a healthy umami is dead-lettered after a bounded
// number of attempts, and while it is retried another site's traffic, and
// even another visitor of the same site, flows through. one delivery slot
// (REPLAYER_WORKERS=1) and a small in-flight bound are the worst case: with
// ordered lanes the single lane held everything behind the poison event.
func TestPoisonEventDoesNotStallOtherSites(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	env := startNATS(t)
	stack := startUmamiStack(t)
	rp := startReplayerTo(t, env.srv.URL, []*url.URL{stack.URL}, "it-poison", 1, 64)
	waitFor(t, 30*time.Second, "replayer to lead", func() bool { return scrapeOK(rp.ops)["replayer_leader"] == 1 })

	a, b := newSite(t, stack.Admin), newSite(t, stack.Admin)
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	poisoned, other := newVT(t), newVT(t)

	poison := pageview(t, a, poisoned, "/poison", at)
	poison.Type, poison.Name, poison.Data = events.TypeEvent, "poison", map[string]any{"d": poisonValue}
	evs := []*events.WebEvent{poison}
	// the poisoned visitor's later events must wait for it and keep their order
	for i := range 3 {
		evs = append(evs, pageview(t, a, poisoned, fmt.Sprintf("/after/%d", i), at.Add(time.Duration(i+1)*time.Second)))
	}
	// another visitor of the same site
	evs = append(evs, pageview(t, a, other, "/a-other", at.Add(time.Second)))
	// normal traffic of another site, published behind the poison event
	const n = 200
	vts := make([]string, 20)
	for i := range vts {
		vts[i] = newVT(t)
	}
	for i := range n {
		evs = append(evs, pageview(t, b, vts[i%len(vts)], fmt.Sprintf("/b/%d", i), at.Add(time.Duration(i)*time.Second)))
	}
	start := time.Now()
	publish(t, env.js, evs...)

	r := umami.Range{Start: at.Add(-time.Minute), End: at.Add(time.Hour)}
	// the poison event is retried for about 36 s; the other site must be
	// complete well before that
	waitFor(t, 20*time.Second, "the other site's pageviews while the poison event is retried", func() bool {
		st, err := stack.Admin.Stats(testCtx(t, time.Minute), b.ID, r)
		return err == nil && st.Pageviews == n
	})
	t.Logf("other site complete %.1f s after publishing", time.Since(start).Seconds())
	waitFor(t, 5*time.Second, "the other visitor of the poisoned site", func() bool {
		return pathCounts(t, stack.Admin, a.ID, r, 10)["/a-other"] == 1
	})
	if pathCounts(t, stack.Admin, a.ID, r, 10)["/after/0"] != 0 {
		t.Fatal("a later event of the poisoned visitor overtook the poison event")
	}

	// the poison event ends in WEB_DLQ with the reason, the visitor's later
	// events follow it, and the stream drains
	waitFor(t, 2*time.Minute, "stream drained", func() bool { return drained(t, env.js) })
	t.Logf("drained %.1f s after publishing", time.Since(start).Seconds())
	var reasons []string
	for _, m := range streamMsgs(t, env.js, natsx.StreamWebDLQ) {
		if m.Subject == natsx.Subject(natsx.SubjectWebDLQ, a.ID) {
			reasons = append(reasons, m.Header.Get(natsx.HeaderDLQReason))
		}
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "health checks passed") || !strings.Contains(reasons[0], "500") {
		t.Fatalf("dlq reasons for the poisoned site: %q", reasons)
	}
	paths := pathCounts(t, stack.Admin, a.ID, r, 10)
	for i := range 3 {
		if paths[fmt.Sprintf("/after/%d", i)] != 1 {
			t.Errorf("/after/%d stored %d times", i, paths[fmt.Sprintf("/after/%d", i)])
		}
	}
	// umami inserts the website_event row before the failing event_data row,
	// so each attempt left one custom event behind: bounded now, where the
	// in-place retry until the 72 h max age left about 430
	rows, err := stack.Admin.Metrics(testCtx(t, time.Minute), a.ID, umami.MetricsQuery{Range: r, Type: "event", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var stored int64
	for _, row := range rows {
		if row.X == "poison" {
			stored = row.Y
		}
	}
	if stored < 1 || stored > 4 {
		t.Errorf("poison event stored %d times, want 1 to 4 (one per attempt)", stored)
	}
	if st, err := stack.Admin.Stats(testCtx(t, time.Minute), b.ID, r); err != nil || st.Pageviews != n || st.Visitors != int64(len(vts)) {
		t.Errorf("other site: %+v %v", st, err)
	}
}

// TestStorableVectorsAgainstUmami sends every case of the shared storable
// vectors to the pinned umami image: the cases tracker and gateway refuse
// are exactly the ones umami answers with an error, and the ones they let
// through are stored.
func TestStorableVectorsAgainstUmami(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	stack := startUmamiStack(t)
	s := newSite(t, stack.Admin)
	raw, err := os.ReadFile(filepath.Join(natsctr.RepoRoot(t), "packages", "tracker", "test", "vectors", "storable-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Data []struct {
			Data json.RawMessage `json:"data"`
			OK   bool            `json:"ok"`
			// UmamiStores marks a value tracker and gateway refuse although
			// umami would store it (the date rule is deliberately broad)
			UmamiStores bool `json:"umamiStores"`
		} `json:"data"`
		Names []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"names"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	c, err := umami.New(stack.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	send := func(name string, data map[string]any) error {
		return func() error {
			_, err := c.Send(testCtx(t, time.Minute), umami.SendRequest{
				Event: umami.Event{Website: s.ID, Hostname: "rp.example", URL: "/v", Name: name, Data: data,
					Timestamp: time.Now().Unix(), Salt: newVT(t)},
				UserAgent: desktopUA,
			})
			return err
		}()
	}
	for _, v := range file.Data {
		dec := json.NewDecoder(bytes.NewReader(v.Data))
		dec.UseNumber()
		var data map[string]any
		if err := dec.Decode(&data); err != nil {
			t.Fatal(err)
		}
		err := send("x", data)
		if (v.OK || v.UmamiStores) != (err == nil) {
			t.Errorf("data %s: ok %v, umami answered %v", v.Data, v.OK, err)
		}
	}
	for _, v := range file.Names {
		err := send(v.Name, nil)
		if v.OK != (err == nil) {
			t.Errorf("name %q: ok %v, umami answered %v", v.Name, v.OK, err)
		}
	}
}
