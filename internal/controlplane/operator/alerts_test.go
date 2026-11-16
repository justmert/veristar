package operator

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/alertmanager"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// amAlert is one alert in the shape alertmanager's GET /api/v2/alerts
// writes (gettableAlert of api/v2/openapi.yaml, v0.34).
type amAlert struct {
	fp, name, severity, node, job, summary string
	starts                                 time.Time
	state                                  string
	silencedBy, inhibitedBy                []string
}

func (a amAlert) json() map[string]any {
	labels := map[string]string{"alertname": a.name}
	if a.severity != "" {
		labels["severity"] = a.severity
	}
	if a.node != "" {
		labels["node"] = a.node
	}
	if a.job != "" {
		labels["job"] = a.job
	}
	annotations := map[string]string{"runbook": "docs/runbooks/alerts.md#" + strings.ToLower(a.name)}
	if a.summary != "" {
		annotations["summary"] = a.summary
	}
	state := a.state
	if state == "" {
		state = alertmanager.StateActive
	}
	nonNil := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return map[string]any{
		"fingerprint": a.fp, "labels": labels, "annotations": annotations,
		"startsAt":  a.starts.Format(time.RFC3339Nano),
		"updatedAt": a.starts.Add(time.Minute).Format(time.RFC3339Nano),
		"endsAt":    a.starts.Add(4 * time.Hour).Format(time.RFC3339Nano),
		"receivers": []map[string]string{{"name": "discord"}},
		"status": map[string]any{"state": state, "silencedBy": nonNil(a.silencedBy),
			"inhibitedBy": nonNil(a.inhibitedBy), "mutedBy": []string{}},
	}
}

// member is a stand-in alertmanager member serving a fixed alert list on
// the v2 route. it runs real http, so the real client reads it.
func member(t *testing.T, alerts ...amAlert) *url.URL {
	t.Helper()
	body := make([]map[string]any, 0, len(alerts))
	for _, a := range alerts {
		body = append(body, a.json())
	}
	b, err := json.Marshal(body)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u
}

// downMember is the address of a member that refuses connections.
func downMember(t *testing.T) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	srv.Close()
	return u
}

var (
	t0      = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	checked = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
)

