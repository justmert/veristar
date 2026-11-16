package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

// the console routes of contract 12.8: access keys of a project, operator
// keys, and the consent screen of the oauth flow. all are session routes
// behind the console's csrf rules; none is reachable with an mcp credential.

// key lifetime and limits
const (
	defaultKeyDays = 90
	maxKeyDays     = 365
	// active access keys one user may hold in one project, or as operator
	maxActiveKeys = 20
)

// CodeTooManyKeys is the 409 of a key create over maxActiveKeys.
const CodeTooManyKeys = "too_many_keys"

// Credential is the console object of contract 12.8.
type Credential struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Scope       string     `json:"scope"`
	TenantID    *uuid.UUID `json:"tenant_id"`
	Name        string     `json:"name"`
	Prefix      *string    `json:"prefix"`
	ClientID    *string    `json:"client_id"`
	UserID      uuid.UUID  `json:"user_id"`
	GitHubLogin *string    `json:"github_login"`
	// DisplayName is the github login or the short stellar or passkey
	// address of the creating account; null once that account is deleted
	DisplayName   *string          `json:"display_name"`
	Status        string           `json:"status"`
	CreatedAt     httpx.Timestamp  `json:"created_at"`
	ExpiresAt     *httpx.Timestamp `json:"expires_at"`
	RevokedAt     *httpx.Timestamp `json:"revoked_at"`
	RevokedReason *string          `json:"revoked_reason"`
	LastSeenAt    *httpx.Timestamp `json:"last_seen_at"`
}

// CreatedKey is the create answer: the credential and the key, shown once.
type CreatedKey struct {
	Credential
	Key string `json:"key"`
}

//nolint:gosec // column names of a query, not a credential
const credentialSelect = `select c.id, c.kind, c.scope, c.tenant_id, c.name, c.prefix, c.client_id, c.user_id, u.github_login,
	u.stellar_address, u.passkey_address, c.created_at, c.expires_at, c.revoked_at, c.revoked_reason, c.last_seen_at
	from cp.mcp_credentials c left join cp.users u on u.id = c.user_id`

func scanCredentialRow(row pgx.Row, now time.Time) (Credential, error) {
	var c Credential
	var tenant uuid.NullUUID
	var created time.Time
	var expires, revoked, seen *time.Time
	var address, passkey *string
	if err := row.Scan(&c.ID, &c.Kind, &c.Scope, &tenant, &c.Name, &c.Prefix, &c.ClientID, &c.UserID, &c.GitHubLogin,
		&address, &passkey, &created, &expires, &revoked, &c.RevokedReason, &seen); err != nil {
		return Credential{}, err
	}
	if n := identity.DisplayName(c.GitHubLogin, address, passkey); n != "" {
		c.DisplayName = &n
	}
	if tenant.Valid {
		t := tenant.UUID
		c.TenantID = &t
	}
	c.CreatedAt = httpx.Timestamp(created)
	c.ExpiresAt, c.RevokedAt, c.LastSeenAt = httpx.TimestampPtr(expires), httpx.TimestampPtr(revoked), httpx.TimestampPtr(seen)
	switch {
	case revoked != nil:
		c.Status = "revoked"
	case expires != nil && !expires.After(now):
		c.Status = "expired"
	default:
		c.Status = "active"
	}
	return c, nil
}

type createBody struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

func (b createBody) check() (string, int, error) {
	name := strings.TrimSpace(b.Name)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "required"
	case utf8.RuneCountInString(name) > 100:
		fields["name"] = "too_long"
	default:
		for _, r := range name {
			if r < 0x20 || r == 0x7f {
				fields["name"] = "invalid_format"
			}
		}
	}
	days := defaultKeyDays
	if b.ExpiresInDays != nil {
		days = *b.ExpiresInDays
		if days < 1 || days > maxKeyDays {
			fields["expires_in_days"] = "invalid_value"
		}
	}
	if len(fields) > 0 {
		return "", 0, httpx.Validation("the key fields are invalid", fields)
	}
	return name, days, nil
}

