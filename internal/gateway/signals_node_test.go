package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/coverage"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// edgeCounts flushes the node's counters and sums every EDGE_STATS message
// by site and outcome.
func (e *nodeEnv) edgeCounts(t *testing.T) map[string]map[string]int64 {
	t.Helper()
	require.NoError(t, e.node.Stats().Flush(t.Context()))
	st, err := e.js.Stream(t.Context(), natsx.StreamEdgeStats)
	require.NoError(t, err)
	info, err := st.Info(t.Context())
	require.NoError(t, err)
	out := map[string]map[string]int64{}
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
		m, err := st.GetMsg(t.Context(), seq)
		require.NoError(t, err)
		stats, err := events.DecodeEdgeStats(m.Data)
		require.NoError(t, err)
		for _, s := range stats {
			if out[s.SiteID] == nil {
				out[s.SiteID] = map[string]int64{}
			}
			out[s.SiteID][s.Outcome] += s.Count
		}
	}
	return out
}

func (e *nodeEnv) queuedCount(t *testing.T) uint64 {
	t.Helper()
	st, err := e.js.Stream(t.Context(), natsx.StreamWebEvents)
	require.NoError(t, err)
	info, err := st.Info(t.Context())
	require.NoError(t, err)
	return info.State.Msgs
}

// postRaw posts a wire body given as a map, for the fields gwclient.Event
// always sends.
func postRaw(t *testing.T, c *gwclient.Client, m map[string]any) int {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	status, _, err := c.Post(t.Context(), b, nil)
	require.NoError(t, err)
	return status
}

func wire13(site, host string, p *gwclient.Proof, edit func(m map[string]any)) map[string]any {
	m := map[string]any{"v": 1, "sv": "1.3.0", "w": site, "t": "pv", "h": host, "u": "/", "r": "",
		"ts": time.Now().UnixMilli(), "p": p}
	if edit != nil {
		edit(m)
	}
	return m
}

// tracker 1.3.0: a page view without title, screen and language is queued
// with the three fields empty, the page signals are counted for the
// integrity grade, a never-visible notice is counted and never queued, and
// older releases add no signal.
func TestPageSignalsOfTracker13(t *testing.T) {
	e := startNode(t, nil)
	s := e.putSite(t, "https://signals.example")
	c := e.client(t, s, "198.51.100.30")
	p := proof(t, c, s.ID)

	require.Equal(t, http.StatusAccepted, postRaw(t, c, wire13(s.ID, s.Host, p, nil)))
	ev := e.lastQueued(t)
	require.Empty(t, ev.Title)
	require.Empty(t, ev.Screen)
	require.Empty(t, ev.Language)
	require.Equal(t, nodeUA, ev.UA, "umami derives browser, os and device from the user agent")

	require.Equal(t, http.StatusAccepted, postRaw(t, c, wire13(s.ID, s.Host, p, func(m map[string]any) { m["fr"] = true })))
	// a framed custom event is accepted but only page views are the base
	require.Equal(t, http.StatusAccepted, postRaw(t, c, wire13(s.ID, s.Host, p, func(m map[string]any) {
		m["t"], m["n"], m["fr"] = "ev", "signup", true
	})))
	// a release before 1.3.0 reports no signal
	require.Equal(t, http.StatusAccepted, post(t, c, gwclient.Pageview(s.ID, s.Host, "/", p), nil))
	// the opted-in title still arrives
	require.Equal(t, http.StatusAccepted, postRaw(t, c, wire13(s.ID, s.Host, p, func(m map[string]any) { m["ti"] = "Docs" })))
	require.Equal(t, "Docs", e.lastQueued(t).Title)
	queued := e.queuedCount(t)

	notice := func(edit func(m map[string]any)) map[string]any {
		m := map[string]any{"v": 1, "sv": "1.3.0", "w": s.ID, "t": "nv", "h": s.Host, "ts": time.Now().UnixMilli(), "p": p}
		if edit != nil {
			edit(m)
		}
		return m
	}
	require.Equal(t, http.StatusAccepted, postRaw(t, c, notice(nil)))
	require.Equal(t, http.StatusAccepted, postRaw(t, c, notice(func(m map[string]any) { m["fr"] = true })))
	require.Equal(t, queued, e.queuedCount(t), "a notice is never queued")
	// a notice carries no page
	require.Equal(t, http.StatusBadRequest, postRaw(t, c, notice(func(m map[string]any) { m["u"] = "/" })))
	// and passes the same checks as an event: a foreign origin is refused
	foreign := gwclient.New(e.url, false)
	foreign.Header = c.Header.Clone()
	foreign.Header.Set("Origin", "https://elsewhere.example")
	require.Equal(t, http.StatusForbidden, postRaw(t, foreign, notice(nil)))

	got := e.edgeCounts(t)[s.ID]
	require.Equal(t, int64(3), got[integrity.SignalViews], "page views of 1.3.0: plain, framed, titled; not the event, not 1.2")
	require.Equal(t, int64(1), got[integrity.SignalFramed])
	require.Equal(t, int64(2), got[integrity.SignalNeverVisible])
	require.Equal(t, int64(5), got["accepted"], "notices are not accepted events")
}

