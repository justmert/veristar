package mcpserver

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// the oauth 2.1 authorization server of the mcp authorization
// specification (contract 12.3): protected resource metadata (rfc 9728),
// authorization server metadata (rfc 8414), dynamic client registration
// (rfc 7591) for public clients, the authorization code grant with pkce
// s256 only, rotating refresh tokens, resource indicators (rfc 8707) and
// revocation (rfc 7009). the person signs in with the console's github
// sign-in and approves on the console page ConsentPath.

// paths served beside /api
const (
	ProtectedResourceMetadataPath   = "/.well-known/oauth-protected-resource"
	AuthorizationServerMetadataPath = "/.well-known/oauth-authorization-server"
	AuthorizePath                   = "/oauth/authorize"
	TokenPath                       = "/oauth/token" //nolint:gosec // a path, not a credential
	RegisterPath                    = "/oauth/register"
	RevokePath                      = "/oauth/revoke"
	// ConsentPath is the console page that shows the consent screen
	ConsentPath = "/ai/consent"
)

// lifetimes of contract 12.3
const (
	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	codeTTL         = 60 * time.Second
	// requestTTL is how long the consent page may take
	requestTTL = 10 * time.Minute
)

// registration limit per control plane instance: registration needs no
// credential, so it is bounded globally; stale clients are deleted
const (
	registerRate  = 10.0 / 60
	registerBurst = 30
	// a registered client without any grant is deleted after a day
	staleClientAge = 24 * time.Hour
)

const maxOAuthBody = 16 << 10

// sealKey derives the key that seals authorization requests between the
// authorize endpoint and the consent page from OAUTH_STATE_KEY, with a
// purpose label of its own, so it never equals the sign-in state key.
func sealKey(stateKey []byte) ([]byte, error) {
	if len(stateKey) != 32 {
		return nil, errors.New("mcpserver: the oauth state key must be 32 bytes")
	}
	return hkdf.Key(sha256.New, stateKey, nil, "veristar mcp authorization request v1", 32)
}

// authRequest is a checked authorization request, sealed into the consent
// page url so the server keeps no state for a visitor who never approves.
type authRequest struct {
	ClientID      string `json:"c"`
	RedirectURI   string `json:"r"`
	State         string `json:"s"`
	CodeChallenge string `json:"p"`
	Resource      string `json:"o"`
	Expires       int64  `json:"e"`
}

func (s *Server) seal(r authRequest) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.sealKey)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, b, []byte("mcp-authorize"))), nil
}

func (s *Server) unseal(v string) (authRequest, error) {
	bad := httpx.Validation("the authorization request is invalid or expired; start again from the AI client",
		map[string]string{"request": "invalid_format"})
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(v) > 4096 {
		return authRequest{}, bad
	}
	block, err := aes.NewCipher(s.sealKey)
	if err != nil {
		return authRequest{}, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return authRequest{}, err
	}
	if len(raw) < g.NonceSize() {
		return authRequest{}, bad
	}
	b, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("mcp-authorize"))
	if err != nil {
		return authRequest{}, bad
	}
	var r authRequest
	if err := json.Unmarshal(b, &r); err != nil || s.now().Unix() > r.Expires {
		return authRequest{}, bad
	}
	return r, nil
}

// MetadataHandler serves the two discovery documents, mounted at
// /.well-known.
func (s *Server) MetadataHandler() http.Handler {
	prm := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               s.resource,
		AuthorizationServers:   []string{s.issuer},
		ScopesSupported:        []string{Scope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Veristar analytics",
	})
	r := chi.NewRouter()
	r.Use(httpx.Base(s.log))
	r.NotFound(httpx.NotFoundHandler)
	r.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	// rfc 9728 section 3.1: the document of https://app/mcp lives at the
	// well-known path followed by the resource path; the bare path serves
	// clients that look there first
	r.Method(http.MethodGet, strings.TrimPrefix(ProtectedResourceMetadataPath, "/.well-known")+Path, prm)
	r.Method(http.MethodGet, strings.TrimPrefix(ProtectedResourceMetadataPath, "/.well-known"), prm)
	r.Get(strings.TrimPrefix(AuthorizationServerMetadataPath, "/.well-known"), s.serverMetadata)
	return r
}