// TenantRoutes are the project's key routes, under cp_tenant.
type TenantRoutes struct {
	s     *Server
	scope *tenants.Scope
}

// TenantRoutes returns the routes for the console's AI page of a project.
func (s *Server) TenantRoutes(scope *tenants.Scope) *TenantRoutes {
	return &TenantRoutes{s: s, scope: scope}
}

const tenantBase = "/tenants/{tenant_id}/mcp-credentials"

// Routes registers the routes under a router mounted at /api.
func (h *TenantRoutes) Routes(r chi.Router) {
	r.Method(http.MethodGet, tenantBase, httpx.HandlerFunc(h.list))
	r.Method(http.MethodPost, tenantBase, httpx.HandlerFunc(h.create))
	r.Method(http.MethodDelete, tenantBase+"/{credential_id}", httpx.HandlerFunc(h.revoke))
}

const tenantListRoute = "mcp-credentials"

func (h *TenantRoutes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	p, err := httpx.ParsePage(r, tenantListRoute)
	if err != nil {
		return err
	}
	now := h.s.now()
	out := httpx.List[Credential]{Items: []Credential{}}
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		// row-level security holds the list to the tenant; the filter is
		// written out as well
		args := []any{a.TenantID, p.Limit + 1}
		where := `where c.tenant_id = $1`
		if p.After != nil {
			where += ` and (c.created_at, c.id) < ($3, $4)`
			args = append(args, p.After.At, p.After.ID)
		}
		rows, err := tx.Query(ctx, credentialSelect+" "+where+` order by c.created_at desc, c.id desc limit $2`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCredentialRow(rows, now)
			if err != nil {
				return err
			}
			if len(out.Items) == p.Limit {
				last := out.Items[len(out.Items)-1]
				out.NextCursor = httpx.NextCursor(tenantListRoute, httpx.Cursor{At: time.Time(last.CreatedAt), ID: last.ID})
				break
			}
			out.Items = append(out.Items, c)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *TenantRoutes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	var body createBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	name, days, err := body.check()
	if err != nil {
		return err
	}
	plain, hash, err := newSecret(KeyPrefix)
	if err != nil {
		return err
	}
	now := h.s.now()
	var out CreatedKey
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		if err := tenants.LockTenant(ctx, tx, &a); err != nil {
			return err
		}
		switch a.TenantStatus {
		case "suspended":
			return httpx.Conflict(httpx.CodeTenantSuspended, "the project is suspended")
		case "rejected":
			return httpx.Conflict(httpx.CodeTenantNotActive, "the project was not approved")
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from cp.mcp_credentials
			where tenant_id = $1 and user_id = $2 and kind = 'key' and revoked_at is null and expires_at > $3`,
			a.TenantID, a.User.ID, now).Scan(&n); err != nil {
			return err
		}
		if n >= maxActiveKeys {
			return httpx.Conflict(CodeTooManyKeys, fmt.Sprintf("you hold %d active keys for this project; revoke one first", maxActiveKeys))
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.mcp_credentials
			(kind, scope, tenant_id, user_id, name, prefix, hash, resource, created_at, expires_at)
			values ('key', 'tenant', $1, $2, $3, $4, $5, $6, $7, $8) returning id`,
			a.TenantID, a.User.ID, name, plain[:keyDisplayLen], hash[:], h.s.resource, now,
			now.Add(time.Duration(days)*24*time.Hour)).Scan(&id); err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, "user:"+a.User.ID.String(), "mcp_credential.create", "mcp_credential:"+id.String(),
			map[string]any{"kind": KindKey, "scope": ScopeTenant, "tenant_id": a.TenantID.String(), "name": name,
				"prefix": plain[:keyDisplayLen], "expires_in_days": days}); err != nil {
			return err
		}
		c, err := scanCredentialRow(tx.QueryRow(ctx, credentialSelect+` where c.id = $1`, id), now)
		out = CreatedKey{Credential: c, Key: plain}
		return err
	})
	if err != nil {
		return err
	}
	httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/mcp-credentials/"+out.ID.String(), out)
	return nil
}