// the coverage counters: a request for sdk.js is attributed to a site by
// the origin of its Referer header, counted once, and again under script_gpc
// when it carries Sec-GPC: 1. a request no single active site claims, or one
// a browser did not make to run a script, is served but not counted.
func TestScriptRequestsAreCountedPerSite(t *testing.T) {
	e := startNode(t, nil)
	s := e.putSite(t, "https://cover.example")
	shared1 := e.putSite(t, "https://shared.example")
	shared2 := e.putSite(t, "https://shared.example")
	pub, err := registry.NewPublisher(t.Context(), e.js)
	require.NoError(t, err)
	wild := uuid.NewString()
	require.NoError(t, pub.PutSite(t.Context(), registry.Site{ID: wild, TenantID: uuid.NewString(), Status: "active",
		Origins: []string{"https://*.wild.example"}, QuarantineWebsiteID: uuid.NewString()}))
	paused := uuid.NewString()
	require.NoError(t, pub.PutSite(t.Context(), registry.Site{ID: paused, TenantID: uuid.NewString(), Status: "paused",
		Origins: []string{"https://paused.example"}, QuarantineWebsiteID: uuid.NewString()}))
	// the registry snapshot has every site once the last one is served
	e.client(t, s, "198.51.100.31")
	time.Sleep(200 * time.Millisecond)

	get := func(referer string, gpc bool, dest string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.url+"/v/1.2.0/sdk.js", nil)
		require.NoError(t, err)
		// caddy sets the client address on every route
		req.Header.Set(nodeClientIPHeader, "198.51.100.31")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		if gpc {
			req.Header.Set("Sec-GPC", "1")
		}
		if dest != "" {
			req.Header.Set("Sec-Fetch-Dest", dest)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
	}
	get("https://cover.example/", false, "script")
	get("https://cover.example/docs/page?x=1", false, "script")
	get("https://cover.example/", true, "script")
	get("https://app.wild.example/", true, "script")
	// not counted: no referer, no fetch metadata, another destination, an
	// origin two sites registered, an unknown origin, a paused site, the
	// wildcard's own host, http for an https origin
	get("", false, "script")
	get("https://cover.example/", true, "")
	get("https://cover.example/", false, "document")
	get("https://shared.example/", false, "script")
	get("https://unknown.example/", false, "script")
	get("https://paused.example/", false, "script")
	get("https://wild.example/", false, "script")
	get("http://cover.example/", false, "script")

	got := e.edgeCounts(t)
	require.Equal(t, map[string]int64{coverage.ScriptRequests: 3, coverage.ScriptGPC: 1}, got[s.ID])
	require.Equal(t, map[string]int64{coverage.ScriptRequests: 1, coverage.ScriptGPC: 1}, got[wild])
	require.Empty(t, got[shared1.ID])
	require.Empty(t, got[shared2.ID])
	require.Empty(t, got[paused])
}

