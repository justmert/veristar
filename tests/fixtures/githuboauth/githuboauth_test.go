package githuboauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const (
	testClientID     = "Iv1.fixtureclient"
	testClientSecret = "fixture-secret"
	testCallback     = "https://app.sa-test.example/api/auth/github/callback"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newFixture(t *testing.T) (*Server, *httptest.Server, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	s := New(Config{ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testCallback, Now: c.now})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, c
}

// oauthConfig mirrors the control plane's client setup: auth style in
// params, because github documents client_id and client_secret as
// parameters, and no scopes, because the public profile needs none.
func oauthConfig(base string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		RedirectURL:  testCallback,
		Endpoint: oauth2.Endpoint{
			AuthURL:   base + "/login/oauth/authorize",
			TokenURL:  base + "/login/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// consent plays the browser: opens the authorize url, then submits the form
// as the user. it returns the callback query.
func consent(t *testing.T, ts *httptest.Server, authURL string, decision string, id, login string) url.Values {
	t.Helper()
	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize page: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "TEST-ONLY") {
		t.Fatal("authorize page lacks the test-only banner")
	}
	u, _ := url.Parse(authURL)
	form := u.Query()
	form.Set("fixture_decision", decision)
	form.Set("fixture_user_id", id)
	form.Set("fixture_login", login)
	resp, err = noRedirect.PostForm(ts.URL+"/login/oauth/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("decision: %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loc.String(), testCallback+"?") {
		t.Fatalf("redirect to %s, want the registered callback", loc)
	}
	return loc.Query()
}

func getUser(t *testing.T, base, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestFlowWithOAuth2Client(t *testing.T) {
	s, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL("state-123", oauth2.S256ChallengeOption(verifier))
	q := consent(t, ts, authURL, "authorize", "583231", "octocat")
	if q.Get("state") != "state-123" || q.Get("code") == "" {
		t.Fatalf("callback query %v", q)
	}
	tok, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, "gho_") || tok.TokenType != "bearer" {
		t.Fatalf("token %+v", tok)
	}
	status, u := getUser(t, ts.URL, tok.AccessToken)
	if status != http.StatusOK || u["login"] != "octocat" || u["id"] != float64(583231) || u["email"] != nil {
		t.Fatalf("/user %d %v", status, u)
	}
	r := s.Requests()
	if len(r.Authorize) != 1 || r.Authorize[0].HasScope {
		t.Fatalf("authorize records %+v", r.Authorize)
	}
	if r.Authorize[0].Query["code_challenge_method"][0] != "S256" {
		t.Fatal("challenge method not recorded")
	}
	if len(r.Token) != 1 || r.Token[0].Result != "ok" || r.Token[0].Form["code_verifier"] != "<set>" || r.Token[0].Form["client_secret"] != "<set>" {
		t.Fatalf("token records %+v", r.Token)
	}
	if len(r.User) != 1 || r.User[0].APIVersion != "2022-11-28" || r.User[0].Scheme != "bearer" {
		t.Fatalf("user records %+v", r.User)
	}
}

func TestCodeIsSingleUse(t *testing.T) {
	_, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(v)), "authorize", "1", "a")
	if _, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(v)); err != nil {
		t.Fatal(err)
	}
	_, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(v))
	assertCode(t, err, "bad_verification_code")
}

func TestWrongVerifierRefusedAndConsumesCode(t *testing.T) {
	_, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(v)), "authorize", "1", "a")
	_, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(oauth2.GenerateVerifier()))
	assertCode(t, err, "bad_verification_code")
	_, err = cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(v))
	assertCode(t, err, "bad_verification_code")
}

func TestMissingVerifierRefused(t *testing.T) {
	_, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(v)), "authorize", "1", "a")
	_, err := cfg.Exchange(context.Background(), q.Get("code"))
	assertCode(t, err, "bad_verification_code")
}

func TestCodeExpiresAfterTenMinutes(t *testing.T) {
	_, ts, c := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(v)), "authorize", "1", "a")
	c.add(CodeTTL + time.Second)
	_, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(v))
	assertCode(t, err, "bad_verification_code")
}

