//go:build otelapps

package otelapps

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
)

// TestAnchorPlatform runs the stellar anchor platform with the java agent as
// examples/otel-anchor-platform/compose.yaml sets it up: its sep server and
// platform server on postgresql, metrics to the platform stand-in with the
// bearer token, traces to the tenant backend, no logs. it drives sep-1, sep-10,
// sep-24 and platform api requests, stops both servers so the agent flushes,
// and checks that every response reached the stand-in as
// http.server.request.duration under the controller's route template and
// status, that the tenant backend has the same requests as server spans, and
// that neither backend got the other's signal.
//
//	OTELAPPS_APPS=anchor-platform go test -tags otelapps -count=1 -run TestAnchorPlatform -v ./tests/otelapps/
func TestAnchorPlatform(t *testing.T) {
	if want := os.Getenv("OTELAPPS_APPS"); want != "" && !strings.Contains(","+want+",", ",anchor-platform,") {
		t.Skip("OTELAPPS_APPS does not name anchor-platform")
	}
	root := repoRoot(t)
	out := t.TempDir()
	for _, d := range []string{out, filepath.Join(out, "tenant"), filepath.Join(out, "platform")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	const standinProject, apProject = "sa-otelapps-anchor", "sa-otelapps-anchor-ap"
	standinFiles := []string{filepath.Join(root, "deploy/compose/examples.yml")}
	apFiles := []string{filepath.Join(root, "examples/otel-anchor-platform/compose.yaml"), filepath.Join(root, "tests/otelapps/anchor.compose.yml")}
	standinEnv := []string{"OTELAPPS_OUT=" + out, "PLATFORM_OTLP_TOKEN=" + token}

	// fresh secrets per run: nothing secret is committed with the example
	jwtSecret := randomHex(t, 32)
	client := keypair.MustRandom()
	apEnv := []string{
		"ANCHOR_NETWORK=" + standinProject + "_default",
		"ANCHOR_SEP10_SIGNING_SEED=" + keypair.MustRandom().Seed(),
		"ANCHOR_JWT_SECRET=" + jwtSecret,
		"ANCHOR_DB_PASSWORD=" + randomHex(t, 16),
		"TENANT_OTLP_ENDPOINT=http://tenant-backend:4318",
		"PLATFORM_OTLP_ENDPOINT=http://platform-standin:4318",
		"PLATFORM_OTLP_TOKEN=" + token,
		fmt.Sprintf("METRICS_EXPORT_INTERVAL_MS=%d", exportInterval.Milliseconds()),
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(composeIn(t, apProject, apFiles, apEnv, "logs", "--no-color", "--tail", "200", "sep-server", "platform-server"))
		}
		composeIn(t, apProject, apFiles, apEnv, "down", "--volumes", "--remove-orphans")
		composeIn(t, standinProject, standinFiles, standinEnv, "down", "--volumes", "--remove-orphans")
	})
	composeIn(t, standinProject, standinFiles, standinEnv, "up", "-d", "tenant-backend", "platform-standin")
	composeIn(t, apProject, apFiles, apEnv, "build")
	composeIn(t, apProject, apFiles, apEnv, "up", "-d", "--no-build")
	checkStandinAuth(t)

	d := &anchorDriver{client: &http.Client{Timeout: 10 * time.Second}, sent: map[string]map[requestKey]int64{}}
	const sep, platform = "anchor-sep-server", "anchor-platform-server"
	sepURL, platformURL := "http://127.0.0.1:47206", "http://127.0.0.1:47207"
	d.waitFor(t, sep, sepURL+"/.well-known/stellar.toml", "/.well-known/stellar.toml")
	d.waitFor(t, platform, platformURL+"/health", "/health")

	jwt := sep10JWT(jwtSecret, client.Address())
	auth := map[string]string{"Authorization": "Bearer " + jwt}
	for i := 0; i < 4; i++ {
		d.do(t, sep, "GET", sepURL+"/.well-known/stellar.toml", "/.well-known/stellar.toml", nil, nil, 200)
		d.do(t, sep, "GET", sepURL+"/sep24/info", "/sep24/info", nil, nil, 200)
	}
	for i := 0; i < 3; i++ {
		d.do(t, sep, "GET", sepURL+"/auth?account="+client.Address(), "/auth", nil, nil, 200)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		b := d.do(t, sep, "POST", sepURL+"/sep24/transactions/deposit/interactive", "/sep24/transactions/deposit/interactive",
			auth, map[string]string{"asset_code": "native", "amount": fmt.Sprintf("%d", i+1)}, 200)
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(b, &r); err != nil || r.ID == "" {
			t.Fatalf("sep-24 deposit answered %s", b)
		}
		ids = append(ids, r.ID)
	}
	// a pause of three export intervals, so the agent exports periodically
	// while traffic is still flowing
	time.Sleep(3 * exportInterval)
	for _, id := range ids {
		d.do(t, sep, "GET", sepURL+"/sep24/transaction?id="+id, "/sep24/transaction", auth, nil, 200)
	}
	// the jwt filter refuses these before any controller runs; the agent
	// still records the route spring matched
	for i := 0; i < 2; i++ {
		d.do(t, sep, "GET", sepURL+"/sep24/transaction?id="+ids[i], "/sep24/transaction", nil, nil, 403)
	}
	// no controller matches: spring's static resource handler answers 404
	// under its pattern, never the raw path
	for i := 0; i < 2; i++ {
		d.do(t, sep, "GET", fmt.Sprintf("%s/no-such-endpoint/%d", sepURL, 900000+i), "/**", nil, nil, 404)
	}
	for _, id := range ids {
		d.do(t, platform, "GET", platformURL+"/transactions/"+id, "/transactions/{id}", nil, nil, 200)
	}
	for i := 0; i < 2; i++ {
		d.do(t, platform, "GET", fmt.Sprintf("%s/transactions/00000000-0000-4000-8000-%012d", platformURL, i), "/transactions/{id}", nil, nil, 404)
		d.do(t, platform, "GET", platformURL+"/transactions?sep=24", "/transactions", nil, nil, 200)
	}

	// sigterm reaches the jvm directly (the example's entrypoint), so the
	// agent's shutdown hook exports the last interval
	composeIn(t, apProject, apFiles, apEnv, "stop", "--timeout", "30", "sep-server", "platform-server")

	for _, svc := range []string{sep, platform} {
		want := d.sent[svc]
		var total int64
		for _, v := range want {
			total += v
		}
		var pm durationSummary
		var tt traceSummary
		deadline := time.Now().Add(30 * time.Second)
		for {
			var err error
			if pm, err = summarizeMetrics(filepath.Join(out, "platform", "metrics.jsonl"), svc); err != nil {
				t.Fatal(err)
			}
			if tt, err = summarizeTraces(filepath.Join(out, "tenant", "traces.jsonl"), svc); err != nil {
				t.Fatal(err)
			}
			if (sum(pm.Counts) >= total && sum(tt.Server) >= total) || time.Now().After(deadline) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if len(pm.Names) != 1 || !pm.Names["http.server.request.duration"] || len(pm.Units) != 1 || !pm.Units["s"] {
			t.Errorf("%s: duration metrics %v in %v, want http.server.request.duration in s", svc, pm.Names, pm.Units)
		}
		if len(pm.Exports) < 2 {
			t.Errorf("%s: %d collection timestamps, want periodic exports before shutdown", svc, len(pm.Exports))
		}
		if df := diff(pm.Counts, want); df != "" {
			t.Errorf("%s: request counts at the platform stand-in differ from the responses received:\n%s", svc, df)
		}
		if df := diff(tt.Server, want); df != "" {
			t.Errorf("%s: server spans at the tenant backend differ from the responses received:\n%s", svc, df)
		}
		// each signal goes to one backend only
		tm, err := summarizeMetrics(filepath.Join(out, "tenant", "metrics.jsonl"), svc)
		if err != nil {
			t.Fatal(err)
		}
		pt, err := summarizeTraces(filepath.Join(out, "platform", "traces.jsonl"), svc)
		if err != nil {
			t.Fatal(err)
		}
		if tm.DataPoints != 0 || len(tm.OtherMetrics) != 0 {
			t.Errorf("%s: the tenant backend got metrics: %d duration points, others %v", svc, tm.DataPoints, tm.OtherMetrics)
		}
		if n := sum(pt.Server) + int64(pt.OtherKindSpans); n != 0 {
			t.Errorf("%s: the platform stand-in got %d spans", svc, n)
		}
		t.Logf("%s: %d requests in %d routes and statuses, %d metric points over %d collections at the platform stand-in, %d server and %d other spans at the tenant backend",
			svc, total, len(want), pm.DataPoints, len(pm.Exports), sum(tt.Server), tt.OtherKindSpans)
	}
}

