// Package auth is console sign-in with github (contract 4.1): the oauth web
// flow with state and pkce s256, sign-out, and /api/me.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/terms"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// failure codes of the callback (contract 4.1.2), sent to the console as
// /sign-in?error=<code>
const (
	failStateMismatch     = "state_mismatch"
	failAccessDenied      = "access_denied"
	failGitHubError       = "github_error"
	failExchangeFailed    = "exchange_failed"
	failGitHubUnavailable = "github_unavailable"
	failInternal          = "internal"
)

// the /user api version is pinned because its end date is known
// (the reference docs 1.4)
const githubAPIVersion = "2022-11-28"

// githubTimeout bounds each call to github, so a slow github cannot hold
// the callback request open.
const githubTimeout = 10 * time.Second

const maxReturnTo = 512

// Config is what the sign-in flow needs.
type Config struct {
	ClientID     string
	ClientSecret config.Secret
	// RedirectURL is GITHUB_REDIRECT_URL.
	RedirectURL string
	// StateKey seals the sa_oauth cookie.
	StateKey []byte
	// Operators are the github ids whose users get is_operator.
	Operators map[int64]bool
	Endpoints Endpoints
	// HTTPClient talks to github. nil uses a client with githubTimeout.
	HTTPClient *http.Client
	// Reconcile starts the provisioning reconciler for a tenant without
	// waiting for it (tenants.Scope.Reconcile). sign-in calls it for every
	// tenant an invite made the user a member of, so the umami membership
	// exists by the time the person opens a dashboard.
	Reconcile func(ctx context.Context, tenantID uuid.UUID)
}

// Handler serves the auth routes.
type Handler struct {
	oauth     *oauth2.Config
	apiBase   string
	sealer    *sealer
	operators map[int64]bool
	client    *http.Client
	db        *db.DB
	sessions  *session.Manager
	reconcile func(ctx context.Context, tenantID uuid.UUID)
	starts    *bucket
	now       func() time.Time
}

// New checks cfg and returns the handler.
func New(cfg Config, d *db.DB, s *session.Manager) (*Handler, error) {
	if cfg.ClientID == "" || cfg.ClientSecret.Reveal() == "" || cfg.RedirectURL == "" {
		return nil, errors.New("auth: client id, client secret and redirect url are required")
	}
	if cfg.Endpoints.OAuth.AuthURL == "" || cfg.Endpoints.OAuth.TokenURL == "" || cfg.Endpoints.APIBase == "" {
		return nil, errors.New("auth: endpoints are not set")
	}
	if cfg.Reconcile == nil {
		return nil, errors.New("auth: Reconcile is required, invites resolved at sign-in need umami access")
	}
	sl, err := newSealer(cfg.StateKey)
	if err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: githubTimeout}
	}
	return &Handler{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret.Reveal(),
			Endpoint:     cfg.Endpoints.OAuth,
			RedirectURL:  cfg.RedirectURL,
			// no scopes: the public /user answer has id and login, which is
			// all the platform stores (the reference docs 1.3)
		},
		apiBase:   strings.TrimSuffix(cfg.Endpoints.APIBase, "/"),
		sealer:    sl,
		operators: cfg.Operators,
		client:    client,
		db:        d,
		sessions:  s,
		reconcile: cfg.Reconcile,
		starts:    newBucket(StartRatePerSecond, StartBurst, time.Now()),
		now:       time.Now,
	}, nil
}

// Public registers the routes that need no session.
func (h *Handler) Public(r chi.Router) {
	r.Method(http.MethodGet, "/auth/github/start", httpx.HandlerFunc(h.start))
	r.Method(http.MethodGet, "/auth/github/callback", http.HandlerFunc(h.callback))
	// logout without a session is a 204 too (contract 4.1.3), so it is not
	// behind RequireUser; the csrf rules still apply to it
	r.Method(http.MethodPost, "/auth/logout", httpx.HandlerFunc(h.logout))
}

// Private registers the routes behind RequireUser.
func (h *Handler) Private(r chi.Router) {
	r.Method(http.MethodGet, "/me", httpx.HandlerFunc(h.me))
}