// revoke: a member revokes its own credentials, an owner any of the
// project's. revoking a revoked one is 204 and changes nothing.
func (h *TenantRoutes) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "credential_id"))
	if err != nil {
		return err
	}
	err = h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var user uuid.UUID
		var revoked *time.Time
		var kind string
		err := tx.QueryRow(ctx, `select user_id, revoked_at, kind from cp.mcp_credentials where id = $1 and tenant_id = $2 for update`,
			id, a.TenantID).Scan(&user, &revoked, &kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if user != a.User.ID {
			if err := a.RequireOwner(); err != nil {
				return err
			}
		}
		if revoked != nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `update cp.mcp_credentials set revoked_at = now(), revoked_reason = 'user' where id = $1`, id); err != nil {
			return err
		}
		return insertAudit(ctx, tx, "user:"+a.User.ID.String(), "mcp_credential.revoke", "mcp_credential:"+id.String(),
			map[string]any{"kind": kind, "scope": ScopeTenant, "tenant_id": a.TenantID.String(), "user_id": user.String(), "reason": "user"})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// OperatorRoutes are the operator-scope credential routes, mounted inside
// the operator area behind its guard; they run under cp_system like the
// operator api keys.
type OperatorRoutes struct{ s *Server }

// OperatorRoutes returns the operator routes.
func (s *Server) OperatorRoutes() *OperatorRoutes { return &OperatorRoutes{s: s} }

const operatorBase = "/mcp-credentials"

// Routes registers under the operator area (/api/operator).
func (h *OperatorRoutes) Routes(r chi.Router) {
	r.Method(http.MethodGet, operatorBase, httpx.HandlerFunc(h.list))
	r.Method(http.MethodPost, operatorBase, httpx.HandlerFunc(h.create))
	r.Method(http.MethodDelete, operatorBase+"/{credential_id}", httpx.HandlerFunc(h.revoke))
}

const operatorListRoute = "operator-mcp-credentials"

func (h *OperatorRoutes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := httpx.ParsePage(r, operatorListRoute)
	if err != nil {
		return err
	}
	now := h.s.now()
	out := httpx.List[Credential]{Items: []Credential{}}
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		args := []any{p.Limit + 1}
		where := `where c.scope = 'operator'`
		if p.After != nil {
			where += ` and (c.created_at, c.id) < ($2, $3)`
			args = append(args, p.After.At, p.After.ID)
		}
		rows, err := tx.Query(ctx, credentialSelect+" "+where+` order by c.created_at desc, c.id desc limit $1`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCredentialRow(rows, now)
			if err != nil {
				return err
			}
			if len(out.Items) == p.Limit {
				last := out.Items[len(out.Items)-1]
				out.NextCursor = httpx.NextCursor(operatorListRoute, httpx.Cursor{At: time.Time(last.CreatedAt), ID: last.ID})
				break
			}
			out.Items = append(out.Items, c)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *OperatorRoutes) create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var body createBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	name, days, err := body.check()
	if err != nil {
		return err
	}
	plain, hash, err := newSecret(KeyPrefix)
	if err != nil {
		return err
	}
	now := h.s.now()
	var out CreatedKey
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from cp.mcp_credentials
			where scope = 'operator' and user_id = $1 and kind = 'key' and revoked_at is null and expires_at > $2`,
			u.ID, now).Scan(&n); err != nil {
			return err
		}
		if n >= maxActiveKeys {
			return httpx.Conflict(CodeTooManyKeys, fmt.Sprintf("you hold %d active operator keys; revoke one first", maxActiveKeys))
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.mcp_credentials
			(kind, scope, user_id, name, prefix, hash, resource, created_at, expires_at)
			values ('key', 'operator', $1, $2, $3, $4, $5, $6, $7) returning id`,
			u.ID, name, plain[:keyDisplayLen], hash[:], h.s.resource, now, now.Add(time.Duration(days)*24*time.Hour)).Scan(&id); err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, "user:"+u.ID.String(), "mcp_credential.create", "mcp_credential:"+id.String(),
			map[string]any{"kind": KindKey, "scope": ScopeOperator, "tenant_id": nil, "name": name,
				"prefix": plain[:keyDisplayLen], "expires_in_days": days}); err != nil {
			return err
		}
		c, err := scanCredentialRow(tx.QueryRow(ctx, credentialSelect+` where c.id = $1`, id), now)
		out = CreatedKey{Credential: c, Key: plain}
		return err
	})
	if err != nil {
		return err
	}
	httpx.Created(w, "/api/operator/mcp-credentials/"+out.ID.String(), out)
	return nil
}