// a client that is not a browser can send notices and framed page views for
// any site it can name. past the volume check of one visitor, and past the
// ip-cluster check of one prefix, they stop counting toward the page
// signals, so the never-visible and framed shares of the grade stop moving.
func TestForgedPageSignalsDoNotMoveTheGrade(t *testing.T) {
	e := startNode(t, map[string]string{"VISITOR_EVENTS_PER_HOUR": "5", "IPCLUSTER_THRESHOLD": "3"})
	s := e.putSite(t, "https://victim.example")
	c := e.client(t, s, "198.51.100.40")
	p := proof(t, c, s.ID)
	notice := map[string]any{"v": 1, "sv": "1.3.0", "w": s.ID, "t": "nv", "h": s.Host, "ts": time.Now().UnixMilli(), "p": p}
	for range 30 {
		require.Equal(t, http.StatusAccepted, postRaw(t, c, notice))
	}
	require.Equal(t, int64(5), e.edgeCounts(t)[s.ID][integrity.SignalNeverVisible],
		"one visitor's notices count up to the volume limit only")

	// a second forger in another network sends framed page views
	f := e.client(t, s, "203.0.113.40")
	pf := proof(t, f, s.ID)
	for range 30 {
		require.Equal(t, http.StatusAccepted, postRaw(t, f, wire13(s.ID, s.Host, pf, func(m map[string]any) { m["fr"] = true })))
	}
	got := e.edgeCounts(t)[s.ID]
	require.Equal(t, int64(5), got[integrity.SignalViews], "quarantined views are not part of the signal base")
	require.Equal(t, int64(5), got[integrity.SignalFramed], "quarantined views never count as framed")
	require.Equal(t, int64(25), got["quarantined"])

	// rotating the user agent from one prefix makes new visitors, which the
	// ip-cluster check catches after its threshold
	r := e.client(t, s, "192.0.2.40")
	pr := proof(t, r, s.ID)
	before := e.edgeCounts(t)[s.ID][integrity.SignalNeverVisible]
	for i := range 20 {
		r.Header.Set("User-Agent", fmt.Sprintf("%s rotate/%d", nodeUA, i))
		require.Equal(t, http.StatusAccepted, postRaw(t, r, map[string]any{"v": 1, "sv": "1.3.0", "w": s.ID, "t": "nv",
			"h": s.Host, "ts": time.Now().UnixMilli(), "p": pr}))
	}
	after := e.edgeCounts(t)[s.ID][integrity.SignalNeverVisible]
	require.LessOrEqual(t, after-before, int64(3), "notices from one prefix count up to the ip-cluster threshold only")
	require.Equal(t, uint64(30), e.queuedCount(t), "the framed views are queued, notices never")
}

// a client that forges Referer, Sec-Fetch-Dest and Sec-GPC is served the
// file every time, but one prefix adds at most the script count burst to a
// site's coverage counters, so it cannot push the unmeasured share toward 0%
// or 100%. another prefix has its own bucket.
func TestForgedScriptRequestsAreCountedWithinAPrefixLimit(t *testing.T) {
	e := startNode(t, nil)
	s := e.putSite(t, "https://cover.example")
	e.client(t, s, "198.51.100.50")
	get := func(addr string, gpc bool) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.url+"/v/1.2.0/sdk.js", nil)
		require.NoError(t, err)
		req.Header.Set(nodeClientIPHeader, addr)
		req.Header.Set("Referer", "https://cover.example/")
		req.Header.Set("Sec-Fetch-Dest", "script")
		if gpc {
			req.Header.Set("Sec-GPC", "1")
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode, "the file is always served")
	}
	burst := int64(gateway.DefaultScriptCountRate.Burst)
	for range 3 * burst {
		get("203.0.113.50", true)
	}
	got := e.edgeCounts(t)[s.ID]
	// the bucket refills one token every ten seconds while the loop runs
	require.GreaterOrEqual(t, got[coverage.ScriptGPC], burst)
	require.LessOrEqual(t, got[coverage.ScriptGPC], burst+1)
	require.Equal(t, got[coverage.ScriptGPC], got[coverage.ScriptRequests])
	// an address in the same /24 shares the bucket
	get("203.0.113.51", false)
	require.Equal(t, got[coverage.ScriptRequests], e.edgeCounts(t)[s.ID][coverage.ScriptRequests])
	// another network counts on its own
	get("192.0.2.50", false)
	require.Equal(t, got[coverage.ScriptRequests]+1, e.edgeCounts(t)[s.ID][coverage.ScriptRequests])
}