// cleanReturnTo keeps only a console path: one leading slash, not a
// protocol-relative or backslash form that a browser would read as another
// host, no control characters, at most 512 characters. anything else is /.
func cleanReturnTo(s string) string {
	if s == "" || len(s) > maxReturnTo || !strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(s, `/\`) {
		return "/"
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f || s[i] == '\\' {
			return "/"
		}
	}
	return s
}

// start answers 302 to github's authorize page with fresh state and a pkce
// challenge, and keeps both secrets in the sealed sa_oauth cookie.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) error {
	// every start sets a cookie and sends a browser to github; the bucket
	// keeps a flood of them from reaching github's limits for our client id
	if ok, wait := h.starts.take(h.now()); !ok {
		return httpx.RateLimited(wait)
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	verifier := oauth2.GenerateVerifier()
	sealed, err := h.sealer.seal(statePayload{
		State:    state,
		Verifier: verifier,
		ReturnTo: cleanReturnTo(r.URL.Query().Get("return_to")),
		Exp:      h.now().Add(stateCookieTTL).Unix(),
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, stateCookieFor(sealed, int(stateCookieTTL/time.Second)))
	dest := h.oauth.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("allow_signup", "true"))
	http.Redirect(w, r, dest, http.StatusFound)
	return nil
}

// fail sends the browser back to the console sign-in page with a code.
func fail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/sign-in?error="+url.QueryEscape(code), http.StatusSeeOther)
}

// callback finishes sign-in (contract 4.1.2).
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	log := httpx.Logger(r.Context())
	// the cookie is single use whatever happens next
	http.SetCookie(w, stateCookieFor("", -1))

	q := r.URL.Query()
	c, err := r.Cookie(stateCookie)
	if err != nil {
		log.Info("sign-in refused", "reason", failStateMismatch, "detail", "no state cookie")
		fail(w, r, failStateMismatch)
		return
	}
	p, err := h.sealer.open(c.Value, h.now())
	if err != nil || subtle.ConstantTimeCompare([]byte(p.State), []byte(q.Get("state"))) != 1 {
		log.Info("sign-in refused", "reason", failStateMismatch)
		fail(w, r, failStateMismatch)
		return
	}
	if e := q.Get("error"); e != "" {
		code := failGitHubError
		if e == "access_denied" {
			code = failAccessDenied
		}
		// github's error name is a fixed vocabulary, safe to log
		log.Info("sign-in refused by github", "reason", code, "github_error", e)
		fail(w, r, code)
		return
	}
	code := q.Get("code")
	if code == "" {
		log.Info("sign-in refused", "reason", failGitHubError, "detail", "no code")
		fail(w, r, failGitHubError)
		return
	}

	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, h.client)
	tok, err := h.oauth.Exchange(ctx, code, oauth2.VerifierOption(p.Verifier))
	if err != nil {
		log.Info("sign-in refused", "reason", failExchangeFailed, "error", exchangeErrorText(err))
		fail(w, r, failExchangeFailed)
		return
	}
	// the access token is used for this one call and never stored
	gh, err := h.githubUser(r.Context(), tok.AccessToken)
	if err != nil {
		log.Warn("sign-in failed", "reason", failGitHubUnavailable, "error", err.Error())
		fail(w, r, failGitHubUnavailable)
		return
	}

	u, joined, err := h.signIn(r.Context(), gh)
	if err != nil {
		log.Error("sign-in failed", "reason", failInternal, "error", err.Error())
		fail(w, r, failInternal)
		return
	}
	// after the commit, so the reconciler sees the new memberships
	for _, tenantID := range joined {
		h.reconcile(r.Context(), tenantID)
	}
	if err := h.sessions.SignIn(r.Context(), u.ID); err != nil {
		log.Error("sign-in failed", "reason", failInternal, "error", err.Error())
		fail(w, r, failInternal)
		return
	}
	log.Info("signed in", "user_id", u.ID.String())
	http.Redirect(w, r, p.ReturnTo, http.StatusSeeOther)
}

// exchangeErrorText keeps only the oauth error code of a failed exchange,
// never the response body.
func exchangeErrorText(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode != "" {
			return "oauth error " + re.ErrorCode
		}
		if re.Response != nil {
			return fmt.Sprintf("token endpoint answered %d", re.Response.StatusCode)
		}
	}
	return "token request failed"
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// githubUser reads id and login of the token's owner.
func (h *Handler) githubUser(ctx context.Context, accessToken string) (githubUser, error) {
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiBase+"/user", nil)
	if err != nil {
		return githubUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	res, err := h.client.Do(req)
	if err != nil {
		return githubUser{}, fmt.Errorf("github /user: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return githubUser{}, fmt.Errorf("github /user answered %d", res.StatusCode)
	}
	var u githubUser
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&u); err != nil {
		return githubUser{}, fmt.Errorf("github /user: %w", err)
	}
	if u.ID <= 0 || u.Login == "" {
		return githubUser{}, errors.New("github /user: id or login missing")
	}
	return u, nil
}

// signIn upserts the user, resolves open invites for the login into
// memberships and writes the audit rows, in one cp_system transaction. it
// returns the tenants the user joined.
func (h *Handler) signIn(ctx context.Context, gh githubUser) (systemq.CpUser, []uuid.UUID, error) {
	var u systemq.CpUser
	var joined []uuid.UUID
	err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		joined = nil
		q := systemq.New(tx)
		var err error
		u, err = q.SignInUser(ctx, systemq.SignInUserParams{
			GithubID: gh.ID, GithubLogin: gh.Login, IsOperator: h.operators[gh.ID],
		})
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		actor := "user:" + u.ID.String()
		resolved, err := q.ResolveInvites(ctx, systemq.ResolveInvitesParams{GithubLogin: gh.Login, UserID: u.ID})
		if err != nil {
			return fmt.Errorf("resolve invites: %w", err)
		}
		for _, inv := range resolved {
			if inv.Added {
				joined = append(joined, inv.TenantID)
			}
			if err := audit(ctx, q, actor, "invite.resolve", "invite:"+inv.ID.String(), map[string]any{
				"tenant_id": inv.TenantID.String(), "user_id": u.ID.String(), "role": inv.Role, "added": inv.Added,
			}); err != nil {
				return err
			}
		}
		return audit(ctx, q, actor, "user.sign_in", "user:"+u.ID.String(), map[string]any{
			"github_id": gh.ID, "github_login": gh.Login, "is_operator": u.IsOperator,
		})
	})
	return u, joined, err
}

func audit(ctx context.Context, q *systemq.Queries, actor, action, target string, detail map[string]any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if _, err := q.InsertAuditLog(ctx, systemq.InsertAuditLogParams{
		Actor: actor, Action: action, Target: target, Detail: b,
	}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// logout ends the session (contract 4.1.3).
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	uid, signedIn := h.sessions.UserID(ctx)
	err := h.sessions.SignOut(ctx, func(tx pgx.Tx) error {
		if !signedIn {
			return nil
		}
		return audit(ctx, systemq.New(tx), "user:"+uid.String(), "user.sign_out",
			"user:"+uid.String(), map[string]any{})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// MeUser is the User object of contract 3.1.
type MeUser struct {
	ID          uuid.UUID `json:"id"`
	GitHubID    int64     `json:"github_id"`
	GitHubLogin string    `json:"github_login"`
	IsOperator  bool      `json:"is_operator"`
}

// MeTenant is one entry of /api/me tenants.
type MeTenant struct {
	ID                   uuid.UUID `json:"id"`
	Slug                 string    `json:"slug"`
	Name                 string    `json:"name"`
	Status               string    `json:"status"`
	Role                 string    `json:"role"`
	AcceptedTermsVersion *string   `json:"accepted_terms_version"`
	TermsCurrent         bool      `json:"terms_current"`
}

// MeTerms is the terms summary of /api/me.
type MeTerms struct {
	CurrentVersion string `json:"current_version"`
	Status         string `json:"status"`
	URL            string `json:"url"`
}

// Me is the answer of GET /api/me.
type Me struct {
	User    MeUser     `json:"user"`
	Tenants []MeTenant `json:"tenants"`
	Terms   MeTerms    `json:"terms"`
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	t, err := terms.Current()
	if err != nil {
		return err
	}
	var rows []systemq.ListUserTenantsRow
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = systemq.New(tx).ListUserTenants(ctx, u.ID)
		return qerr
	})
	if err != nil {
		return err
	}
	out := Me{
		User:    MeUser{ID: u.ID, GitHubID: u.GitHubID, GitHubLogin: u.GitHubLogin, IsOperator: u.IsOperator},
		Tenants: make([]MeTenant, 0, len(rows)),
		Terms:   MeTerms{CurrentVersion: t.Version, Status: t.Status, URL: terms.URL},
	}
	for _, row := range rows {
		out.Tenants = append(out.Tenants, MeTenant{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Status: row.Status, Role: row.Role,
			AcceptedTermsVersion: row.AcceptedTermsVersion,
			TermsCurrent:         row.AcceptedTermsVersion != nil && *row.AcceptedTermsVersion == t.Version,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
