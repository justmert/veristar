package replayer_test

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// setUmamiReadOnly makes every new session of umami's database read-only
// and ends the open ones, so umami's prisma pool reconnects read-only: the
// website lookup of the collection probe still works, every insert fails.
// that is a real write fault (a connection to a read-only node), not a
// stand-in.
func setUmamiReadOnly(t *testing.T, pg *testcontainers.DockerContainer, on bool) {
	t.Helper()
	val := "off"
	if on {
		val = "on"
	}
	for _, sql := range []string{
		"ALTER DATABASE umami SET default_transaction_read_only = " + val,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'umami' AND pid <> pg_backend_pid()",
	} {
		code, out, err := pg.Exec(context.Background(), []string{"psql", "-U", "umami", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(out)
		if code != 0 {
			t.Fatalf("psql %q: exit %d: %s", sql, code, b)
		}
	}
}

// N-1: umami answers the collection probe (a read) while it cannot write.
// before the fix every event then counted as failing on a healthy umami and
// went to WEB_DLQ after about 36 s. now the failures of many visitors are
// recognised as a write fault: nothing is dead-lettered, and once writes
// work again every event is stored exactly once.
func TestWriteFaultIsNotDeadLettered(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a dedicated umami")
	}
	env := startNATS(t)
	stack := startUmamiStack(t)
	s := newSite(t, stack.Admin)
	c, err := umami.New(stack.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	rp := startReplayerTo(t, env.srv.URL, []*url.URL{stack.URL}, "it-writefault", 4, 64)
	waitFor(t, 30*time.Second, "replayer to lead", func() bool { return scrapeOK(rp.ops)["replayer_leader"] == 1 })

	setUmamiReadOnly(t, stack.pg, true)
	t.Cleanup(func() { setUmamiReadOnly(t, stack.pg, false) })
	// the probe a replayer runs must pass, or this would test an outage
	waitFor(t, 30*time.Second, "the collection probe to pass on a read-only database", func() bool {
		return c.Healthy(testCtx(t, 10*time.Second)) == nil
	})
	if _, err := c.Send(testCtx(t, 10*time.Second), umami.SendRequest{
		Event:     umami.Event{Website: s.ID, Hostname: "rp.example", URL: "/check", Timestamp: time.Now().Unix(), Salt: newVT(t)},
		UserAgent: desktopUA,
	}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a send on the read-only database answered %v, want a 500", err)
	}

	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	const visitors, each = 8, 3
	var evs []*events.WebEvent
	for v := range visitors {
		vt := newVT(t)
		for i := range each {
			evs = append(evs, pageview(t, s, vt, fmt.Sprintf("/v%d/%d", v, i), at.Add(time.Duration(v*each+i)*time.Second)))
		}
	}
	publish(t, env.js, evs...)

	// well past the ~36 s after which a failing event on a healthy umami
	// is dead-lettered
	time.Sleep(50 * time.Second)
	if n := len(streamMsgs(t, env.js, natsx.StreamWebDLQ)); n != 0 {
		t.Fatalf("%d events dead-lettered during the write fault", n)
	}
	if got := scrape(t, rp.ops)["replayer_write_fault_retries_total"]; got == 0 {
		t.Fatal("no retry was attributed to the write fault")
	}

	setUmamiReadOnly(t, stack.pg, false)
	start := time.Now()
	waitFor(t, 2*time.Minute, "stream drained after the fault", func() bool { return drained(t, env.js) })
	t.Logf("drained %.1f s after writes came back", time.Since(start).Seconds())
	if n := len(streamMsgs(t, env.js, natsx.StreamWebDLQ)); n != 0 {
		t.Fatalf("%d events dead-lettered", n)
	}
	r := umami.Range{Start: at.Add(-time.Minute), End: at.Add(time.Hour)}
	st, err := stack.Admin.Stats(testCtx(t, time.Minute), s.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != visitors*each || st.Visitors != visitors {
		t.Fatalf("stored %d pageviews of %d visitors, want %d of %d", st.Pageviews, st.Visitors, visitors*each, visitors)
	}
}
