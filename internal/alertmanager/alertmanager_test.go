package alertmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/tests/fixtures/tlsca"
)

// one gettableAlert as alertmanager v0.34 writes it
func wire(fp, name, state string, silencedBy ...string) string {
	silenced := "[]"
	if len(silencedBy) > 0 {
		silenced = `["` + strings.Join(silencedBy, `","`) + `"]`
	}
	return fmt.Sprintf(`{"annotations":{"summary":"%[2]s fires","runbook":"docs/runbooks/alerts.md#%[4]s"},
"endsAt":"2026-11-13T12:04:00.000Z","fingerprint":"%[1]s","receivers":[{"name":"discord"}],
"startsAt":"2026-11-13T11:00:00.123Z","updatedAt":"2026-11-13T12:00:00.000Z",
"status":{"inhibitedBy":[],"mutedBy":[],"silencedBy":%[5]s,"state":"%[3]s"},
"labels":{"alertname":"%[2]s","severity":"page","node":"core-1"},"generatorURL":"http://prometheus:9090/graph"}`,
		fp, name, state, strings.ToLower(name), silenced)
}

// fakeMember answers like alertmanager's GET /api/v2/alerts and records
// the query it got.
func fakeMember(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &query
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

func TestAlertsAsksEveryMember(t *testing.T) {
	one, q := fakeMember(t, http.StatusOK, "["+wire("a1", "ServiceDown", StateActive)+","+wire("w", "Watchdog", StateActive)+"]")
	two, _ := fakeMember(t, http.StatusOK, "[]")
	broken, _ := fakeMember(t, http.StatusInternalServerError, `{"error":"x"}`)
	notAM, _ := fakeMember(t, http.StatusOK, `<html>proxy login</html>`)
	c, err := New([]*url.URL{mustURL(t, one.URL), mustURL(t, two.URL), mustURL(t, broken.URL), mustURL(t, notAM.URL)}, nil, time.Second)
	require.NoError(t, err)

	got := c.Alerts(t.Context())
	require.Len(t, got, 4)
	require.NoError(t, got[0].Err)
	require.Empty(t, *q, "no filter: silenced alerts and the watchdog are needed by the caller")
	require.Equal(t, mustURL(t, one.URL).Host, got[0].Address)
	require.Len(t, got[0].Alerts, 2)
	a := got[0].Alerts[0]
	require.Equal(t, "ServiceDown", a.Name())
	require.Equal(t, "a1", a.Fingerprint)
	require.Equal(t, StateActive, a.State)
	require.Equal(t, time.Date(2026, 11, 13, 11, 0, 0, 123e6, time.UTC), a.StartsAt)
	require.Equal(t, "docs/runbooks/alerts.md#servicedown", a.Annotations["runbook"])
	require.False(t, a.Silenced())

	require.NoError(t, got[1].Err)
	require.NotNil(t, got[1].Alerts, "an empty list is an answer, not a missing one")
	require.Empty(t, got[1].Alerts)
	require.ErrorContains(t, got[2].Err, "http 500")
	require.ErrorContains(t, got[3].Err, "not a list of alerts")
}

func TestAlertsUnreachableAndSlowMembers(t *testing.T) {
	// a port nothing listens on: the server is closed right after start
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := mustURL(t, gone.URL)
	gone.Close()
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(block) })

	c, err := New([]*url.URL{goneURL, mustURL(t, slow.URL)}, nil, 200*time.Millisecond)
	require.NoError(t, err)
	start := time.Now()
	got := c.Alerts(context.Background())
	require.Less(t, time.Since(start), 3*time.Second, "the members are asked at once and bounded by the timeout")
	require.Error(t, got[0].Err)
	require.ErrorIs(t, got[1].Err, context.DeadlineExceeded)
}

func TestAlertsOverMutualTLS(t *testing.T) {
	ca := tlsca.New(t, "internal-ca")
	server := ca.Issue(t, "core-1", "127.0.0.1")
	client := ca.Issue(t, "core-2", "127.0.0.1")
	serverPair, err := tls.X509KeyPair(server.CertPEM, server.KeyPEM)
	require.NoError(t, err)
	clientPair, err := tls.X509KeyPair(client.CertPEM, client.KeyPEM)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca.CertPEM))

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[" + wire("a1", "BackupTooOld", StateSuppressed, "silence-1") + "]"))
	}))
	// the members' web config: a client certificate of the internal ca
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverPair}, ClientCAs: roots,
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u := mustURL(t, srv.URL)

	withCert, err := New([]*url.URL{u}, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{clientPair}, MinVersion: tls.VersionTLS13}, time.Second)
	require.NoError(t, err)
	got := withCert.Alerts(t.Context())
	require.NoError(t, got[0].Err)
	require.Len(t, got[0].Alerts, 1)
	require.True(t, got[0].Alerts[0].Silenced())
	require.Equal(t, []string{"silence-1"}, got[0].Alerts[0].SilencedBy)

	without, err := New([]*url.URL{u}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}, time.Second)
	require.NoError(t, err)
	require.Error(t, without.Alerts(t.Context())[0].Err, "the member refuses a client without a certificate")

	_, err = New([]*url.URL{u}, nil, time.Second)
	require.ErrorContains(t, err, "needs a tls config")
	_, err = New(nil, nil, time.Second)
	require.Error(t, err)
}

func TestDecodeRefusesIncompleteAlerts(t *testing.T) {
	good := wire("a1", "ServiceDown", StateActive)
	for name, body := range map[string]string{
		"null":           `null`,
		"object":         `{"alerts":[]}`,
		"no fingerprint": strings.Replace(good, `"fingerprint":"a1"`, `"fingerprint":""`, 1),
		"no alertname":   strings.Replace(good, `"alertname":"ServiceDown"`, `"alertname":""`, 1),
		"no startsAt":    strings.Replace(good, `"startsAt":"2026-11-13T11:00:00.123Z",`, ``, 1),
		"no status":      strings.Replace(good, `"status":{"inhibitedBy":[],"mutedBy":[],"silencedBy":[],"state":"active"},`, ``, 1),
		"unknown state":  strings.Replace(good, `"state":"active"`, `"state":"firing"`, 1),
		"no annotations": strings.Replace(good, `"annotations":{"summary":"ServiceDown fires","runbook":"docs/runbooks/alerts.md#servicedown"},`, ``, 1),
	} {
		if name != "null" && name != "object" {
			body = "[" + body + "]"
		}
		_, err := Decode([]byte(body))
		require.Error(t, err, name)
	}
	got, err := Decode([]byte("[" + good + "]"))
	require.NoError(t, err)
	require.Len(t, got, 1)
}
