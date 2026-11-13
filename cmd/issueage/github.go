package main

import (
	"bytes"
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
	"time"
)

// the subset of the github rest api objects the tool reads. the shapes were
// recorded from api.github.com (see the testdata comment in github_test.go).
type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghIssue struct {
	Number            int              `json:"number"`
	Title             string           `json:"title"`
	HTMLURL           string           `json:"html_url"`
	State             string           `json:"state"`
	User              ghUser           `json:"user"`
	Labels            []ghLabel        `json:"labels"`
	Comments          int              `json:"comments"`
	CreatedAt         time.Time        `json:"created_at"`
	AuthorAssociation string           `json:"author_association"`
	PullRequest       *json.RawMessage `json:"pull_request"`
}

type ghComment struct {
	User              ghUser    `json:"user"`
	CreatedAt         time.Time `json:"created_at"`
	AuthorAssociation string    `json:"author_association"`
}

// Maintainers decides who answers support issues. github marks repository
// owners, organisation members and collaborators in author_association;
// Extra adds logins whose association github does not report that way.
type Maintainers struct {
	Extra map[string]bool
}

func (m Maintainers) Is(u ghUser, association string) bool {
	if strings.EqualFold(u.Type, "Bot") {
		return false
	}
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return m.Extra[strings.ToLower(u.Login)]
}

// IsTicket reports whether an open issue is a support ticket: not a pull
// request, not opened by a bot (the hourly security workflow files those and
// they follow the 24 hour patch runbook instead), not opened by a maintainer.
func (m Maintainers) IsTicket(i ghIssue) bool {
	return i.PullRequest == nil && !strings.EqualFold(i.User.Type, "Bot") && !m.Is(i.User, i.AuthorAssociation)
}

// FirstResponse is the earliest comment by a maintainer other than the
// issue's author, or nil.
func (m Maintainers) FirstResponse(author string, cs []ghComment) *time.Time {
	var first *time.Time
	for _, c := range cs {
		if strings.EqualFold(c.User.Login, author) || !m.Is(c.User, c.AuthorAssociation) {
			continue
		}
		if first == nil || c.CreatedAt.Before(*first) {
			t := c.CreatedAt
			first = &t
		}
	}
	return first
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

// Client is a small github rest client. the token is only ever sent to the
// api origin it was configured with.
type Client struct {
	base  *url.URL
	repo  string
	token string
	http  *http.Client
}

func NewClient(api, repo, token string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(api, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("api url %q is not absolute", api)
	}
	if !repoRe.MatchString(repo) {
		return nil, fmt.Errorf("repository %q: want owner/name", repo)
	}
	return &Client{base: u, repo: repo, token: token, http: hc}, nil
}

// url joins the base (which may carry a path, as on github enterprise) with
// a path whose segments are already escaped, so label names with spaces and
// colons arrive exactly as escaped.
func (c *Client) url(path string, q url.Values) string {
	s := strings.TrimRight(c.base.String(), "/") + path
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

type apiError struct {
	Method, URL string
	Status      int
	Message     string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, rawURL string, body any) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return nil, fmt.Errorf("refusing to send the token to %s://%s, outside the api origin", u.Scheme, u.Host)
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "veristar-issueage")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var m struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(b))
		if json.Unmarshal(b, &m) == nil && m.Message != "" {
			msg = m.Message
		}
		return nil, &apiError{Method: method, URL: rawURL, Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

var nextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// getAll follows the Link rel="next" header. github's issue listing pages
// with an opaque cursor (after=...), so the next url is taken as given
// rather than built from a page number.
func getAll[T any](ctx context.Context, c *Client, first string) ([]T, error) {
	var out []T
	for next := first; next != ""; {
		resp, err := c.do(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		var page []T
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", next, err)
		}
		out = append(out, page...)
		next = ""
		if m := nextRe.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return out, nil
}

func (c *Client) OpenIssues(ctx context.Context) ([]ghIssue, error) {
	q := url.Values{"state": {"open"}, "per_page": {"100"}}
	return getAll[ghIssue](ctx, c, c.url("/repos/"+c.repo+"/issues", q))
}

func (c *Client) Comments(ctx context.Context, number int) ([]ghComment, error) {
	q := url.Values{"per_page": {"100"}}
	return getAll[ghComment](ctx, c, c.url("/repos/"+c.repo+"/issues/"+strconv.Itoa(number)+"/comments", q))
}

// EnsureLabel creates the label when the repository does not have it yet.
func (c *Client) EnsureLabel(ctx context.Context, l LabelSpec) (created bool, err error) {
	resp, err := c.do(ctx, http.MethodGet, c.url("/repos/"+c.repo+"/labels/"+url.PathEscape(l.Name), nil), nil)
	if err == nil {
		_ = resp.Body.Close()
		return false, nil
	}
	var ae *apiError
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		return false, err
	}
	resp, err = c.do(ctx, http.MethodPost, c.url("/repos/"+c.repo+"/labels", nil),
		map[string]string{"name": l.Name, "color": l.Color, "description": l.Description})
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	return true, nil
}

func (c *Client) AddLabels(ctx context.Context, number int, names []string) error {
	resp, err := c.do(ctx, http.MethodPost, c.url("/repos/"+c.repo+"/issues/"+strconv.Itoa(number)+"/labels", nil),
		map[string][]string{"labels": names})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) RemoveLabel(ctx context.Context, number int, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.url("/repos/"+c.repo+"/issues/"+strconv.Itoa(number)+"/labels/"+url.PathEscape(name), nil), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