// revoke: any operator may revoke any operator-scope credential.
func (h *OperatorRoutes) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "credential_id"))
	if err != nil {
		return err
	}
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var scope string
		err := tx.QueryRow(ctx, `select scope from cp.mcp_credentials where id = $1`, id).Scan(&scope)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && scope != ScopeOperator) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		return revokeCredential(ctx, tx, id, "operator", "user:"+u.ID.String())
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// ConsentRoutes serve the console's consent page (ConsentPath).
type ConsentRoutes struct{ s *Server }

// ConsentRoutes returns the consent routes.
func (s *Server) ConsentRoutes() *ConsentRoutes { return &ConsentRoutes{s: s} }

const consentBase = "/mcp/authorize"

// Routes registers under /api.
func (h *ConsentRoutes) Routes(r chi.Router) {
	r.Method(http.MethodPost, consentBase+"/inspect", httpx.HandlerFunc(h.inspect))
	r.Method(http.MethodPost, consentBase+"/approve", httpx.HandlerFunc(h.approve))
	r.Method(http.MethodPost, consentBase+"/deny", httpx.HandlerFunc(h.deny))
}

type requestBody struct {
	Request string `json:"request"`
}

// ConsentTenant is a project the user may grant.
type ConsentTenant struct {
	ID     uuid.UUID `json:"id"`
	Slug   string    `json:"slug"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Role   string    `json:"role"`
}

// ConsentRequest is what the consent page shows.
type ConsentRequest struct {
	ClientName     string          `json:"client_name"`
	ClientID       string          `json:"client_id"`
	RedirectOrigin string          `json:"redirect_origin"`
	Scope          string          `json:"scope"`
	Resource       string          `json:"resource"`
	ExpiresAt      httpx.Timestamp `json:"expires_at"`
	Tenants        []ConsentTenant `json:"tenants"`
	Operator       bool            `json:"operator"`
}

func (h *ConsentRoutes) load(r *http.Request) (session.User, authRequest, client, error) {
	u, err := session.MustUser(r.Context())
	if err != nil {
		return session.User{}, authRequest{}, client{}, err
	}
	var body requestBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return session.User{}, authRequest{}, client{}, err
	}
	ar, err := h.s.unseal(body.Request)
	if err != nil {
		return session.User{}, authRequest{}, client{}, err
	}
	c, ok, err := h.s.client(r.Context(), ar.ClientID)
	if err != nil {
		return session.User{}, authRequest{}, client{}, err
	}
	if !ok {
		return session.User{}, authRequest{}, client{}, httpx.Validation("the AI client is no longer registered; start again from the client",
			map[string]string{"request": "invalid_value"})
	}
	return u, ar, c, nil
}

// redirectOrigin is what the page shows of the redirect uri: where the
// browser goes after approval.
func redirectOrigin(v string) string {
	u, err := url.Parse(v)
	if err != nil {
		return v
	}
	if u.Host == "" {
		return u.Scheme + ":"
	}
	return u.Scheme + "://" + u.Host
}

// grantableTenants are the user's projects a grant may name.
func (h *ConsentRoutes) grantableTenants(ctx context.Context, user uuid.UUID) ([]ConsentTenant, error) {
	out := []ConsentTenant{}
	err := h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select t.id, t.slug, t.name, t.status, m.role
			from cp.tenant_members m join cp.tenants t on t.id = m.tenant_id
			where m.user_id = $1 and t.status in ('active', 'pending') order by lower(t.name), t.id`, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t ConsentTenant
			if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.Role); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func (h *ConsentRoutes) inspect(w http.ResponseWriter, r *http.Request) error {
	u, ar, c, err := h.load(r)
	if err != nil {
		return err
	}
	ts, err := h.grantableTenants(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, ConsentRequest{
		ClientName: c.Name, ClientID: c.ID, RedirectOrigin: redirectOrigin(ar.RedirectURI), Scope: Scope,
		Resource: ar.Resource, ExpiresAt: httpx.Timestamp(time.Unix(ar.Expires, 0)), Tenants: ts,
		Operator: h.s.guard.Allowed(u),
	})
	return nil
}

