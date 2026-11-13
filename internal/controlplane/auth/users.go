package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/justmert/veristar/internal/config"
)

// ErrGitHubUserNotFound is returned by Lookup for a login that names no
// github user account (github answers 404, or the login is an organization,
// which can never sign in).
var ErrGitHubUserNotFound = errors.New("auth: no github user with that login")

// GitHubUser is a github account as the public users api describes it.
type GitHubUser struct {
	ID    int64
	Login string
}

// UserLookupConfig is what a lookup needs: the oauth app credentials, which
// github accepts for public reads at the app's rate limit instead of the
// 60 requests per hour of an anonymous caller, and the api base.
type UserLookupConfig struct {
	ClientID     string
	ClientSecret config.Secret
	Endpoints    Endpoints
	// HTTPClient talks to github. nil uses a client with githubTimeout.
	HTTPClient *http.Client
}

// UserLookup resolves a github login to the numeric account id through
// GET /users/{login}. an invite and a direct
// add are bound to that id: a login can be renamed and the freed name taken
// by someone else, the id never changes hands.
type UserLookup struct {
	id      string
	secret  string
	apiBase string
	client  *http.Client
}

// NewUserLookup checks cfg.
func NewUserLookup(cfg UserLookupConfig) (*UserLookup, error) {
	if cfg.ClientID == "" || cfg.ClientSecret.Reveal() == "" || cfg.Endpoints.APIBase == "" {
		return nil, errors.New("auth: user lookup needs the client id, client secret and api base")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: githubTimeout}
	}
	return &UserLookup{
		id: cfg.ClientID, secret: cfg.ClientSecret.Reveal(),
		apiBase: strings.TrimSuffix(cfg.Endpoints.APIBase, "/"), client: client,
	}, nil
}

// Lookup returns the user account with that login (github compares logins
// case-insensitively). ErrGitHubUserNotFound when there is none; any other
// error means github could not answer.
func (l *UserLookup) Lookup(ctx context.Context, login string) (GitHubUser, error) {
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.apiBase+"/users/"+url.PathEscape(login), nil)
	if err != nil {
		return GitHubUser{}, err
	}
	req.SetBasicAuth(l.id, l.secret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	res, err := l.client.Do(req)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("github /users: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return GitHubUser{}, ErrGitHubUserNotFound
	}
	if res.StatusCode != http.StatusOK {
		return GitHubUser{}, fmt.Errorf("github /users answered %d", res.StatusCode)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&u); err != nil {
		return GitHubUser{}, fmt.Errorf("github /users: %w", err)
	}
	if u.ID <= 0 || u.Login == "" {
		return GitHubUser{}, errors.New("github /users: id or login missing")
	}
	if u.Type != "User" {
		return GitHubUser{}, ErrGitHubUserNotFound
	}
	return GitHubUser{ID: u.ID, Login: u.Login}, nil
}