func TestWrongSecretRefused(t *testing.T) {
	_, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(v)), "authorize", "1", "a")
	cfg.ClientSecret = "wrong"
	_, err := cfg.Exchange(context.Background(), q.Get("code"), oauth2.VerifierOption(v))
	assertCode(t, err, "incorrect_client_credentials")
}

func TestErrorsAre200FormEncodedOrJSON(t *testing.T) {
	_, ts, _ := newFixture(t)
	form := url.Values{"client_id": {testClientID}, "client_secret": {testClientSecret}, "code": {"nope"}, "code_verifier": {strings.Repeat("a", 43)}}
	resp, err := http.PostForm(ts.URL+"/login/oauth/access_token", form)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	v, _ := url.ParseQuery(string(b))
	if v.Get("error") != "bad_verification_code" {
		t.Fatalf("body %s", b)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&m)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || m["error"] != "bad_verification_code" {
		t.Fatalf("%d %v", resp.StatusCode, m)
	}
}

func TestAuthorizeRefusals(t *testing.T) {
	_, ts, _ := newFixture(t)
	good := url.Values{
		"client_id": {testClientID}, "redirect_uri": {testCallback}, "state": {"s"},
		"code_challenge": {strings.Repeat("A", 43)}, "code_challenge_method": {"S256"},
	}
	cases := map[string]func(url.Values) (url.Values, int){
		"unknown client": func(v url.Values) (url.Values, int) { v.Set("client_id", "x"); return v, 404 },
		"redirect mismatch": func(v url.Values) (url.Values, int) {
			v.Set("redirect_uri", testCallback+"/x")
			return v, 400
		},
		"plain pkce": func(v url.Values) (url.Values, int) { v.Set("code_challenge_method", "plain"); return v, 400 },
		"no pkce": func(v url.Values) (url.Values, int) {
			v.Del("code_challenge")
			v.Del("code_challenge_method")
			return v, 400
		},
		"short":          func(v url.Values) (url.Values, int) { v.Set("code_challenge", "abc"); return v, 400 },
		"no state":       func(v url.Values) (url.Values, int) { v.Del("state"); return v, 400 },
		"good":           func(v url.Values) (url.Values, int) { return v, 200 },
		"redirect unset": func(v url.Values) (url.Values, int) { v.Del("redirect_uri"); return v, 200 },
	}
	for name, mod := range cases {
		v := url.Values{}
		for k, vs := range good {
			v[k] = append([]string{}, vs...)
		}
		q, want := mod(v)
		resp, err := http.Get(ts.URL + "/login/oauth/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", name, resp.StatusCode, want)
		}
	}
}

func TestDenyRedirectsWithAccessDenied(t *testing.T) {
	_, ts, _ := newFixture(t)
	cfg := oauthConfig(ts.URL)
	v := oauth2.GenerateVerifier()
	q := consent(t, ts, cfg.AuthCodeURL("st", oauth2.S256ChallengeOption(v)), "cancel", "", "")
	if q.Get("error") != "access_denied" || q.Get("state") != "st" || q.Get("code") != "" || q.Get("error_uri") == "" {
		t.Fatalf("callback query %v", q)
	}
}

func TestUserRequiresToken(t *testing.T) {
	_, ts, _ := newFixture(t)
	status, m := getUser(t, ts.URL, "gho_unknown")
	if status != http.StatusUnauthorized || m["message"] != "Bad credentials" {
		t.Fatalf("%d %v", status, m)
	}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var re *oauth2.RetrieveError
	if err == nil {
		t.Fatalf("exchange succeeded, want %s", code)
	}
	if !asRetrieve(err, &re) || re.ErrorCode != code {
		t.Fatalf("error %v, want %s", err, code)
	}
}

func asRetrieve(err error, out **oauth2.RetrieveError) bool {
	re, ok := err.(*oauth2.RetrieveError) //nolint:errorlint // oauth2 returns it unwrapped
	if ok {
		*out = re
	}
	return ok
}
