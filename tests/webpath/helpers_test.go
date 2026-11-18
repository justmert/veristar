//go:build webpath

package webpath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// UmamiEvent is one row of GET /api/websites/<id>/events
// (src/queries/sql/events/getWebsiteEvents.ts).
type UmamiEvent struct {
	ID             string    `json:"id"`
	SessionID      string    `json:"sessionId"`
	VisitID        string    `json:"visitId"`
	CreatedAt      time.Time `json:"createdAt"`
	URLPath        string    `json:"urlPath"`
	ReferrerDomain *string   `json:"referrerDomain"`
	Country        *string   `json:"country"`
	City           *string   `json:"city"`
	Device         *string   `json:"device"`
	OS             *string   `json:"os"`
	Browser        *string   `json:"browser"`
	EventType      int       `json:"eventType"`
	EventName      *string   `json:"eventName"`
}

func (e UmamiEvent) Name() string {
	if e.EventName == nil {
		return ""
	}
	return *e.EventName
}

// umami event types (src/lib/constants.ts EVENT_TYPE)
const (
	umamiPageview    = 1
	umamiCustomEvent = 2
)

// get calls a read route of umami as the admin service account.
func (s *Stack) get(t testing.TB, path string, q url.Values, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sess, err := s.Client.Login(ctx, umami.SeededUsername, s.AdminPW)
	if err != nil {
		t.Fatal(err)
	}
	u := *s.UmamiURL
	u.Path = path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sess.Token.Reveal())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s %s", path, resp.Status, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, b)
	}
}

// Events lists every event of a website in a range, all pages.
func (s *Stack) Events(t testing.TB, websiteID string, r umami.Range) []UmamiEvent {
	t.Helper()
	var all []UmamiEvent
	for page := 1; ; page++ {
		var res struct {
			Data  []UmamiEvent `json:"data"`
			Count int          `json:"count"`
		}
		q := url.Values{}
		q.Set("startAt", strconv.FormatInt(r.Start.UnixMilli(), 10))
		q.Set("endAt", strconv.FormatInt(r.End.UnixMilli(), 10))
		q.Set("pageSize", "500")
		q.Set("page", strconv.Itoa(page))
		s.get(t, "/api/websites/"+websiteID+"/events", q, &res)
		all = append(all, res.Data...)
		if len(res.Data) == 0 || len(all) >= res.Count {
			if len(all) != res.Count {
				t.Fatalf("events of %s: read %d of %d", websiteID, len(all), res.Count)
			}
			return all
		}
	}
}

func (s *Stack) stats(t testing.TB, websiteID string, r umami.Range) umami.StatsValues {
	t.Helper()
	st, err := s.Admin.Stats(context.Background(), websiteID, r)
	if err != nil {
		t.Fatal(err)
	}
	return st.StatsValues
}

