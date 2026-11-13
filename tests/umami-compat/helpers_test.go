//go:build umamicompat

// Package umamicompat holds the checks that pin the umami behaviours the
// platform depends on (the design to 4.9). they run against a live
// umami with the environment of section 4.9 (make umami-compat) and are the
// gate for every umami upgrade. each test creates its own team and website
// and deletes them when it ends.
package umamicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/umami"
)

// a desktop chrome user agent that umami's isbot check accepts
const chromeUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

type env struct {
	base   *url.URL
	client *umami.Client
	admin  *umami.Admin
	token  umami.Token
}

func setup(t *testing.T) *env {
	t.Helper()
	raw := os.Getenv("UMAMI_IT_URL")
	pw := os.Getenv("UMAMI_ADMIN_PASSWORD")
	if raw == "" || pw == "" {
		// the build tag means someone asked for the gate, so a missing target is a failure
		t.Fatal("UMAMI_IT_URL and UMAMI_ADMIN_PASSWORD must be set; run make umami-compat")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := umami.New(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := umami.NewAdmin(c, umami.SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Login(ctx(t), umami.SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	return &env{base: u, client: c, admin: a, token: s.Token}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// site creates a team and one website in it. deleting the team deletes the
// website and its data.
func (e *env) site(t *testing.T) (teamID, websiteID string) {
	t.Helper()
	team, _, err := e.admin.CreateTeam(ctx(t), "compat-"+uuid.NewString()[:8], "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.admin.DeleteTeam(context.Background(), team.ID); err != nil {
			t.Errorf("cleanup: delete team %s: %v", team.ID, err)
		}
	})
	w, err := e.admin.CreateWebsite(ctx(t), umami.NewWebsite{
		ID: uuid.NewString(), Name: "compat", Domain: "compat.example", TeamID: team.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return team.ID, w.ID
}

func pageview(website string, ts time.Time, salt string) umami.Event {
	return umami.Event{
		Website: website, Hostname: "compat.example", URL: "/p", Title: "compat",
		Screen: "1920x1080", Language: "en-US", Timestamp: ts.Unix(), Salt: salt,
	}
}

func (e *env) send(t *testing.T, r umami.SendRequest) umami.SendResult {
	t.Helper()
	if r.UserAgent == "" {
		r.UserAgent = chromeUA
	}
	res, err := e.client.Send(ctx(t), r)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return res
}

// rawSend posts a body to /api/send with exactly the given headers, for the
// checks the typed client deliberately cannot express.
func (e *env) rawSend(t *testing.T, hdr map[string]string, body any) (int, string) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx(t), http.MethodPost, e.base.String()+"/api/send", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// get reads an api route with token and decodes the answer into out. it
// returns the status so callers can assert denials.
func (e *env) get(t *testing.T, token umami.Token, path string, out any) int {
	t.Helper()
	return e.call(t, token, http.MethodGet, path, nil, out)
}

func (e *env) call(t *testing.T, token umami.Token, method, path string, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx(t), method, e.base.String()+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !token.Empty() {
		req.Header.Set("Authorization", "Bearer "+token.Reveal())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

type session struct {
	ID      string  `json:"id"`
	Country *string `json:"country"`
	Region  *string `json:"region"`
	City    *string `json:"city"`
	Browser *string `json:"browser"`
	FirstAt string  `json:"firstAt"`
	LastAt  string  `json:"lastAt"`
}

func (e *env) session(t *testing.T, websiteID, sessionID string) session {
	t.Helper()
	var s session
	if code := e.get(t, e.token, fmt.Sprintf("/api/websites/%s/sessions/%s", websiteID, sessionID), &s); code != http.StatusOK {
		t.Fatalf("get session: http %d", code)
	}
	return s
}

type sessionPage struct {
	Data  []session `json:"data"`
	Count int       `json:"count"`
}

// sessions lists every session of a website over a wide range.
func (e *env) sessions(t *testing.T, websiteID string) sessionPage {
	t.Helper()
	var p sessionPage
	now := time.Now()
	path := fmt.Sprintf("/api/websites/%s/sessions?startAt=%d&endAt=%d&pageSize=100",
		websiteID, now.AddDate(0, 0, -30).UnixMilli(), now.Add(time.Hour).UnixMilli())
	if code := e.get(t, e.token, path, &p); code != http.StatusOK {
		t.Fatalf("list sessions: http %d", code)
	}
	return p
}

func str(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}
