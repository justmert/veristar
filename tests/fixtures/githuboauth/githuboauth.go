// Package githuboauth is a TEST-ONLY stand-in for github's oauth web flow. it
// is never part of a product build: the control plane reaches it only when
// compiled with the oauthfixture build tag and OAUTH_FIXTURE_URL set, and
// production binaries are built without that tag.
//
// it implements the three github endpoints the control plane uses, with the
// behaviour documented by github (docs/reference/the reference docs section 1):
//
//	GET  /login/oauth/authorize     consent page; the test chooses the user
//	POST /login/oauth/authorize     the consent decision, redirects with code or error
//	POST /login/oauth/access_token  code exchange with pkce s256 verification
//	GET  /user                      the public user response (id, login, ...)
//
// and a control api under /_fixture/ for test runners: every authorize, token
// and /user request is recorded so a test can assert what the client sent
// (no scope, pkce s256, state echo, api version header).
//
// rules it enforces, so a client that breaks one fails here as it would on
// github: client_id must be the registered one; redirect_uri, when sent, must
// equal the registered callback exactly; code_challenge must be 43 base64url
// characters with code_challenge_method S256 (plain is refused, as github
// refuses it); codes are single use and expire after 10 minutes; a token
// exchange error is answered with http 200 and an error field, form-encoded
// unless the client asks for json, as github does.
package githuboauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CodeTTL is github's documented lifetime of an authorization code.
const CodeTTL = 10 * time.Minute

// error_uri values github sends; kept so clients see the same shape
const (
	docsAuthorize = "https://docs.github.com/apps/managing-oauth-apps/troubleshooting-authorization-request-errors"
	docsToken     = "https://docs.github.com/apps/managing-oauth-apps/troubleshooting-oauth-app-access-token-request-errors" //nolint:gosec // a documentation url, not a credential
)

// Config is the registered oauth app the fixture accepts.
type Config struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is the registered callback url. github compares a sent
	// redirect_uri with it; wildcard matching is off an operator input.
	RedirectURL string
	// Now is the clock; nil means time.Now. tests move it to expire codes.
	Now func() time.Time
}

// User is the identity the consent page signs in as.
type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// AuthorizeRecord is one GET /login/oauth/authorize as the browser sent it.
type AuthorizeRecord struct {
	At    time.Time           `json:"at"`
	Query map[string][]string `json:"query"`
	// HasScope is true when the query carried a scope parameter at all,
	// also an empty one
	HasScope bool `json:"has_scope"`
}

// TokenRecord is one POST /login/oauth/access_token.
type TokenRecord struct {
	At time.Time `json:"at"`
	// Form holds the request parameters with client_secret and
	// code_verifier replaced by "<set>" or "" so records can be printed
	Form map[string]string `json:"form"`
	// BasicAuth is true when credentials came in an authorization header
	BasicAuth bool   `json:"basic_auth"`
	Accept    string `json:"accept"`
	// Result is "ok" or the github error code that was answered
	Result string `json:"result"`
}

// UserRecord is one GET /user.
type UserRecord struct {
	At         time.Time `json:"at"`
	Accept     string    `json:"accept"`
	APIVersion string    `json:"api_version"`
	// Scheme is the authorization scheme the client used (bearer or token)
	Scheme string `json:"scheme"`
	Status int    `json:"status"`
}

type grant struct {
	user        User
	challenge   string
	redirectURI string
	clientID    string
	expires     time.Time
}

// Server is the fixture. the zero value is not usable; call New.
type Server struct {
	cfg Config

	mu         sync.Mutex
	codes      map[string]grant
	tokens     map[string]User
	authorizes []AuthorizeRecord
	tokenReqs  []TokenRecord
	userReqs   []UserRecord
}

// New returns a fixture for one registered oauth app.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{cfg: cfg, codes: map[string]grant{}, tokens: map[string]User{}}
}

// Handler serves the github endpoints and the control api.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login/oauth/authorize", s.authorizePage)
	mux.HandleFunc("POST /login/oauth/authorize", s.authorizeDecision)
	mux.HandleFunc("POST /login/oauth/access_token", s.accessToken)
	mux.HandleFunc("GET /user", s.user)
	mux.HandleFunc("GET /_fixture/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /_fixture/requests", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Requests())
	})
	mux.HandleFunc("POST /_fixture/reset", func(w http.ResponseWriter, _ *http.Request) {
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /_fixture/revoke-tokens", func(w http.ResponseWriter, _ *http.Request) {
		// lets a test make /user fail after a successful exchange
		s.mu.Lock()
		s.tokens = map[string]User{}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	return mux
}

// Requests is a snapshot of every recorded request.
type Requests struct {
	Authorize []AuthorizeRecord `json:"authorize"`
	Token     []TokenRecord     `json:"token"`
	User      []UserRecord      `json:"user"`
}

// Requests returns copies of the recorded requests.
func (s *Server) Requests() Requests {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Requests{
		Authorize: append([]AuthorizeRecord{}, s.authorizes...),
		Token:     append([]TokenRecord{}, s.tokenReqs...),
		User:      append([]UserRecord{}, s.userReqs...),
	}
}

// Reset forgets codes, tokens and records.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes = map[string]grant{}
	s.tokens = map[string]User{}
	s.authorizes, s.tokenReqs, s.userReqs = nil, nil, nil
}