func (s *Stack) metrics(t testing.TB, websiteID, typ string, r umami.Range) map[string]int64 {
	t.Helper()
	rows, err := s.Admin.Metrics(context.Background(), websiteID, umami.MetricsQuery{Range: r, Type: typ, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, row := range rows {
		out[row.X] += row.Y
	}
	return out
}

// runStart is when the test binary started. every live read covers the utc
// days from this day on: a run takes over an hour and may cross midnight, and
// a range of the current day alone would then miss the events sent before
// it. umami clamps each read to the website's reset time, so the wider range
// never picks up an earlier scenario's events.
var runStart = time.Now().UTC()

// runRange is the range every live scenario reads: the utc days of the run
// so far, up to an hour ahead (runWindow in days_test.go).
func runRange() umami.Range {
	from, to := runWindow(runStart, time.Now())
	return umami.Range{Start: from, End: to}
}

func dayRange(d time.Time) umami.Range {
	start := d.UTC().Truncate(24 * time.Hour)
	return umami.Range{Start: start, End: start.Add(24*time.Hour - time.Millisecond)}
}

// Drained waits until every replayer consumer (each partition and, when it
// exists, the legacy one) has nothing pending and nothing unacknowledged,
// and returns how long that took.
func (s *Stack) Drained(t testing.TB, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		info := s.consumerInfo(t)
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return time.Since(start)
		}
		if time.Since(start) > timeout {
			t.Fatalf("replayer consumers not drained after %s: pending %d, ack pending %d", timeout, info.NumPending, info.NumAckPending)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// consumerInfo sums the pending and unacknowledged counts of every replayer
// consumer.
func (s *Stack) consumerInfo(t testing.TB) *jetstream.ConsumerInfo {
	t.Helper()
	ctx := context.Background()
	sum := &jetstream.ConsumerInfo{}
	for _, name := range natsx.ReplayerConsumers() {
		c, err := s.JS.Consumer(ctx, natsx.StreamWebEvents, name)
		if name == natsx.ConsumerReplayer && errors.Is(err, jetstream.ErrConsumerNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		info, err := c.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sum.NumPending += info.NumPending
		sum.NumAckPending += info.NumAckPending
	}
	return sum
}

// ResetSites waits for the replayer to drain, resets the umami websites and
// removes the sites' visit continuity, so a scenario starts empty. the
// visit_state keys go too: a cache token of a deleted session must not be
// sent again.
func (s *Stack) ResetSites(t testing.TB, websites map[string][]string) {
	t.Helper()
	s.Drained(t, 5*time.Minute)
	ctx := context.Background()
	kv, err := s.JS.KeyValue(ctx, natsx.BucketVisitState)
	if err != nil {
		t.Fatal(err)
	}
	for site, ids := range websites {
		for _, id := range ids {
			if err := s.Admin.ResetWebsite(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		lister, err := kv.ListKeysFiltered(ctx, "visit."+site+".*")
		if err != nil {
			t.Fatal(err)
		}
		for k := range lister.Keys() {
			if err := kv.Delete(ctx, k); err != nil {
				t.Fatal(err)
			}
		}
		// the lister has already ended once Keys is closed; stopping it then
		// reports the finished subscription as invalid, which is no failure
		if err := lister.Stop(); err != nil && !errors.Is(err, nats.ErrBadSubscription) {
			t.Fatal(err)
		}
	}
	// umami clamps every read to the website's resetAt (src/lib/request.ts),
	// and the replayer sends whole acceptance seconds, so an event accepted
	// in the same second as the reset would be stored but never read. the
	// next scenario starts in a later second.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(2 * time.Second)))
}

// lastSeq is the last stream sequence of WEB_EVENTS.
func (s *Stack) lastSeq(t testing.TB) uint64 {
	t.Helper()
	st, err := s.JS.Stream(context.Background(), natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.LastSeq
}

// queuedAfter reads every message after mark by sequence, which does not
// consume; a message already acknowledged is gone from the work queue and is
// reported as missing.
func (s *Stack) queuedAfter(t testing.TB, mark uint64) (evs []*events.WebEvent, missing int) {
	t.Helper()
	st, err := s.JS.Stream(context.Background(), natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	last := s.lastSeq(t)
	for seq := mark + 1; seq <= last; seq++ {
		m, err := st.GetMsg(context.Background(), seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			missing++
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		ev, err := events.DecodeWebEvent(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	return evs, missing
}

// Publish puts a crafted event on WEB_EVENTS, as the gateway would after
// accepting it, with the given acceptance time.
func (s *Stack) Publish(t testing.TB, ev events.WebEvent) {
	t.Helper()
	id, err := events.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	ev.ID = id
	if err := ev.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := ev.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.PublishDedup(context.Background(), s.JS, natsx.StreamWebEvents, ev.Subject(), ev.ID, b, nil); err != nil {
		t.Fatal(err)
	}
}

// edgeStats drains EDGE_STATS with the jobs consumer (nothing else reads it
// in this stack) and sums counts per site and outcome.
func (s *Stack) edgeStats(t testing.TB) map[[2]string]int64 {
	t.Helper()
	cons, err := s.JS.Consumer(context.Background(), natsx.StreamEdgeStats, natsx.ConsumerJobs)
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
			for _, st := range stats {
				out[[2]string{st.SiteID, st.Outcome}] += st.Count
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

var promLine = regexp.MustCompile(`(?m)^([a-z_]+)(\{[^}]*\})? ([0-9.e+-]+)$`)

// scrape returns every sample of a prometheus endpoint keyed by name plus
// labels, e.g. replayer_outcomes_total{outcome="bot"}. an unreachable
// endpoint returns an error, so a killed replayer can be told apart.
func scrape(u string) (map[string]float64, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(u + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/metrics: %s", u, resp.Status)
	}
	out := map[string]float64{}
	for _, m := range promLine.FindAllStringSubmatch(string(b), -1) {
		v, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return nil, err
		}
		out[m[1]+m[2]] = v
	}
	return out, nil
}

func mustScrape(t testing.TB, u string) map[string]float64 {
	t.Helper()
	m, err := scrape(u)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// replayerSum sums one sample over every running replayer.
func (s *Stack) replayerSum(t testing.TB, sample string) float64 {
	t.Helper()
	sum := 0.0
	for _, r := range s.Replayers {
		if m, err := scrape(r.OpsURL); err == nil {
			sum += m[sample]
		}
	}
	return sum
}

func countBy[T any](xs []T, key func(T) string) map[string]int {
	out := map[string]int{}
	for _, x := range xs {
		out[key(x)]++
	}
	return out
}

func joinKeys(m map[string]int) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%d", k, v))
	}
	return strings.Join(parts, " ")
}

// WaitStored waits until umami lists at least n events of a website in this run
// and returns how long that took after the consumer drained. the replayer
// acknowledges only after umami's 200, so the wait is expected to be zero;
// a gap is logged, because it means umami answered before its rows were
// readable.
func (s *Stack) WaitStored(t testing.TB, website string, n int) {
	t.Helper()
	start := time.Now()
	for {
		got := len(s.Events(t, website, runRange()))
		if got >= n {
			if d := time.Since(start); d > 500*time.Millisecond {
				t.Logf("umami listed %d events of %s only %s after the replayer drained", got, website, d.Round(time.Millisecond))
			}
			return
		}
		if time.Since(start) > 30*time.Second {
			t.Errorf("umami lists %d events of %s 30 s after the replayer drained, want %d", got, website, n)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}
