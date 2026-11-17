package gateway_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

const (
	nodeClientIPHeader = "X-Edge-Client-Ip"
	nodeUA             = "Mozilla/5.0 (X11; Linux x86_64; rv:157.0) Gecko/20100101 Firefox/157.0"
)

// nodeEnv is a whole gateway.Node in this process against a nats server
// with the topology applied, built as cmd/gateway builds it.
type nodeEnv struct {
	js       jetstream.JetStream
	url      string
	releases string
	node     *gateway.Node
	reg      *prometheus.Registry
}

// nodeDBIP is the db-ip file of make geo-fetch, the same one the geo tests
// use.
func nodeDBIP(t *testing.T) string {
	t.Helper()
	p := os.Getenv("DBIP_MMDB")
	if p == "" {
		p = ".scratch/geo/dbip-country-lite.mmdb"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(natsctr.RepoRoot(t), p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("db-ip file %s missing: run make geo-fetch", p)
	}
	return p
}

// copyReleases copies the committed tracker releases, so a test can revoke a
// version without touching the repository.
func copyReleases(t *testing.T) string {
	t.Helper()
	src := filepath.Join(natsctr.RepoRoot(t), "packages", "tracker", "releases")
	dst := t.TempDir()
	require.NoError(t, os.CopyFS(dst, os.DirFS(src)))
	return dst
}

func startNode(t *testing.T, overrides map[string]string) *nodeEnv {
	t.Helper()
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), js, spec)
	require.NoError(t, err)
	env := map[string]string{
		"GATEWAY_NODE_ID": "node-test", "GATEWAY_ADDR": "127.0.0.1:18080", "GATEWAY_OPS_ADDR": "127.0.0.1:18081",
		"PROOF_BITS": "4", "PROOF_REPLAY_LIMIT": "50", "IPCLUSTER_THRESHOLD": "20",
		"RATE_SITE_PER_SECOND": "200", "RATE_SITE_BURST": "400", "RATE_PREFIX_PER_SECOND": "20",
		"RATE_PREFIX_BURST": "60", "VISITOR_EVENTS_PER_HOUR": "600", "GATEWAY_MAX_BODY": "8192",
		"CLIENT_IP_HEADER": nodeClientIPHeader, "GATEWAY_SECURE_TRANSPORT": "true",
		"DBIP_MMDB": nodeDBIP(t), "TRACKER_RELEASE_DIR": copyReleases(t),
	}
	for k, v := range overrides {
		env[k] = v
	}
	cfg, err := config.LoadGateway(config.FromMap(env))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	reg := obs.NewRegistry()
	n, err := gateway.NewNode(ctx, gateway.NodeConfig{Gateway: cfg, NC: nc, Replicas: 1,
		Log: obs.NewLogger(os.Stderr, slog.LevelWarn), Registry: reg})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	hs := httptest.NewServer(n.Handler())
	t.Cleanup(func() {
		hs.Close()
		cancel()
		<-done
	})
	return &nodeEnv{js: js, url: hs.URL, releases: cfg.ReleaseDir, node: n, reg: reg}
}

// counter sums the node's counter name over the series whose labels include
// every pair of labels
func (e *nodeEnv) counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := e.reg.Gather()
	require.NoError(t, err)
	var sum float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	series:
		for _, m := range mf.GetMetric() {
			have := map[string]string{}
			for _, lp := range m.GetLabel() {
				have[lp.GetName()] = lp.GetValue()
			}
			for k, v := range labels {
				if have[k] != v {
					continue series
				}
			}
			sum += m.GetCounter().GetValue()
		}
	}
	return sum
}

type nodeSite struct {
	ID, Quarantine, Origin, Host string
}

func (e *nodeEnv) putSite(t *testing.T, origin string) nodeSite {
	t.Helper()
	p, err := registry.NewPublisher(t.Context(), e.js)
	require.NoError(t, err)
	s := nodeSite{ID: uuid.NewString(), Quarantine: uuid.NewString(), Origin: origin}
	s.Host = origin[len("https://"):]
	require.NoError(t, p.PutSite(t.Context(), registry.Site{ID: s.ID, TenantID: uuid.NewString(), Status: "active",
		Origins: []string{origin}, QuarantineWebsiteID: s.Quarantine}))
	return s
}