// github's code_challenge rule: "a 43 character SHA-256 hash", which is the
// unpadded base64url encoding of 32 bytes
var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// github login syntax: alphanumeric or single hyphens, no leading or
// trailing hyphen, at most 39 characters
var loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)

// authorizeParams validates what github validates before it shows consent.
// a non-empty problem means the page answers with an error instead.
func (s *Server) authorizeParams(q url.Values) (redirect string, problem string) {
	if q.Get("client_id") != s.cfg.ClientID {
		// github shows a 404 page for an unknown client id
		return "", "unknown client_id"
	}
	redirect = s.cfg.RedirectURL
	if r := q.Get("redirect_uri"); r != "" && r != s.cfg.RedirectURL {
		return "", "redirect_uri_mismatch"
	}
	switch m := q.Get("code_challenge_method"); {
	case q.Get("code_challenge") == "" && m == "":
		// github does not require pkce yet; the fixture does, because the
		// control plane must always send it (contract 4.1.1)
		return redirect, "pkce required by the fixture"
	case m != "S256":
		return redirect, "code_challenge_method must be S256"
	case !challengeRe.MatchString(q.Get("code_challenge")):
		return redirect, "code_challenge must be 43 base64url characters"
	}
	if q.Get("state") == "" {
		// github accepts a missing state; the fixture refuses it so a client
		// without csrf protection fails its tests
		return redirect, "state required by the fixture"
	}
	return redirect, ""
}

