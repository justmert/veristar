package umami

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewRejectsBadBase(t *testing.T) {
	for _, raw := range []string{"umami:3000", "ftp://umami", "/api"} {
		u, _ := url.Parse(raw)
		if _, err := New(u, nil); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := New(nil, nil); err == nil {
		t.Error("accepted nil url")
	}
}

func TestTokenNeverPrints(t *testing.T) {
	tok := Token{v: "eyJhbGciOiJIUzI1NiJ9.secretpart"}
	s := fmt.Sprintf("%v %s %+v %#v", tok, tok, Session{Token: tok}, tok)
	if strings.Contains(s, "secretpart") {
		t.Fatalf("token leaked: %s", s)
	}
}

func TestUnauthorizedMatches(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &APIError{Op: "login", Status: 401, Code: "incorrect-username-password"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal("401 does not match ErrUnauthorized")
	}
	if errors.Is(&APIError{Status: 400}, ErrUnauthorized) {
		t.Fatal("400 matches ErrUnauthorized")
	}
}

// integration tests run against a real umami, normally the dev stack
// (make test-integration). they never change state on the instance.
func liveClient(t *testing.T) *Client {
	t.Helper()
	raw := os.Getenv("UMAMI_IT_URL")
	if raw == "" {
		t.Skip("UMAMI_IT_URL not set; run make test-integration against the dev stack")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLiveHeartbeatAndLogin(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := c.Login(ctx, SeededUsername, "definitely-not-the-password")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: want ErrUnauthorized, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "incorrect-username-password" {
		t.Fatalf("unexpected error body: %v", err)
	}
}

func TestLiveBootstrappedState(t *testing.T) {
	c := liveClient(t)
	pw := os.Getenv("UMAMI_ADMIN_PASSWORD")
	if pw == "" {
		t.Skip("UMAMI_ADMIN_PASSWORD not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := c.Login(ctx, SeededUsername, SeededPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("seeded password: want ErrUnauthorized (run platformctl umami bootstrap first), got %v", err)
	}
	s, err := c.Login(ctx, SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Token.Empty() || !s.User.IsAdmin || s.User.PasswordChangeRequired {
		t.Fatalf("unexpected session: %+v", s.User)
	}
}