// OAuthHandler serves the oauth endpoints, mounted at /oauth.
func (s *Server) OAuthHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.Base(s.log))
	r.NotFound(httpx.NotFoundHandler)
	r.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	r.Get(strings.TrimPrefix(AuthorizePath, "/oauth"), s.authorize)
	r.Post(strings.TrimPrefix(TokenPath, "/oauth"), s.token)
	r.Post(strings.TrimPrefix(RegisterPath, "/oauth"), s.registerClient)
	r.Post(strings.TrimPrefix(RevokePath, "/oauth"), s.revoke)
	return r
}

func (s *Server) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	// public metadata for client discovery, readable cross-origin like the
	// protected resource document (rfc 8414 section 3)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.issuer,
		"authorization_endpoint":                         s.issuer + AuthorizePath,
		"token_endpoint":                                 s.issuer + TokenPath,
		"registration_endpoint":                          s.issuer + RegisterPath,
		"revocation_endpoint":                            s.issuer + RevokePath,
		"scopes_supported":                               []string{Scope},
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          false,
		"service_documentation":                          s.issuer + "/docs/guides/mcp",
	})
}

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError is the error answer of rfc 6749 section 5.2.
func (s *Server) oauthError(w http.ResponseWriter, endpoint string, status int, code, description string) {
	s.metrics.oauth.WithLabelValues(endpoint, code).Inc()
	writeOAuthJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// client is a registered client.
type client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

func (s *Server) client(ctx context.Context, id string) (client, bool, error) {
	if !validClientID(id) {
		return client{}, false, nil
	}
	var c client
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select id, client_name, redirect_uris from cp.mcp_oauth_clients where id = $1`, id).
			Scan(&c.ID, &c.Name, &c.RedirectURIs)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return client{}, false, nil
	}
	if err != nil {
		return client{}, false, fmt.Errorf("mcpserver: client: %w", err)
	}
	return c, true, nil
}

var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// consentError sends the browser to the console page with an error it
// shows, for a request that cannot be answered at its redirect uri.
func (s *Server) consentError(w http.ResponseWriter, r *http.Request, code, description string) {
	s.metrics.oauth.WithLabelValues("authorize", code).Inc()
	q := url.Values{"error": {code}, "error_description": {description}}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, ConsentPath+"?"+q.Encode(), http.StatusSeeOther)
}

// redirectError answers a checked client at its redirect uri (rfc 6749
// section 4.1.2.1), with iss (rfc 9207).
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	s.metrics.oauth.WithLabelValues("authorize", code).Inc()
	w.Header().Set("Referrer-Policy", "no-referrer")
	//nolint:gosec // redirectURI is exactly one the registered client gave at registration
	http.Redirect(w, r, s.callbackURL(redirectURI, url.Values{"error": {code}, "error_description": {description}}, state), http.StatusSeeOther)
}

func (s *Server) callbackURL(redirectURI string, q url.Values, state string) string {
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", s.issuer)
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	return redirectURI + sep + q.Encode()
}

// authorize checks the request and sends the browser to the consent page.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.consentError(w, r, "invalid_request", "the authorization request is malformed")
		return
	}
	for k, v := range q {
		if len(v) != 1 {
			s.consentError(w, r, "invalid_request", "parameter "+k+" is repeated")
			return
		}
	}
	c, ok, err := s.client(ctx, q.Get("client_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !ok {
		s.consentError(w, r, "invalid_client", "the AI client is not registered here; remove the connector and add it again")
		return
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	registered := false
	for _, u := range c.RedirectURIs {
		registered = registered || u == redirect
	}
	if !registered {
		s.consentError(w, r, "invalid_request", "the redirect uri is not one the AI client registered")
		return
	}
	state := q.Get("state")
	if len(state) > 1024 {
		s.redirectError(w, r, redirect, "", "invalid_request", "state is longer than 1024 characters")
		return
	}
	switch {
	case q.Get("response_type") != "code":
		s.redirectError(w, r, redirect, state, "unsupported_response_type", "response_type must be code")
		return
	case q.Get("code_challenge_method") != "S256":
		s.redirectError(w, r, redirect, state, "invalid_request", "pkce with code_challenge_method S256 is required")
		return
	case !challengeRe.MatchString(q.Get("code_challenge")):
		s.redirectError(w, r, redirect, state, "invalid_request", "code_challenge must be a base64url sha-256 of 43 characters")
		return
	case q.Get("resource") != "" && q.Get("resource") != s.resource:
		s.redirectError(w, r, redirect, state, "invalid_target", "resource must be "+s.resource)
		return
	}
	if sc := q.Get("scope"); sc != "" {
		for _, v := range strings.Fields(sc) {
			// offline_access asks for a refresh token, which every grant gets
			if v != Scope && v != "offline_access" {
				s.redirectError(w, r, redirect, state, "invalid_scope", "the only scope is "+Scope)
				return
			}
		}
	}
	sealed, err := s.seal(authRequest{
		ClientID: c.ID, RedirectURI: redirect, State: state, CodeChallenge: q.Get("code_challenge"),
		Resource: s.resource, Expires: s.now().Add(requestTTL).Unix(),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s.metrics.oauth.WithLabelValues("authorize", "consent").Inc()
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, ConsentPath+"?"+url.Values{"request": {sealed}}.Encode(), http.StatusSeeOther)
}

// readForm reads an application/x-www-form-urlencoded body of at most
// maxOAuthBody bytes; a parameter given twice is refused (rfc 6749 3.1).
func readForm(r *http.Request) (url.Values, error) {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if !strings.EqualFold(strings.TrimSpace(ct), "application/x-www-form-urlencoded") {
		return nil, errors.New("the body must be application/x-www-form-urlencoded")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxOAuthBody+1))
	if err != nil || len(b) > maxOAuthBody {
		return nil, errors.New("the body is too large")
	}
	f, err := url.ParseQuery(string(b))
	if err != nil {
		return nil, errors.New("the body is malformed")
	}
	for k, v := range f {
		if len(v) != 1 {
			return nil, errors.New("parameter " + k + " is repeated")
		}
	}
	return f, nil
}

var verifierRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// token serves the authorization_code and refresh_token grants.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := readForm(r)
	if err != nil {
		s.oauthError(w, "token", http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if res := f.Get("resource"); res != "" && res != s.resource {
		s.oauthError(w, "token", http.StatusBadRequest, "invalid_target", "resource must be "+s.resource)
		return
	}
	c, ok, err := s.client(ctx, f.Get("client_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !ok {
		s.oauthError(w, "token", http.StatusUnauthorized, "invalid_client", "unknown client_id")
		return
	}
	var out tokenResponse
	switch f.Get("grant_type") {
	case "authorization_code":
		out, err = s.exchangeCode(ctx, c, f)
	case "refresh_token":
		out, err = s.refresh(ctx, c, f)
	default:
		s.oauthError(w, "token", http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	var ge *grantError
	if errors.As(err, &ge) {
		s.oauthError(w, "token", http.StatusBadRequest, ge.code, ge.description)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s.metrics.oauth.WithLabelValues("token", "issued").Inc()
	writeOAuthJSON(w, http.StatusOK, out)
}

type grantError struct{ code, description string }

func (e *grantError) Error() string { return e.code + ": " + e.description }

func invalidGrant(d string) error { return &grantError{code: "invalid_grant", description: d} }

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// issue writes a new access and refresh token of a grant.
func (s *Server) issue(ctx context.Context, tx pgx.Tx, credential uuid.UUID, now time.Time) (tokenResponse, error) {
	access, ah, err := newSecret(AccessTokenPrefix)
	if err != nil {
		return tokenResponse{}, err
	}
	refresh, rh, err := newSecret(RefreshTokenPrefix)
	if err != nil {
		return tokenResponse{}, err
	}
	if _, err := tx.Exec(ctx, `insert into cp.mcp_oauth_tokens (hash, credential_id, kind, created_at, expires_at)
		values ($1, $3, 'access', $4, $5), ($2, $3, 'refresh', $4, $6)`,
		ah[:], rh[:], credential, now, now.Add(AccessTokenTTL), now.Add(RefreshTokenTTL)); err != nil {
		return tokenResponse{}, fmt.Errorf("mcpserver: tokens: %w", err)
	}
	// expired tokens of every grant are dropped as new ones are made, so the
	// table holds live tokens and used refresh tokens until they expire
	if _, err := tx.Exec(ctx, `delete from cp.mcp_oauth_tokens where expires_at < $1`, now); err != nil {
		return tokenResponse{}, fmt.Errorf("mcpserver: expired tokens: %w", err)
	}
	return tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL / time.Second),
		RefreshToken: refresh, Scope: Scope}, nil
}

// exchangeCode redeems an authorization code once.
func (s *Server) exchangeCode(ctx context.Context, c client, f url.Values) (tokenResponse, error) {
	hash, ok := parseSecret(f.Get("code"), codePrefix)
	if !ok {
		return tokenResponse{}, invalidGrant("the code is invalid")
	}
	verifier := f.Get("code_verifier")
	if !verifierRe.MatchString(verifier) {
		return tokenResponse{}, invalidGrant("code_verifier must be 43 to 128 unreserved characters")
	}
	now := s.now()
	var out tokenResponse
	// refusal is an answer whose revocation must be committed first
	var refusal error
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var code struct {
			Client, Redirect, Challenge, Resource, Scope string
			User                                         uuid.UUID
			Tenant                                       uuid.NullUUID
			Expires                                      time.Time
			Used                                         *time.Time
			Credential                                   uuid.NullUUID
		}
		err := tx.QueryRow(ctx, `select client_id, redirect_uri, code_challenge, resource, scope, user_id, tenant_id,
				expires_at, used_at, credential_id
			from cp.mcp_oauth_codes where hash = $1 for update`, hash[:]).
			Scan(&code.Client, &code.Redirect, &code.Challenge, &code.Resource, &code.Scope, &code.User, &code.Tenant,
				&code.Expires, &code.Used, &code.Credential)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidGrant("the code is invalid")
		}
		if err != nil {
			return err
		}
		if code.Used != nil {
			// a second use of a code: revoke what the first one issued
			// (rfc 6749 section 4.1.2)
			if code.Credential.Valid {
				if err := revokeCredential(ctx, tx, code.Credential.UUID, "code_reuse", "system:mcp-oauth"); err != nil {
					return err
				}
			}
			refusal = invalidGrant("the code was already used")
			return nil
		}
		sum := sha256.Sum256([]byte(verifier))
		switch {
		case !now.Before(code.Expires):
			return invalidGrant("the code expired")
		case code.Client != c.ID:
			return invalidGrant("the code was issued to another client")
		case f.Get("redirect_uri") != code.Redirect:
			return invalidGrant("redirect_uri does not match the authorization request")
		case code.Resource != s.resource:
			return invalidGrant("the code was issued for another resource")
		case subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(code.Challenge)) != 1:
			return invalidGrant("code_verifier does not match the code challenge")
		}
		if err := s.stillAllowed(ctx, tx, code.User, code.Scope, code.Tenant); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.mcp_credentials (kind, scope, tenant_id, user_id, name, client_id, resource)
			values ('oauth', $1, $2, $3, $4, $5, $6) returning id`,
			code.Scope, code.Tenant, code.User, c.Name, c.ID, s.resource).Scan(&id); err != nil {
			return fmt.Errorf("mcpserver: grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `update cp.mcp_oauth_codes set used_at = $2, credential_id = $3 where hash = $1`,
			hash[:], now, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update cp.mcp_oauth_clients set last_used_at = $2 where id = $1`, c.ID, now); err != nil {
			return err
		}
		var tenant any
		target := ""
		if code.Tenant.Valid {
			tenant, target = code.Tenant.UUID.String(), "tenant:"+code.Tenant.UUID.String()
		}
		if err := insertAudit(ctx, tx, "user:"+code.User.String(), "mcp_credential.create", "mcp_credential:"+id.String(),
			map[string]any{"kind": KindOAuth, "scope": code.Scope, "tenant_id": tenant, "client_id": c.ID,
				"name": c.Name, "target": target}); err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, id, now)
		return err
	})
	if err == nil && refusal != nil {
		return tokenResponse{}, refusal
	}
	return out, err
}

// stillAllowed checks at token time what authenticate checks per request,
// so a grant is never issued or refreshed for a user who lost access.
func (s *Server) stillAllowed(ctx context.Context, tx pgx.Tx, user uuid.UUID, scope string, tenant uuid.NullUUID) error {
	var gh *int64
	var address *string
	var isOp bool
	var role, status *string
	err := tx.QueryRow(ctx, `select u.github_id, u.stellar_address, u.is_operator, m.role, t.status
		from cp.users u
		left join cp.tenant_members m on m.user_id = u.id and m.tenant_id = $2
		left join cp.tenants t on t.id = $2
		where u.id = $1 and u.deleted_at is null`, user, tenant).Scan(&gh, &address, &isOp, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalidGrant("the user no longer exists")
	}
	if err != nil {
		return err
	}
	switch scope {
	case ScopeTenant:
		if role == nil || status == nil || !usableTenantStatus[*status] {
			return invalidGrant("the user no longer has access to the project")
		}
	case ScopeOperator:
		if !s.guard.Allowed(session.User{ID: user, GitHubID: gh, StellarAddress: address, IsOperator: isOp}) {
			return invalidGrant("the user is no longer an operator")
		}
	default:
		return fmt.Errorf("mcpserver: scope %q", scope)
	}
	return nil
}

// refresh rotates a refresh token. a used refresh token presented again
// means it leaked or was replayed, so the whole grant is revoked (oauth 2.1
// section 4.3.1, rfc 9700 section 4.14.2).
func (s *Server) refresh(ctx context.Context, c client, f url.Values) (tokenResponse, error) {
	hash, ok := parseSecret(f.Get("refresh_token"), RefreshTokenPrefix)
	if !ok {
		return tokenResponse{}, invalidGrant("the refresh token is invalid")
	}
	now := s.now()
	var out tokenResponse
	var refusal error
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var cred uuid.UUID
		var expires time.Time
		var used, revoked *time.Time
		var clientID *string
		var scope, resource string
		var user uuid.UUID
		var tenant uuid.NullUUID
		err := tx.QueryRow(ctx, `select tk.credential_id, tk.expires_at, tk.used_at, c.revoked_at, c.client_id,
				c.scope, c.resource, c.user_id, c.tenant_id
			from cp.mcp_oauth_tokens tk join cp.mcp_credentials c on c.id = tk.credential_id
			where tk.hash = $1 and tk.kind = 'refresh' for update of tk, c`, hash[:]).
			Scan(&cred, &expires, &used, &revoked, &clientID, &scope, &resource, &user, &tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidGrant("the refresh token is invalid")
		}
		if err != nil {
			return err
		}
		switch {
		case clientID == nil || *clientID != c.ID:
			return invalidGrant("the refresh token was issued to another client")
		case revoked != nil:
			return invalidGrant("the grant was revoked")
		case used != nil:
			if err := revokeCredential(ctx, tx, cred, "refresh_reuse", "system:mcp-oauth"); err != nil {
				return err
			}
			refusal = invalidGrant("the refresh token was already used; the grant is revoked, sign in again")
			return nil
		case !now.Before(expires):
			return invalidGrant("the refresh token expired")
		case resource != s.resource:
			return invalidGrant("the grant was issued for another resource")
		}
		if err := s.stillAllowed(ctx, tx, user, scope, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update cp.mcp_oauth_tokens set used_at = $2 where hash = $1`, hash[:], now); err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, cred, now)
		return err
	})
	if err == nil && refusal != nil {
		return tokenResponse{}, refusal
	}
	return out, err
}