type approveBody struct {
	Request  string     `json:"request"`
	Scope    string     `json:"scope"`
	TenantID *uuid.UUID `json:"tenant_id"`
}

type redirectAnswer struct {
	RedirectTo string `json:"redirect_to"`
}

// approve makes a single-use code for the chosen tenant or the operator
// view and answers where the page sends the browser.
func (h *ConsentRoutes) approve(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var body approveBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	ar, err := h.s.unseal(body.Request)
	if err != nil {
		return err
	}
	c, ok, err := h.s.client(ctx, ar.ClientID)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Validation("the AI client is no longer registered", map[string]string{"request": "invalid_value"})
	}
	var tenant uuid.NullUUID
	switch body.Scope {
	case ScopeTenant:
		if body.TenantID == nil {
			return httpx.Validation("choose a project", map[string]string{"tenant_id": "required"})
		}
		ts, err := h.grantableTenants(ctx, u.ID)
		if err != nil {
			return err
		}
		found := false
		for _, t := range ts {
			found = found || t.ID == *body.TenantID
		}
		if !found {
			// not a member, or a project that cannot be granted: the same
			// answer as an unknown id
			return httpx.NotFound()
		}
		tenant = uuid.NullUUID{UUID: *body.TenantID, Valid: true}
	case ScopeOperator:
		if body.TenantID != nil {
			return httpx.Validation("the operator view names no project", map[string]string{"tenant_id": "invalid_value"})
		}
		if !h.s.guard.Allowed(u) {
			return httpx.Forbidden(httpx.CodeOperatorRequired, "the operator view is for operators")
		}
	default:
		return httpx.Validation("scope must be tenant or operator", map[string]string{"scope": "invalid_value"})
	}
	plain, hash, err := newSecret(codePrefix)
	if err != nil {
		return err
	}
	now := h.s.now()
	err = h.s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from cp.mcp_oauth_codes where expires_at < $1`, now.Add(-time.Hour)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into cp.mcp_oauth_codes
			(hash, client_id, user_id, scope, tenant_id, redirect_uri, code_challenge, resource, created_at, expires_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			hash[:], c.ID, u.ID, body.Scope, tenant, ar.RedirectURI, ar.CodeChallenge, ar.Resource, now, now.Add(codeTTL)); err != nil {
			return err
		}
		var t any
		if tenant.Valid {
			t = tenant.UUID.String()
		}
		return insertAudit(ctx, tx, "user:"+u.ID.String(), "mcp_oauth.consent", "mcp_client:"+c.ID,
			map[string]any{"client_id": c.ID, "client_name": c.Name, "scope": body.Scope, "tenant_id": t})
	})
	if err != nil {
		return err
	}
	h.s.metrics.oauth.WithLabelValues("authorize", "approved").Inc()
	httpx.WriteJSON(w, http.StatusOK, redirectAnswer{RedirectTo: h.s.callbackURL(ar.RedirectURI, url.Values{"code": {plain}}, ar.State)})
	return nil
}

func (h *ConsentRoutes) deny(w http.ResponseWriter, r *http.Request) error {
	_, ar, _, err := h.load(r)
	if err != nil {
		return err
	}
	h.s.metrics.oauth.WithLabelValues("authorize", "access_denied").Inc()
	httpx.WriteJSON(w, http.StatusOK, redirectAnswer{RedirectTo: h.s.callbackURL(ar.RedirectURI,
		url.Values{"error": {"access_denied"}, "error_description": {"the user declined"}}, ar.State)})
	return nil
}