// client is a browser-like client from one address, waiting until the
// node's registry snapshot has the site.
func (e *nodeEnv) client(t *testing.T, s nodeSite, addr string) *gwclient.Client {
	t.Helper()
	c := gwclient.New(e.url, false)
	c.Header.Set("Origin", s.Origin)
	c.Header.Set("User-Agent", nodeUA)
	c.Header.Set(nodeClientIPHeader, addr)
	c.Header.Set("Sec-Fetch-Site", "cross-site")
	c.Header.Set("Sec-Fetch-Mode", "cors")
	c.Header.Set("Sec-Fetch-Dest", "empty")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := c.Challenge(t.Context(), s.ID); err == nil {
			return c
		} else if time.Now().After(deadline) {
			t.Fatalf("site never served: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func proof(t *testing.T, c *gwclient.Client, site string) *gwclient.Proof {
	t.Helper()
	ch, err := c.Challenge(t.Context(), site)
	require.NoError(t, err)
	p := gwclient.Solve(ch)
	return &p
}

// lastQueued decodes the newest WEB_EVENTS message.
func (e *nodeEnv) lastQueued(t *testing.T) *events.WebEvent {
	t.Helper()
	st, err := e.js.Stream(t.Context(), natsx.StreamWebEvents)
	require.NoError(t, err)
	info, err := st.Info(t.Context())
	require.NoError(t, err)
	require.NotZero(t, info.State.LastSeq, "nothing queued")
	m, err := st.GetMsg(t.Context(), info.State.LastSeq)
	require.NoError(t, err)
	ev, err := events.DecodeWebEvent(m.Data)
	require.NoError(t, err)
	return ev
}

func post(t *testing.T, c *gwclient.Client, e gwclient.Event, extra http.Header) int {
	t.Helper()
	status, err := c.PostEvent(t.Context(), e, extra)
	require.NoError(t, err)
	return status
}

// events refused after the proof check (here a missing user agent) do
// not use up the proof's allowance; only queued events count.
func TestRefusedEventsKeepTheProofAllowance(t *testing.T) {
	e := startNode(t, map[string]string{"PROOF_REPLAY_LIMIT": "3"})
	s := e.putSite(t, "https://shop.example")
	c := e.client(t, s, "198.51.100.7")
	p := proof(t, c, s.ID)
	noUA := gwclient.New(e.url, false)
	noUA.Header = c.Header.Clone()
	noUA.Header.Del("User-Agent")
	// go's client sends its own user agent unless the header is set empty
	noUA.Header["User-Agent"] = []string{""}
	for range 3 {
		require.Equal(t, http.StatusBadRequest, post(t, noUA, gwclient.Pageview(s.ID, s.Host, "/", p), nil))
	}
	for i := range 3 {
		require.Equal(t, http.StatusAccepted, post(t, c, gwclient.Pageview(s.ID, s.Host, "/", p), nil), "use %d", i+1)
	}
	require.Equal(t, http.StatusForbidden, post(t, c, gwclient.Pageview(s.ID, s.Host, "/", p), nil))
}

// the queued hostname is the host of the checked Origin, whatever the
// body says.
func TestHostnameComesFromTheOrigin(t *testing.T) {
	e := startNode(t, nil)
	s := e.putSite(t, "https://shop.example:8443")
	c := e.client(t, s, "198.51.100.8")
	ev := gwclient.Pageview(s.ID, "evil.example", "/", proof(t, c, s.ID))
	require.Equal(t, http.StatusAccepted, post(t, c, ev, nil))
	require.Equal(t, "shop.example", e.lastQueued(t).Hostname)
}

// an event that names a revoked tracker release is refused with 410,
// the answer sdk.js of that release gets; other releases and events without
// a release name pass.
func TestRevokedReleaseEventsAreRefused(t *testing.T) {
	e := startNode(t, nil)
	revoked := `{"revoked":[{"version":"1.0.0","reason":"test"}]}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(e.releases, "revoked.json"), []byte(revoked), 0o600))
	// the node read revoked.json at start; a reload is what an operator runs
	require.NoError(t, e.node.Reload())
	s := e.putSite(t, "https://shop.example")
	c := e.client(t, s, "198.51.100.9")
	p := proof(t, c, s.ID)
	ev := gwclient.Pageview(s.ID, s.Host, "/", p)
	ev.SV = "1.0.0"
	require.Equal(t, http.StatusGone, post(t, c, ev, nil))
	ev.SV = "1.1.0"
	require.Equal(t, http.StatusAccepted, post(t, c, ev, nil))
	ev.SV = ""
	require.Equal(t, http.StatusAccepted, post(t, c, ev, nil))
	ev.SV = "1.0"
	require.Equal(t, http.StatusBadRequest, post(t, c, ev, nil))
}

// one visitor (one address and one user agent) past
// VISITOR_EVENTS_PER_HOUR goes to the quarantine band.
func TestOneVisitorsVolumeIsQuarantined(t *testing.T) {
	e := startNode(t, map[string]string{"VISITOR_EVENTS_PER_HOUR": "5"})
	s := e.putSite(t, "https://shop.example")
	c := e.client(t, s, "198.51.100.10")
	p := proof(t, c, s.ID)
	for i := range 5 {
		require.Equal(t, http.StatusAccepted, post(t, c, gwclient.Pageview(s.ID, s.Host, "/", p), nil))
		require.Equal(t, events.BandPrimary, e.lastQueued(t).Band, "event %d", i+1)
	}
	require.Equal(t, http.StatusAccepted, post(t, c, gwclient.Pageview(s.ID, s.Host, "/", p), nil))
	last := e.lastQueued(t)
	require.Equal(t, events.BandQuarantine, last.Band)
	require.Equal(t, s.Quarantine, last.WebsiteID)
	// another visitor from another address is not affected
	other := e.client(t, s, "203.0.113.10")
	require.Equal(t, http.StatusAccepted, post(t, other, gwclient.Pageview(s.ID, s.Host, "/", proof(t, other, s.ID)), nil))
	require.Equal(t, events.BandPrimary, e.lastQueued(t).Band)
}

// event data umami cannot store is refused at the door.
func TestUnstorableDataIsRefused(t *testing.T) {
	e := startNode(t, nil)
	s := e.putSite(t, "https://shop.example")
	c := e.client(t, s, "198.51.100.11")
	ev := gwclient.Pageview(s.ID, s.Host, "/", proof(t, c, s.ID))
	ev.T, ev.N, ev.D = "ev", "swap", map[string]any{"amount": json.Number("1e15")}
	require.Equal(t, http.StatusBadRequest, post(t, c, ev, nil))
	ev.D = map[string]any{"amount": json.Number("999999999999999")}
	require.Equal(t, http.StatusAccepted, post(t, c, ev, nil))
}
