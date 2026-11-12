// Package umami is a client for the parts of the umami rest api the platform uses.
//
// behaviour is written against umami v3.5.0. route files are cited next to
// each call so a version bump can be checked against them.
package umami

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token is an umami session token. it formats as a fixed marker so it never
// ends up in a log line or an error message.
type Token struct{ v string }

const redacted = "[redacted]"

// Reveal returns the raw token for use in an Authorization header.
func (t Token) Reveal() string { return t.v }

func (t Token) String() string { return redacted }

func (t Token) GoString() string { return redacted }

func (t Token) LogValue() slog.Value { return slog.StringValue(redacted) }

// Empty reports whether the token holds no value.
func (t Token) Empty() bool { return t.v == "" }

// User is the subset of umami's user object the platform reads.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	IsAdmin  bool   `json:"isAdmin"`
	// PasswordChangeRequired is true while the seeded admin still has the
	// seeded password hash (src/lib/password.ts hasDefaultPassword).
	PasswordChangeRequired bool `json:"passwordChangeRequired"`
	// Teams lists the user's teams in the login and verify answers
	// (src/app/api/auth/login/route.ts getAllUserTeams); other routes leave
	// it empty.
	Teams []Team `json:"teams"`
}

// Session is the result of a successful login.
type Session struct {
	Token Token
	User  User
}

// APIError is a non-success answer from umami. umami replies with
// {"error":{"message","code","status"}} (src/lib/response.ts).
type APIError struct {
	Op      string
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("umami %s: http %d %s: %s", e.Op, e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("umami %s: http %d", e.Op, e.Status)
}

// ErrUnauthorized matches any APIError with status 401.
var ErrUnauthorized = errors.New("umami: unauthorized")

// ErrNotFound matches any APIError with status 404.
var ErrNotFound = errors.New("umami: not found")

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	}
	return false
}

// Client talks to one umami instance.
type Client struct {
	base *url.URL
	hc   *http.Client
}

// DefaultTimeout bounds every request made by a client built with a nil http.Client.
const DefaultTimeout = 15 * time.Second

// New returns a client for the umami instance at base. a nil hc gets a
// client with DefaultTimeout.
func New(base *url.URL, hc *http.Client) (*Client, error) {
	if base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("umami: base url must be an absolute http or https url")
	}
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	b := *base
	b.Path = strings.TrimSuffix(b.Path, "/")
	return &Client{base: &b, hc: hc}, nil
}

// maxBody caps how much of a response we read. the largest answer used here is
// a login response with the user's team list.
const maxBody = 1 << 20

func (c *Client) do(ctx context.Context, op, method, path string, token Token, in, out any) error {
	return c.doHeaders(ctx, op, method, path, token, nil, in, out)
}

// doHeaders is do with extra request headers. only /api/send needs them.
func (c *Client) doHeaders(ctx context.Context, op, method, path string, token Token, hdr http.Header, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("umami %s: encode request: %w", op, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return fmt.Errorf("umami %s: %w", op, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !token.Empty() {
		req.Header.Set("Authorization", "Bearer "+token.Reveal())
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// url.Error carries only the url, which holds no credential here
		return fmt.Errorf("umami %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("umami %s: read response: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Op: op, Status: resp.StatusCode}
		var env struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("umami %s: decode response (http %d): %w", op, resp.StatusCode, err)
	}
	return nil
}

// Heartbeat calls GET /api/heartbeat, which answers {"ok":true} without
// touching the database (src/app/api/heartbeat/route.ts).
func (c *Client) Heartbeat(ctx context.Context) error {
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.do(ctx, "heartbeat", http.MethodGet, "/api/heartbeat", Token{}, nil, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New(`umami heartbeat: response did not contain "ok":true`)
	}
	return nil
}

// Login calls POST /api/auth/login (src/app/api/auth/login/route.ts). a wrong
// username or password returns an error matching ErrUnauthorized.
//
// a user with 2fa enabled gets a partial token instead of a session. service
// accounts never enable 2fa, so that answer is returned as an error.
func (c *Client) Login(ctx context.Context, username, password string) (Session, error) {
	in := map[string]string{"username": username, "password": password}
	var out struct {
		Token             string `json:"token"`
		User              User   `json:"user"`
		RequiresTwoFactor bool   `json:"requiresTwoFactor"`
	}
	if err := c.do(ctx, "login", http.MethodPost, "/api/auth/login", Token{}, in, &out); err != nil {
		return Session{}, err
	}
	if out.RequiresTwoFactor {
		return Session{}, errors.New("umami login: account requires two-factor verification, which service accounts must not use")
	}
	if out.Token == "" || out.User.ID == "" {
		return Session{}, errors.New("umami login: response is missing token or user id")
	}
	return Session{Token: Token{v: out.Token}, User: out.User}, nil
}

// ChangeOwnPassword calls POST /api/me/password (src/app/api/me/password/route.ts).
// umami checks currentPassword, requires newPassword to be at least 8
// characters, and invalidates every older token of the user because tokens
// are bound to the password hash. the returned token is the new session.
func (c *Client) ChangeOwnPassword(ctx context.Context, token Token, current, next string) (Token, error) {
	if token.Empty() {
		return Token{}, errors.New("umami change password: no session token")
	}
	in := map[string]string{"currentPassword": current, "newPassword": next}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, "change password", http.MethodPost, "/api/me/password", token, in, &out); err != nil {
		return Token{}, err
	}
	if out.Token == "" {
		return Token{}, errors.New("umami change password: response is missing the new token")
	}
	return Token{v: out.Token}, nil
}
