package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testdata holds github rest api answers recorded from api.github.com on
// 2026-11-13: an open-issues page of a public repository (its Link header
// paged with an opaque after= cursor, which the fake below reproduces), a
// pull request as the issues endpoint lists it, issue comments by an
// ordinary user (author_association NONE), by github-actions[bot]
// (CONTRIBUTOR, type Bot) and by an organisation member (MEMBER), a label,
// and the 404 body of a missing label. every key and nesting level is as
// recorded; logins, ids, urls, text and times were replaced to build the
// scenario below. answers to label changes were not recorded (that needs
// writes to a repository); the client reads only their status.

type call struct {
	Method, Path, Body string
	Auth               string
}

type fakeGitHub struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []call
	existing map[string]bool
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, call{Method: r.Method, Path: r.URL.EscapedPath() + "?" + r.URL.RawQuery, Body: string(b), Auth: r.Header.Get("Authorization")})
	f.mu.Unlock()

	file := func(name string) {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(f.t, err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(data)
	}
	p := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodGet && p == "/repos/example-org/example-repo/issues":
		require.Equal(f.t, "open", r.URL.Query().Get("state"))
		w.Header().Set("Link", `<http://`+r.Host+`/repositories/1/issues?state=open&per_page=100&after=Y3Vyc29yOnYyOpLPAAAB&page=2>; rel="next"`)
		file("issues-page1.json")
	case r.Method == http.MethodGet && p == "/repositories/1/issues":
		require.Equal(f.t, "Y3Vyc29yOnYyOpLPAAAB", r.URL.Query().Get("after"))
		file("issues-page2.json")
	case r.Method == http.MethodGet && p == "/repos/example-org/example-repo/issues/101/comments":
		file("comments-101.json")
	case r.Method == http.MethodGet && p == "/repos/example-org/example-repo/issues/104/comments":
		file("comments-104.json")
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/repos/example-org/example-repo/labels/"):
		name := strings.TrimPrefix(r.URL.Path, "/repos/example-org/example-repo/labels/")
		if f.existing[name] {
			file("label.json")
			return
		}
		w.WriteHeader(http.StatusNotFound)
		file("label-404.json")
	case r.Method == http.MethodPost && p == "/repos/example-org/example-repo/labels":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/labels"):
		_, _ = w.Write([]byte("[]"))
	case r.Method == http.MethodDelete:
		_, _ = w.Write([]byte("[]"))
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}
}

func (f *fakeGitHub) mutations() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func newFake(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{t: t, existing: map[string]bool{LabelResponseDue: true}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// friday 2026-11-13 12:00 utc, the moment the scenario is assessed at:
//   - #101 opened friday 11-06 09:00, only a bot commented: 5 d 3 h without a
//     maintainer response, response overdue
//   - #102 is a pull request, #103 was opened by a member, #105 by a bot: not tickets
//   - #104 opened friday 10-30 12:00, answered by a member on monday: the
//     response was met, and exactly 10 business days have passed, so
//     resolution is overdue; its two stale due labels go
//   - #106 opened monday 11-09 12:00, no comments: exactly 4 business days,
//     response due
var scenarioNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func TestRunAgainstRecordedShapes(t *testing.T) {
	f, srv := newFake(t)
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"-repo", "example-org/example-repo", "-api", srv.URL, "-apply"},
		env(map[string]string{"GITHUB_TOKEN": "test-token"}), &out, &errb, srv.Client(), func() time.Time { return scenarioNow })
	require.Equal(t, 0, code, errb.String())

	for _, c := range f.calls {
		require.Equal(t, "Bearer test-token", c.Auth, "%s %s", c.Method, c.Path)
	}
	// no comments are read for issues without any, or for non-tickets
	var reads []string
	for _, c := range f.calls {
		if c.Method == http.MethodGet && strings.Contains(c.Path, "/comments") {
			reads = append(reads, c.Path)
		}
	}
	require.Equal(t, []string{
		"/repos/example-org/example-repo/issues/101/comments?per_page=100",
		"/repos/example-org/example-repo/issues/104/comments?per_page=100",
	}, reads)

	muts := f.mutations()
	var got []string
	for _, m := range muts {
		got = append(got, m.Method+" "+m.Path+" "+m.Body)
	}
	require.Equal(t, []string{
		// the two labels the repository lacks are created first; response due exists
		`POST /repos/example-org/example-repo/labels? {"color":"d93f0b","description":"No maintainer response within 5 business days","name":"support: response overdue"}`,
		`POST /repos/example-org/example-repo/labels? {"color":"b60205","description":"Not resolved within 10 business days","name":"support: resolution overdue"}`,
		`POST /repos/example-org/example-repo/issues/101/labels? {"labels":["support: response overdue"]}`,
		`POST /repos/example-org/example-repo/issues/104/labels? {"labels":["support: resolution overdue"]}`,
		`DELETE /repos/example-org/example-repo/issues/104/labels/support:%20response%20due? `,
		`DELETE /repos/example-org/example-repo/issues/104/labels/support:%20resolution%20due? `,
		`POST /repos/example-org/example-repo/issues/106/labels? {"labels":["support: response due"]}`,
	}, got)

	report := out.String()
	require.Contains(t, report, "## Support issue ages at 2026-11-13 12:00 UTC")
	require.Contains(t, report, "| [#101](https://github.com/example-org/example-repo/issues/101) Events missing on one site | 2026-11-06 09:00 UTC | 5.12 | **overdue** (limit 2026-11-13 09:00 UTC) | by 2026-11-20 09:00 UTC | support: response overdue |  |")
	require.Contains(t, report, "| [#104](https://github.com/example-org/example-repo/issues/104) Cannot open the web dashboard | 2026-10-30 12:00 UTC | 10.00 | met | **overdue** (limit 2026-11-13 12:00 UTC) | support: resolution overdue | support: response due, support: resolution due |")
	require.Contains(t, report, "| [#106](https://github.com/example-org/example-repo/issues/106) How do I set http.route in Express? | 2026-11-09 12:00 UTC | 4.00 | due by 2026-11-16 12:00 UTC | by 2026-11-23 12:00 UTC | support: response due |  |")
	for _, n := range []string{"#102", "#103", "#105"} {
		require.NotContains(t, report, n)
	}
	require.Contains(t, report, "Created labels: support: response overdue, support: resolution overdue.")
}

