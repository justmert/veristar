// Package auth is console sign-in (contract 4.1): the github oauth web flow
// with state and pkce s256, wallet sign-in with a sep-10 challenge
// (stellar.go), soropass passkey sign-in with a webauthn assertion
// (passkey.go), linking and unlinking the three (link.go), sign-out, and
// /api/me.
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

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/terms"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
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
	failRateLimited       = "rate_limited"
)

// the /user api version is pinned so a new github default cannot change
// the answer; github keeps a version for at least 24 months after its
// successor ships
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
	// ConsoleOrigin is CONSOLE_ORIGIN. its host is the web_auth_domain and
	// home domain of wallet challenges, which binds a signed challenge to
	// this console.
	ConsoleOrigin string
	Endpoints     Endpoints
	// HTTPClient talks to github. nil uses a client with githubTimeout.
	HTTPClient *http.Client
	// Reconcile starts the provisioning reconciler for a tenant without
	// waiting for it (tenants.Scope.Reconcile). sign-in calls it for every
	// tenant an invite made the user a member of, so the umami membership
	// exists by the time the person opens a dashboard.
	Reconcile func(ctx context.Context, tenantID uuid.UUID)
	// StepUp reports whether the session holds a fresh operator step-up
	// (stepup.Service). an operator account changes its sign-in methods
	// only with one; nil refuses every such change.
	StepUp StepUpChecker
}

// StepUpChecker is what linking needs of the operator step-up.
type StepUpChecker interface {
	Fresh(ctx context.Context, userID uuid.UUID) (bool, time.Time)
}

// Handler serves the auth routes.
type Handler struct {
	oauth     *oauth2.Config
	apiBase   string
	sealer    *sealer
	client    *http.Client
	db        *db.DB
	sessions  *session.Manager
	reconcile func(ctx context.Context, tenantID uuid.UUID)
	clients   *clientHasher
	starts    *limiter
	exchanges *limiter
	used      usedStates
	now       func() time.Time
	stellar   *stellarAuth
	passkey   *passkeyAuth
	stepUp    StepUpChecker
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
	clients, err := newClientHasher()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sa, err := newStellarAuth(cfg.StateKey, cfg.ConsoleOrigin, now)
	if err != nil {
		return nil, err
	}
	pa, err := newPasskeyAuth(cfg.ConsoleOrigin, now)
	if err != nil {
		return nil, err
	}
	return &Handler{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret.Reveal(),
			Endpoint:     cfg.Endpoints.OAuth,
			RedirectURL:  cfg.RedirectURL,
			// no scopes: the public /user answer has id and login, which is
			// all the platform stores
		},
		apiBase:   strings.TrimSuffix(cfg.Endpoints.APIBase, "/"),
		sealer:    sl,
		client:    client,
		db:        d,
		sessions:  s,
		reconcile: cfg.Reconcile,
		clients:   clients,
		starts: newLimiter(StartClientRatePerSecond, StartClientBurst, StartRatePerSecond, StartBurst,
			StartReserveRatePerSecond, StartReserveBurst, now),
		exchanges: newLimiter(ExchangeClientRatePerSecond, ExchangeClientBurst, ExchangeRatePerSecond, ExchangeBurst,
			ExchangeReserveRatePerSecond, ExchangeReserveBurst, now),
		now:     time.Now,
		stellar: sa,
		passkey: pa,
		stepUp:  cfg.StepUp,
	}, nil
}

// Public registers the routes that need no session.
func (h *Handler) Public(r chi.Router) {
	r.Method(http.MethodGet, "/auth/github/start", httpx.HandlerFunc(h.start))
	r.Method(http.MethodGet, "/auth/github/callback", http.HandlerFunc(h.callback))
	// logout without a session is a 204 too (contract 4.1.3), so it is not
	// behind RequireUser; the csrf rules still apply to it
	r.Method(http.MethodPost, "/auth/logout", httpx.HandlerFunc(h.logout))
	r.Method(http.MethodPost, "/auth/stellar/challenge", httpx.HandlerFunc(h.stellarChallenge))
	r.Method(http.MethodPost, "/auth/stellar/verify", httpx.HandlerFunc(h.stellarVerify))
	r.Method(http.MethodPost, "/auth/passkey/challenge", httpx.HandlerFunc(h.passkeyChallenge))
	r.Method(http.MethodPost, "/auth/passkey/verify", httpx.HandlerFunc(h.passkeyVerify))
}