// revokeCredential revokes one active credential and writes its audit row.
func revokeCredential(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason, actor string) error {
	var kind, scope string
	var tenant uuid.NullUUID
	var user uuid.UUID
	err := tx.QueryRow(ctx, `update cp.mcp_credentials set revoked_at = now(), revoked_reason = $2
		where id = $1 and revoked_at is null returning kind, scope, tenant_id, user_id`, id, reason).
		Scan(&kind, &scope, &tenant, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mcpserver: revoke: %w", err)
	}
	var t any
	if tenant.Valid {
		t = tenant.UUID.String()
	}
	return insertAudit(ctx, tx, actor, "mcp_credential.revoke", "mcp_credential:"+id.String(),
		map[string]any{"kind": kind, "scope": scope, "tenant_id": t, "user_id": user.String(), "reason": reason})
}

// revoke is rfc 7009: the grant of a refresh or access token of the
// presenting client is revoked. the answer is 200 whatever the token was.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := readForm(r)
	if err != nil {
		s.oauthError(w, "revoke", http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	c, ok, err := s.client(ctx, f.Get("client_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !ok {
		s.oauthError(w, "revoke", http.StatusUnauthorized, "invalid_client", "unknown client_id")
		return
	}
	tok := f.Get("token")
	hash, ok := parseSecret(tok, RefreshTokenPrefix)
	if !ok {
		hash, ok = parseSecret(tok, AccessTokenPrefix)
	}
	if ok {
		err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
			var cred uuid.UUID
			err := tx.QueryRow(ctx, `select tk.credential_id from cp.mcp_oauth_tokens tk
				join cp.mcp_credentials c on c.id = tk.credential_id
				where tk.hash = $1 and c.client_id = $2`, hash[:], c.ID).Scan(&cred)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return revokeCredential(ctx, tx, cred, "user", "system:mcp-oauth")
		})
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	s.metrics.oauth.WithLabelValues("revoke", "ok").Inc()
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// registration (rfc 7591)

type registration struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

// blocked schemes can run code or read local files in the browser that
// follows the redirect
var blockedSchemes = map[string]bool{
	"javascript": true, "data": true, "file": true, "vbscript": true, "about": true, "blob": true,
	"ws": true, "wss": true, "ftp": true, "filesystem": true,
}

var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

// checkRedirect accepts https uris, http on a loopback host (rfc 8252
// section 7.3) and private-use schemes of native apps (rfc 8252 section
// 7.1), always without a fragment.
func checkRedirect(v string) error {
	if len(v) > 2048 {
		return errors.New("a redirect uri is longer than 2048 characters")
	}
	u, err := url.Parse(v)
	if err != nil || !u.IsAbs() || u.Fragment != "" || strings.Contains(v, "#") {
		return errors.New("every redirect uri must be absolute and without a fragment")
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return errors.New("an https redirect uri needs a host")
		}
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
		default:
			return errors.New("http redirect uris are allowed on loopback hosts only")
		}
	default:
		if !schemeRe.MatchString(u.Scheme) || blockedSchemes[u.Scheme] {
			return errors.New("redirect uri scheme " + u.Scheme + " is not allowed")
		}
	}
	return nil
}