var page = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Test-only GitHub OAuth fixture</title></head>
<body>
<p id="fixture-banner"><strong>TEST-ONLY GitHub OAuth fixture.</strong> Not GitHub. Used only by automated tests.</p>
{{if .Problem}}<p id="fixture-error">{{.Problem}}</p>{{else}}
<h1>Authorize application</h1>
<form method="post" action="/login/oauth/authorize">
{{range $k, $vs := .Query}}{{range $vs}}<input type="hidden" name="{{$k}}" value="{{.}}">{{end}}{{end}}
<label>GitHub user id <input id="fixture-user-id" name="fixture_user_id" inputmode="numeric" required></label>
<label>GitHub login <input id="fixture-login" name="fixture_login" required></label>
<button id="fixture-authorize" type="submit" name="fixture_decision" value="authorize">Authorize</button>
<button id="fixture-cancel" type="submit" name="fixture_decision" value="cancel" formnovalidate>Cancel</button>
</form>{{end}}
</body></html>`))

func (s *Server) authorizePage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, hasScope := q["scope"]
	s.mu.Lock()
	s.authorizes = append(s.authorizes, AuthorizeRecord{At: s.cfg.Now().UTC(), Query: q, HasScope: hasScope})
	s.mu.Unlock()
	_, problem := s.authorizeParams(q)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if problem == "unknown client_id" {
		status = http.StatusNotFound
	} else if problem != "" {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	_ = page.Execute(w, map[string]any{"Query": q, "Problem": problem})
}

func (s *Server) authorizeDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	q := url.Values{}
	for k, v := range r.PostForm {
		if !strings.HasPrefix(k, "fixture_") {
			q[k] = v
		}
	}
	redirect, problem := s.authorizeParams(q)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "registered redirect url is invalid", http.StatusInternalServerError)
		return
	}
	out := target.Query()
	// github echoes state unchanged on success and on error
	out.Set("state", q.Get("state"))
	if r.PostForm.Get("fixture_decision") != "authorize" {
		out.Set("error", "access_denied")
		out.Set("error_description", "The user has denied your application access.")
		out.Set("error_uri", docsAuthorize+"#access-denied")
		target.RawQuery = out.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}
	id, err := strconv.ParseInt(r.PostForm.Get("fixture_user_id"), 10, 64)
	login := r.PostForm.Get("fixture_login")
	if err != nil || id <= 0 || !loginRe.MatchString(login) {
		http.Error(w, "fixture needs a positive numeric user id and a valid github login", http.StatusBadRequest)
		return
	}
	code := randomToken(20)
	s.mu.Lock()
	s.codes[code] = grant{
		user:        User{ID: id, Login: login},
		challenge:   q.Get("code_challenge"),
		redirectURI: q.Get("redirect_uri"),
		clientID:    q.Get("client_id"),
		expires:     s.cfg.Now().Add(CodeTTL),
	}
	s.mu.Unlock()
	out.Set("code", code)
	target.RawQuery = out.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// tokenError answers like github: status 200 with an error field.
func tokenError(w http.ResponseWriter, asJSON bool, code, desc string) {
	body := map[string]string{"error": code, "error_description": desc, "error_uri": docsToken + "#" + strings.ReplaceAll(code, "_", "-")}
	writeTokenBody(w, asJSON, body)
}

func writeTokenBody(w http.ResponseWriter, asJSON bool, body map[string]string) {
	w.Header().Set("Cache-Control", "no-store")
	if asJSON {
		writeJSON(w, http.StatusOK, body)
		return
	}
	v := url.Values{}
	for k, val := range body {
		v.Set(k, val)
	}
	w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(v.Encode()))
}

func (s *Server) accessToken(w http.ResponseWriter, r *http.Request) {
	accept := r.Header.Get("Accept")
	asJSON := strings.Contains(accept, "application/json")
	if err := r.ParseForm(); err != nil {
		tokenError(w, asJSON, "bad_verification_code", "The code passed is incorrect or expired.")
		return
	}
	clientID, clientSecret, basic := r.BasicAuth()
	if !basic {
		clientID, clientSecret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	rec := TokenRecord{At: s.cfg.Now().UTC(), BasicAuth: basic, Accept: accept, Form: map[string]string{}}
	for k := range r.PostForm {
		v := r.PostForm.Get(k)
		if (k == "client_secret" || k == "code_verifier") && v != "" {
			v = "<set>"
		}
		rec.Form[k] = v
	}
	result := s.exchange(w, asJSON, clientID, clientSecret, r.PostForm)
	rec.Result = result
	s.mu.Lock()
	s.tokenReqs = append(s.tokenReqs, rec)
	s.mu.Unlock()
}

// exchange answers one token request and returns "ok" or the error code.
func (s *Server) exchange(w http.ResponseWriter, asJSON bool, clientID, clientSecret string, form url.Values) string {
	if subtle.ConstantTimeCompare([]byte(clientID), []byte(s.cfg.ClientID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(clientSecret), []byte(s.cfg.ClientSecret)) != 1 {
		tokenError(w, asJSON, "incorrect_client_credentials", "The client_id and/or client_secret passed are incorrect.")
		return "incorrect_client_credentials"
	}
	code := form.Get("code")
	s.mu.Lock()
	g, ok := s.codes[code]
	// single use: any exchange attempt for a known code consumes it, so a
	// wrong verifier cannot be retried against the same code
	delete(s.codes, code)
	s.mu.Unlock()
	if !ok || s.cfg.Now().After(g.expires) || g.clientID != clientID {
		tokenError(w, asJSON, "bad_verification_code", "The code passed is incorrect or expired.")
		return "bad_verification_code"
	}
	if ru := form.Get("redirect_uri"); ru != "" && ru != s.cfg.RedirectURL {
		tokenError(w, asJSON, "redirect_uri_mismatch", "The redirect_uri MUST match the registered callback URL for this application.")
		return "redirect_uri_mismatch"
	}
	verifier := form.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	// rfc 7636: the verifier is 43 to 128 characters of the unreserved set
	if len(verifier) < 43 || len(verifier) > 128 || subtle.ConstantTimeCompare([]byte(got), []byte(g.challenge)) != 1 {
		// github does not document a separate code for a failed pkce check;
		// the code is refused like any other invalid one
		tokenError(w, asJSON, "bad_verification_code", "The code passed is incorrect or expired.")
		return "bad_verification_code"
	}
	token := "gho_" + randomToken(27)
	s.mu.Lock()
	s.tokens[token] = g.user
	s.mu.Unlock()
	// no scope was granted, which github reports as an empty scope
	writeTokenBody(w, asJSON, map[string]string{"access_token": token, "scope": "", "token_type": "bearer"})
	return "ok"
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) {
	rec := UserRecord{At: s.cfg.Now().UTC(), Accept: r.Header.Get("Accept"), APIVersion: r.Header.Get("X-GitHub-Api-Version")}
	scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	rec.Scheme = strings.ToLower(scheme)
	s.mu.Lock()
	u, ok := s.tokens[token]
	s.mu.Unlock()
	if (rec.Scheme != "bearer" && rec.Scheme != "token") || !ok {
		rec.Status = http.StatusUnauthorized
		s.record(rec)
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"message": "Bad credentials", "documentation_url": "https://docs.github.com/rest", "status": "401",
		})
		return
	}
	rec.Status = http.StatusOK
	s.record(rec)
	// the public user response; a token without read:user gets exactly this
	writeJSON(w, http.StatusOK, map[string]any{
		"login":            u.Login,
		"id":               u.ID,
		"node_id":          "U_fixture" + strconv.FormatInt(u.ID, 10),
		"avatar_url":       "https://avatars.githubusercontent.com/u/" + strconv.FormatInt(u.ID, 10) + "?v=4",
		"gravatar_id":      "",
		"url":              "https://api.github.com/users/" + u.Login,
		"html_url":         "https://github.com/" + u.Login,
		"type":             "User",
		"user_view_type":   "public",
		"site_admin":       false,
		"name":             nil,
		"company":          nil,
		"blog":             "",
		"location":         nil,
		"email":            nil,
		"hireable":         nil,
		"bio":              nil,
		"twitter_username": nil,
		"public_repos":     0,
		"public_gists":     0,
		"followers":        0,
		"following":        0,
		"created_at":       "2020-01-01T00:00:00Z",
		"updated_at":       "2026-01-01T00:00:00Z",
	})
}

func (s *Server) record(rec UserRecord) {
	s.mu.Lock()
	s.userReqs = append(s.userReqs, rec)
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