func TestDryRunChangesNothing(t *testing.T) {
	f, srv := newFake(t)
	var out, errb bytes.Buffer
	// a public repository can be read without a token
	code := run(context.Background(), []string{"-repo", "example-org/example-repo", "-api", srv.URL, "-now", "2026-11-13T12:00:00Z"},
		env(nil), &out, &errb, srv.Client(), time.Now)
	require.Equal(t, 0, code, errb.String())
	require.Empty(t, f.mutations())
	for _, c := range f.calls {
		require.Empty(t, c.Auth)
	}
	require.Contains(t, out.String(), "Dry run: no label was changed.")
	require.Contains(t, out.String(), "support: response overdue")
}

func TestMaintainerLogins(t *testing.T) {
	// with reporter-two listed as a maintainer, #104 is not a ticket at all;
	// with the bot listed, it still never counts
	var issues []ghIssue
	data, err := os.ReadFile(filepath.Join("testdata", "issues-page2.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &issues))
	m := Maintainers{Extra: map[string]bool{"reporter-two": true, "github-actions[bot]": true}}
	var tickets []int
	for _, i := range issues {
		if m.IsTicket(i) {
			tickets = append(tickets, i.Number)
		}
	}
	require.Equal(t, []int{106}, tickets)

	var cs []ghComment
	data, err = os.ReadFile(filepath.Join("testdata", "comments-101.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cs))
	require.Nil(t, m.FirstResponse("reporter-one", cs), "a bot comment is never a response")

	data, err = os.ReadFile(filepath.Join("testdata", "comments-104.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cs))
	first := Maintainers{}.FirstResponse("reporter-two", cs)
	require.NotNil(t, first)
	require.Equal(t, time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), *first)
	// the author's own comment never counts, even when listed as a maintainer
	require.Equal(t, *first, *Maintainers{Extra: map[string]bool{"reporter-two": true}}.FirstResponse("reporter-two", cs))
}

func TestTokenStaysOnTheAPIOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the other host was called with %q", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<`+other.URL+`/repositories/1/issues?page=2>; rel="next"`)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"-repo", "example-org/example-repo", "-api", srv.URL},
		env(map[string]string{"GITHUB_TOKEN": "secret"}), &out, &errb, srv.Client(), time.Now)
	require.Equal(t, 1, code)
	require.Contains(t, errb.String(), "outside the api origin")
}

func TestAPIErrorsFailTheRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// reads work, the first label change is refused, as for a token
		// without issues: write
		files := map[string]string{
			"/repos/example-org/example-repo/issues":              "issues-page2.json",
			"/repos/example-org/example-repo/issues/104/comments": "comments-104.json",
		}
		if r.Method == http.MethodGet {
			name, ok := files[r.URL.Path]
			if !ok {
				name = "label.json"
			}
			data, err := os.ReadFile(filepath.Join("testdata", name))
			require.NoError(t, err)
			_, _ = w.Write(data)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration","status":"403"}`))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"-repo", "example-org/example-repo", "-api", srv.URL, "-apply", "-now", "2026-11-13T12:00:00Z"},
		env(map[string]string{"GITHUB_TOKEN": "t"}), &out, &errb, srv.Client(), time.Now)
	require.Equal(t, 1, code)
	require.Contains(t, errb.String(), "403 Resource not accessible by integration")
	require.Contains(t, errb.String(), "issues: write")
	require.Empty(t, out.String(), "no report for a run that failed")
}

func TestUsage(t *testing.T) {
	cases := [][]string{
		{},                         // no repository
		{"-repo", "not a repo"},    // bad repository
		{"-repo", "a/b", "-apply"}, // apply without a token
		{"-repo", "a/b", "-now", "yesterday"},
		{"-repo", "a/b", "-holidays", "2026-13-01"},
		{"-repo", "a/b", "-warn-days", "5"},
		{"-repo", "a/b", "extra"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		code := run(context.Background(), args, env(nil), &out, &errb, http.DefaultClient, time.Now)
		require.Equal(t, 2, code, "%v: %s", args, errb.String())
	}
}