func (s *Server) registerClient(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.registerMu.Lock()
	ok, wait := s.register.take(s.now())
	s.registerMu.Unlock()
	if !ok {
		s.metrics.oauth.WithLabelValues("register", "rate_limited").Inc()
		w.Header().Set("Retry-After", fmt.Sprint(int(wait/time.Second)+1))
		writeOAuthJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited", "error_description": "too many registrations; try again shortly"})
		return
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if !strings.EqualFold(strings.TrimSpace(ct), "application/json") {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "the body must be application/json")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxOAuthBody+1))
	if err != nil || len(b) > maxOAuthBody {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "the body is too large")
		return
	}
	var reg registration
	// unknown metadata fields are ignored, as rfc 7591 section 2 asks
	if err := json.Unmarshal(b, &reg); err != nil {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "the body is not a client metadata object")
		return
	}
	if len(reg.RedirectURIs) < 1 || len(reg.RedirectURIs) > 10 {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_redirect_uri", "give 1 to 10 redirect_uris")
		return
	}
	for _, u := range reg.RedirectURIs {
		if err := checkRedirect(u); err != nil {
			s.oauthError(w, "register", http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}
	if m := reg.TokenEndpointAuthMethod; m != "" && m != "none" {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "only public clients (token_endpoint_auth_method none) with pkce are supported")
		return
	}
	for _, g := range reg.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "grant_types may hold authorization_code and refresh_token only")
			return
		}
	}
	for _, rt := range reg.ResponseTypes {
		if rt != "code" {
			s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "response_types may hold code only")
			return
		}
	}
	name := trimmed(reg.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if err := text("client_name", name, 400); err != nil || len([]rune(name)) > 100 {
		s.oauthError(w, "register", http.StatusBadRequest, "invalid_client_metadata", "client_name must be text of at most 100 characters")
		return
	}
	rnd, err := randomString(clientRandomLen)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := clientPrefix + rnd
	now := s.now()
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into cp.mcp_oauth_clients (id, client_name, redirect_uris, created_at)
			values ($1, $2, $3, $4)`, id, name, reg.RedirectURIs, now); err != nil {
			return err
		}
		// clients that never completed a grant are not kept: registration is
		// open, so the table must stay bounded
		_, err := tx.Exec(ctx, `delete from cp.mcp_oauth_clients c
			where c.created_at < $1 and not exists (select 1 from cp.mcp_credentials m where m.client_id = c.id)`,
			now.Add(-staleClientAge))
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, fmt.Errorf("mcpserver: register: %w", err))
		return
	}
	s.metrics.oauth.WithLabelValues("register", "registered").Inc()
	writeOAuthJSON(w, http.StatusCreated, map[string]any{
		"client_id": id, "client_id_issued_at": now.Unix(), "client_name": name,
		"redirect_uris": reg.RedirectURIs, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"scope": Scope,
	})
}