// anchorDriver sends requests to the anchor servers and counts every
// response it received by service, method, expected route and status.
type anchorDriver struct {
	client *http.Client
	sent   map[string]map[requestKey]int64
}

func (d *anchorDriver) record(service, method, route string, status int) {
	if d.sent[service] == nil {
		d.sent[service] = map[requestKey]int64{}
	}
	d.sent[service][requestKey{method, route, status}]++
}

func (d *anchorDriver) send(method, url string, headers map[string]string, body any) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := d.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return res.StatusCode, b, err
}

// do sends one request and fails unless the server answered with want.
func (d *anchorDriver) do(t *testing.T, service, method, url, route string, headers map[string]string, body any, want int) []byte {
	t.Helper()
	status, b, err := d.send(method, url, headers, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	d.record(service, method, route, status)
	if status != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, url, status, want, strings.TrimSpace(string(b)))
	}
	return b
}

// waitFor polls url until it answers 200. every answer is a request the
// agent records, so each one is counted; refused connections are not.
func (d *anchorDriver) waitFor(t *testing.T, service, url, route string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		status, _, err := d.send("GET", url, nil, nil)
		if err == nil {
			d.record(service, "GET", route, status)
			if status == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s did not answer 200 within 4 minutes", url)
}

// sep10JWT is the token the sep server's own sep-10 endpoint issues after a
// signed challenge, minted with the shared secret instead: the challenge
// verification would load the client account from horizon, and the test
// stays off the network.
func sep10JWT(secret, account string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": "http://localhost:8080/auth", "sub": account, "iat": now, "exp": now + 3600,
		"jti": fmt.Sprintf("otelapps-%d", now), "aud": []string{"sep10"}, "client_name": "",
	})
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