type alertsBody struct {
	CheckedAt      string `json:"checked_at"`
	WatchdogFiring bool   `json:"watchdog_firing"`
	Sources        []struct {
		Address        string  `json:"address"`
		Reachable      bool    `json:"reachable"`
		WatchdogFiring bool    `json:"watchdog_firing"`
		Error          *string `json:"error"`
	} `json:"sources"`
	Alerts []struct {
		Fingerprint string            `json:"fingerprint"`
		Name        string            `json:"name"`
		Severity    *string           `json:"severity"`
		Summary     *string           `json:"summary"`
		Description *string           `json:"description"`
		Node        *string           `json:"node"`
		Instance    *string           `json:"instance"`
		Service     *string           `json:"service"`
		StartsAt    string            `json:"starts_at"`
		State       string            `json:"state"`
		InhibitedBy []string          `json:"inhibited_by"`
		Runbook     *string           `json:"runbook"`
		Labels      map[string]string `json:"labels"`
	} `json:"alerts"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// getAlerts serves the route without the guard (the guard has its own
// test below) and decodes the answer.
func getAlerts(t *testing.T, src AlertReader) (int, alertsBody, string) {
	t.Helper()
	r := chi.NewRouter()
	NewAlerts(src, func() time.Time { return checked }).Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/alerts", nil))
	var b alertsBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b), rec.Body.String())
	return rec.Code, b, rec.Body.String()
}

func client(t *testing.T, urls ...*url.URL) *alertmanager.Client {
	t.Helper()
	c, err := alertmanager.New(urls, nil, time.Second)
	require.NoError(t, err)
	return c
}

func strp(s string) *string { return &s }

func TestAlertsListsFiringAlerts(t *testing.T) {
	watchdog := amAlert{fp: "wd", name: "Watchdog", severity: "heartbeat", starts: t0}
	down := amAlert{fp: "f-down", name: "ServiceDown", severity: "page", node: "core-1", job: "controlplane",
		summary: "controlplane on core-1 does not answer scrapes", starts: t0.Add(2 * time.Hour)}
	backup := amAlert{fp: "f-backup", name: "BackupTooOld", severity: "ticket", starts: t0}
	nats := amAlert{fp: "f-nats", name: "NATSQuorumLost", severity: "page", starts: t0.Add(time.Hour)}
	visitor := amAlert{fp: "f-visitor", name: "VisitorSecretMissing", severity: "page", node: "edge-1",
		starts: t0.Add(90 * time.Minute), state: alertmanager.StateSuppressed, inhibitedBy: []string{"f-nats"}}
	silenced := amAlert{fp: "f-disk", name: "DiskUsageHigh", severity: "ticket", starts: t0,
		state: alertmanager.StateSuppressed, silencedBy: []string{"8b1c"}}
	// the second member saw ServiceDown start earlier (its own clock of the
	// first notification); the earliest start is the one shown
	downEarlier := down
	downEarlier.starts = down.starts.Add(-time.Minute)

	code, b, raw := getAlerts(t, client(t,
		member(t, watchdog, down, backup, nats, visitor, silenced),
		member(t, watchdog, downEarlier, backup, nats, visitor, silenced),
	))
	require.Equal(t, http.StatusOK, code, raw)
	require.Equal(t, "2026-11-13T12:00:00Z", b.CheckedAt)
	require.True(t, b.WatchdogFiring)
	require.Len(t, b.Sources, 2)
	for _, s := range b.Sources {
		require.True(t, s.Reachable)
		require.True(t, s.WatchdogFiring)
		require.Nil(t, s.Error)
	}

	names := make([]string, 0, len(b.Alerts))
	for _, a := range b.Alerts {
		names = append(names, a.Name)
	}
	// page first, longest firing first within a severity; no watchdog, no
	// silenced alert, and each fingerprint once
	require.Equal(t, []string{"NATSQuorumLost", "VisitorSecretMissing", "ServiceDown", "BackupTooOld"}, names)

	d := b.Alerts[2]
	require.Equal(t, "f-down", d.Fingerprint)
	require.Equal(t, strp("page"), d.Severity)
	require.Equal(t, strp("controlplane on core-1 does not answer scrapes"), d.Summary)
	require.Nil(t, d.Description)
	require.Equal(t, strp("core-1"), d.Node)
	require.Equal(t, strp("controlplane"), d.Service)
	require.Nil(t, d.Instance)
	require.Equal(t, "2026-11-13T10:59:00Z", d.StartsAt)
	require.Equal(t, "active", d.State)
	require.Equal(t, []string{}, d.InhibitedBy)
	require.Equal(t, strp("docs/runbooks/alerts.md#servicedown"), d.Runbook)
	require.Equal(t, "core-1", d.Labels["node"])

	v := b.Alerts[1]
	require.Equal(t, "suppressed", v.State)
	require.Equal(t, []string{"f-nats"}, v.InhibitedBy)
	require.Nil(t, b.Error)
}

func TestAlertsAllClear(t *testing.T) {
	code, b, raw := getAlerts(t, client(t, member(t, amAlert{fp: "wd", name: "Watchdog", starts: t0})))
	require.Equal(t, http.StatusOK, code, raw)
	require.True(t, b.WatchdogFiring)
	require.NotNil(t, b.Alerts)
	require.Empty(t, b.Alerts)
	require.Contains(t, raw, `"alerts":[]`, "an empty list, never null")
}

// without the watchdog the pipeline from prometheus to alertmanager is
// broken: the answer says so, and still lists what the member holds
func TestAlertsWatchdogMissing(t *testing.T) {
	stale := amAlert{fp: "f-1", name: "DLQNotEmpty", severity: "ticket", starts: t0}
	code, b, raw := getAlerts(t, client(t, member(t, stale), member(t)))
	require.Equal(t, http.StatusOK, code, raw)
	require.False(t, b.WatchdogFiring)
	require.False(t, b.Sources[0].WatchdogFiring)
	require.False(t, b.Sources[1].WatchdogFiring)
	require.Len(t, b.Alerts, 1)

	// a silenced watchdog still proves the pipeline works
	silencedWD := amAlert{fp: "wd", name: "Watchdog", starts: t0, state: alertmanager.StateSuppressed, silencedBy: []string{"s"}}
	_, b, _ = getAlerts(t, client(t, member(t, silencedWD)))
	require.True(t, b.WatchdogFiring)
	require.Empty(t, b.Alerts)
}

func TestAlertsOneMemberDown(t *testing.T) {
	down := downMember(t)
	page := amAlert{fp: "f-1", name: "PatroniNoLeader", severity: "page", starts: t0}
	code, b, raw := getAlerts(t, client(t, down, member(t, amAlert{fp: "wd", name: "Watchdog", starts: t0}, page)))
	require.Equal(t, http.StatusOK, code, raw)
	require.True(t, b.WatchdogFiring)
	require.Len(t, b.Alerts, 1)
	require.False(t, b.Sources[0].Reachable)
	require.Equal(t, down.Host, b.Sources[0].Address)
	require.NotNil(t, b.Sources[0].Error)
	require.NotEmpty(t, *b.Sources[0].Error)
	require.True(t, b.Sources[1].Reachable)
}

// no member answering is an error, never an empty list that reads as all
// clear
func TestAlertsSourceUnreachable(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	t.Cleanup(broken.Close)
	bu, err := url.Parse(broken.URL)
	require.NoError(t, err)
	down := downMember(t)

	code, b, raw := getAlerts(t, client(t, down, bu))
	require.Equal(t, http.StatusServiceUnavailable, code, raw)
	require.NotNil(t, b.Error)
	require.Equal(t, "alert_source_unreachable", b.Error.Code)
	require.Contains(t, b.Error.Message, down.Host)
	require.Contains(t, b.Error.Message, "http 502")
	require.NotContains(t, raw, `"alerts"`)
}

func TestAlertsNotConfigured(t *testing.T) {
	code, b, raw := getAlerts(t, nil)
	require.Equal(t, http.StatusServiceUnavailable, code, raw)
	require.Equal(t, "alert_source_not_configured", b.Error.Code)
	require.NotContains(t, raw, `"alerts"`)
}

// TestAlertsNeedAnOperator mounts the route the way the control plane does
// (sessions, RequireUser, the operator guard): anonymous is 401, a signed-in
// user who is not an operator is 403 operator_required and alertmanager is
// never asked, an operator gets the list.
func TestAlertsNeedAnOperator(t *testing.T) {
	d, pg := reviewDB(t)
	operatorID, plainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, github_id, github_login, is_operator) values ($1, 7, 'op', true), ($2, 8, 'plain', false)`,
		operatorID, plainID)

	var asked atomic.Int32
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		b, _ := json.Marshal([]map[string]any{
			amAlert{fp: "wd", name: "Watchdog", starts: t0}.json(),
			amAlert{fp: "f-1", name: "ServiceDown", severity: "page", starts: t0}.json(),
		})
		_, _ = w.Write(b)
	}))
	t.Cleanup(am.Close)
	amURL, err := url.Parse(am.URL)
	require.NoError(t, err)

	sessions := session.New(d)
	guard, err := NewGuard(identity.Operators{GitHub: map[int64]bool{7: true}})
	require.NoError(t, err)
	routes := NewAlerts(client(t, amURL), time.Now)
	var api atomic.Pointer[http.Handler]
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*api.Load()).ServeHTTP(w, r)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	h := controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: srv.URL, Sessions: sessions,
		// a sign-in shortcut for the test: the session is the real one,
		// only the oauth round trip is skipped
		Public: []controlplane.Routes{controlplane.RoutesFunc(func(r chi.Router) {
			r.Get("/test-sign-in/{id}", func(w http.ResponseWriter, r *http.Request) {
				if err := sessions.SignIn(r.Context(), uuid.MustParse(chi.URLParam(r, "id"))); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})},
		Private: []controlplane.Routes{controlplane.RoutesFunc(guard.Mount(routes))},
	})
	api.Store(&h)

	signedIn := func(user *uuid.UUID) *http.Client {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		hc := *srv.Client()
		hc.Jar = jar
		if user != nil {
			res, err := hc.Get(srv.URL + "/api/test-sign-in/" + user.String())
			require.NoError(t, err)
			_ = res.Body.Close()
			require.Equal(t, http.StatusNoContent, res.StatusCode)
		}
		return &hc
	}
	get := func(hc *http.Client) (int, string) {
		res, err := hc.Get(srv.URL + "/api/operator/alerts")
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res.StatusCode, string(b)
	}

	code, body := get(signedIn(nil))
	require.Equal(t, http.StatusUnauthorized, code, body)
	code, body = get(signedIn(&plainID))
	require.Equal(t, http.StatusForbidden, code, body)
	require.Contains(t, body, `"code":"operator_required"`)
	require.NotContains(t, body, "ServiceDown")
	require.Zero(t, asked.Load(), "a non-operator request must not reach alertmanager")

	code, body = get(signedIn(&operatorID))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"name":"ServiceDown"`)
	require.NotContains(t, body, `"name":"Watchdog"`)
	require.Equal(t, int32(1), asked.Load(), fmt.Sprintf("alertmanager asked %d times", asked.Load()))
}