// Private registers the routes behind RequireUser.
func (h *Handler) Private(r chi.Router) {
	r.Method(http.MethodGet, "/me", httpx.HandlerFunc(h.me))
	r.Method(http.MethodPost, "/me/identities/stellar/challenge", httpx.HandlerFunc(h.stellarLinkChallenge))
	r.Method(http.MethodPost, "/me/identities/stellar", httpx.HandlerFunc(h.stellarLink))
	r.Method(http.MethodDelete, "/me/identities/stellar", httpx.HandlerFunc(h.unlinkStellar))
	r.Method(http.MethodDelete, "/me/identities/github", httpx.HandlerFunc(h.unlinkGitHub))
	r.Method(http.MethodPost, "/me/identities/passkey/challenge", httpx.HandlerFunc(h.passkeyLinkChallenge))
	r.Method(http.MethodPost, "/me/identities/passkey", httpx.HandlerFunc(h.passkeyLink))
	r.Method(http.MethodDelete, "/me/identities/passkey", httpx.HandlerFunc(h.unlinkPasskey))
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
	// every start sets a cookie and sends a browser to github; the buckets
	// keep one client's flood from locking others out (per client) and a
	// distributed flood from reaching github's limits for our client id
	// (global ceiling)
	if ok, wait := h.starts.take(h.clients.of(r), h.now()); !ok {
		return httpx.RateLimited(wait)
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	p := statePayload{
		State:    state,
		Verifier: oauth2.GenerateVerifier(),
		ReturnTo: cleanReturnTo(r.URL.Query().Get("return_to")),
		Exp:      h.now().Add(stateCookieTTL).Unix(),
	}
	// link=1 links github to the signed-in account (contract 4.1.8): the
	// proof of the account is the session, the proof of the github account
	// is this flow, both in one browser session
	if r.URL.Query().Get("link") == "1" {
		uid, ok := h.sessions.UserID(r.Context())
		if !ok {
			return httpx.Unauthenticated()
		}
		// the route needs no session, so the account is read here; the
		// callback checks the step-up again when it links
		if err := h.identityChangeAllowed(r.Context(), uid); err != nil {
			if he, ok := httpx.AsError(err); ok {
				failLink(w, r, he.Code)
				return nil
			}
			return err
		}
		p.LinkUser = uid.String()
		p.ReturnTo = accountPath
	}
	verifier := p.Verifier
	sealed, err := h.sealer.seal(p)
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

	// a copy of a sealed state cookie could be replayed with made-up codes
	// until it expires, each try costing a request to github: a state
	// reaches the exchange once, and the buckets bound the rest per client
	// and in total
	now := h.now()
	fresh, full := h.used.use(p.State, time.Unix(p.Exp, 0), now)
	if full {
		log.Warn("sign-in refused", "reason", failRateLimited, "detail", "used state set is full")
		fail(w, r, failRateLimited)
		return
	}
	if !fresh {
		log.Info("sign-in refused", "reason", failStateMismatch, "detail", "state already used")
		fail(w, r, failStateMismatch)
		return
	}
	if ok, _ := h.exchanges.take(h.clients.of(r), now); !ok {
		log.Info("sign-in refused", "reason", failRateLimited)
		fail(w, r, failRateLimited)
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

	if p.LinkUser != "" {
		h.linkGitHubCallback(w, r, p, gh)
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

// signIn upserts the user, resolves open invites for the account into
// memberships and writes the audit rows, in one cp_system transaction. it
// returns the tenants the user joined.
func (h *Handler) signIn(ctx context.Context, gh githubUser) (systemq.CpUser, []uuid.UUID, error) {
	var u systemq.CpUser
	var joined []uuid.UUID
	err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var err error
		u, err = q.SignInUser(ctx, systemq.SignInUserParams{GithubID: &gh.ID, GithubLogin: &gh.Login})
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		u, joined, err = h.afterSignIn(ctx, q, u, map[string]any{
			"method": "github", "github_id": gh.ID, "github_login": gh.Login,
		})
		return err
	})
	return u, joined, err
}

// afterSignIn is the part of a sign-in both methods share, inside the
// sign-in transaction: the operator flag from both identities of the row,
// the open invites of either identity turned into memberships, and the
// audit rows.
func (h *Handler) afterSignIn(ctx context.Context, q *systemq.Queries, u systemq.CpUser, detail map[string]any) (systemq.CpUser, []uuid.UUID, error) {
	u, err := q.SetOperatorFlag(ctx, u.ID)
	if err != nil {
		return u, nil, fmt.Errorf("operator flag: %w", err)
	}
	actor := "user:" + u.ID.String()
	resolved, err := q.ResolveInvites(ctx, systemq.ResolveInvitesParams{
		GithubID: u.GithubID, StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress, UserID: u.ID,
	})
	if err != nil {
		return u, nil, fmt.Errorf("resolve invites: %w", err)
	}
	var joined []uuid.UUID
	for _, inv := range resolved {
		if inv.Added {
			joined = append(joined, inv.TenantID)
		}
		if err := audit(ctx, q, actor, "invite.resolve", "invite:"+inv.ID.String(), map[string]any{
			"tenant_id": inv.TenantID.String(), "user_id": u.ID.String(), "role": inv.Role, "added": inv.Added,
		}); err != nil {
			return u, nil, err
		}
	}
	detail["is_operator"] = u.IsOperator
	return u, joined, audit(ctx, q, actor, "user.sign_in", "user:"+u.ID.String(), detail)
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

// MeUser is the User object of contract 3.1. each identity is null when the
// account does not hold it: github_id and github_login, stellar_address (a
// wallet's G-address) and passkey_address (a soropass account's C-address).
type MeUser struct {
	ID             uuid.UUID `json:"id"`
	GitHubID       *int64    `json:"github_id"`
	GitHubLogin    *string   `json:"github_login"`
	StellarAddress *string   `json:"stellar_address"`
	PasskeyAddress *string   `json:"passkey_address"`
	DisplayName    string    `json:"display_name"`
	IsOperator     bool      `json:"is_operator"`
}

// MeUserOf is the MeUser of a session user.
func MeUserOf(u session.User) MeUser {
	return MeUser{
		ID: u.ID, GitHubID: u.GitHubID, GitHubLogin: u.GitHubLogin, StellarAddress: u.StellarAddress,
		PasskeyAddress: u.PasskeyAddress,
		DisplayName:    u.DisplayName(), IsOperator: u.IsOperator,
	}
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

// MeRemovedTenant is one entry of /api/me removed_tenants (contract 5.10).
type MeRemovedTenant struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	StatusReason    *string         `json:"status_reason"`
	StatusChangedAt httpx.Timestamp `json:"status_changed_at"`
}

// Me is the answer of GET /api/me.
type Me struct {
	User           MeUser            `json:"user"`
	Tenants        []MeTenant        `json:"tenants"`
	RemovedTenants []MeRemovedTenant `json:"removed_tenants"`
	Terms          MeTerms           `json:"terms"`
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
	var removed []systemq.ListUserRemovedTenantsRow
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var qerr error
		if rows, qerr = q.ListUserTenants(ctx, u.ID); qerr != nil {
			return qerr
		}
		removed, qerr = q.ListUserRemovedTenants(ctx, u.ID)
		return qerr
	})
	if err != nil {
		return err
	}
	out := Me{
		User:           MeUserOf(u),
		Tenants:        make([]MeTenant, 0, len(rows)),
		RemovedTenants: make([]MeRemovedTenant, 0, len(removed)),
		Terms:          MeTerms{CurrentVersion: t.Version, Status: t.Status, URL: terms.URL},
	}
	for _, row := range rows {
		out.Tenants = append(out.Tenants, MeTenant{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Status: row.Status, Role: row.Role,
			AcceptedTermsVersion: row.AcceptedTermsVersion,
			TermsCurrent:         row.AcceptedTermsVersion != nil && *row.AcceptedTermsVersion == t.Version,
		})
	}
	for _, rt := range removed {
		out.RemovedTenants = append(out.RemovedTenants, MeRemovedTenant{
			ID: rt.ID, Name: rt.Name, StatusReason: rt.StatusReason, StatusChangedAt: httpx.Timestamp(rt.StatusChangedAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
