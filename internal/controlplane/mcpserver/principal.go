package mcpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/session"
)

// credential kinds and scopes of cp.mcp_credentials
const (
	KindKey   = "key"
	KindOAuth = "oauth"

	ScopeTenant   = "tenant"
	ScopeOperator = "operator"
)

// Principal is who a request to /mcp acts as: the credential, and the user
// it acts as with what that user may do right now.
type Principal struct {
	CredentialID uuid.UUID
	Kind         string
	Scope        string
	// TenantID is the one tenant of a tenant credential, uuid.Nil for an
	// operator credential
	TenantID uuid.UUID
	// TenantStatus and Role are read from cp.tenants and cp.tenant_members
	// on this request (tenant credentials only)
	TenantStatus string
	Role         string
	User         session.User
}

// errUnauthenticated is a credential that is not accepted. Reason is said
// to the client only for a credential that matched, so a guesser learns
// nothing from it.
type errUnauthenticated struct{ reason string }

func (e *errUnauthenticated) Error() string { return e.reason }

func unauthenticated(reason string) error { return &errUnauthenticated{reason: reason} }

// statuses in which a tenant credential works. suspended, rejected and
// removed tenants revoke their credentials (migration 00030); the check
// here also covers the moment between the status change and the trigger.
var usableTenantStatus = map[string]bool{"active": true, "pending": true}

// credentialRow is a credential with its user, membership and tenant.
type credentialRow struct {
	ID           uuid.UUID
	Kind         string
	Scope        string
	TenantID     uuid.NullUUID
	UserID       uuid.UUID
	Hash         []byte
	Resource     string
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	GitHubID     int64
	GitHubLogin  string
	IsOperator   bool
	Role         *string
	TenantStatus *string
}

//nolint:gosec // column names of a query, not a credential
const credentialColumns = `c.id, c.kind, c.scope, c.tenant_id, c.user_id, c.resource, c.expires_at, c.revoked_at,
	u.github_id, u.github_login, u.is_operator, m.role, t.status`

//nolint:gosec // joins of a query, not a credential
const credentialJoins = `
	join cp.users u on u.id = c.user_id
	left join cp.tenant_members m on m.tenant_id = c.tenant_id and m.user_id = c.user_id
	left join cp.tenants t on t.id = c.tenant_id`

func scanCredential(row pgx.Row, hash *[]byte) (credentialRow, error) {
	var c credentialRow
	err := row.Scan(hash, &c.ID, &c.Kind, &c.Scope, &c.TenantID, &c.UserID, &c.Resource, &c.ExpiresAt, &c.RevokedAt,
		&c.GitHubID, &c.GitHubLogin, &c.IsOperator, &c.Role, &c.TenantStatus)
	c.Hash = *hash
	return c, err
}

// authenticate turns an Authorization header into a principal. it reads
// the credential, its user, the membership and the tenant status in one
// cp_system statement on every request, so a revocation, a removed member,
// a suspended tenant or a lost operator flag applies to the very next
// request. nothing is cached.
func (s *Server) authenticate(ctx context.Context, header string) (Principal, error) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return Principal{}, unauthenticated("a bearer credential is required: an mcp access key (sa_mcp_...) or an oauth access token")
	}
	var c credentialRow
	var found bool
	var err error
	now := s.now()
	switch {
	case strings.HasPrefix(token, AccessTokenPrefix):
		hash, valid := parseSecret(token, AccessTokenPrefix)
		if !valid {
			return Principal{}, unauthenticated("the bearer value is not a valid credential")
		}
		c, found, err = s.lookup(ctx, hash, `select tk.hash, `+credentialColumns+`
			from cp.mcp_oauth_tokens tk
			join cp.mcp_credentials c on c.id = tk.credential_id`+credentialJoins+`
			where tk.hash = $1 and tk.kind = 'access' and tk.expires_at > $2`, now)
		if err == nil && !found {
			return Principal{}, unauthenticated("unknown or expired access token")
		}
	case strings.HasPrefix(token, KeyPrefix):
		hash, valid := parseSecret(token, KeyPrefix)
		if !valid {
			return Principal{}, unauthenticated("the bearer value is not a valid credential")
		}
		c, found, err = s.lookup(ctx, hash, `select c.hash, `+credentialColumns+`
			from cp.mcp_credentials c`+credentialJoins+`
			where c.hash = $1 and c.kind = 'key'`)
		if err == nil && !found {
			return Principal{}, unauthenticated("unknown mcp access key")
		}
	default:
		return Principal{}, unauthenticated("the bearer value is not a valid credential")
	}
	if err != nil {
		return Principal{}, err
	}
	return s.admit(c, now)
}

// lookup runs one credential query by the sha-256 of the presented
// secret. the stored hash is compared again in constant time.
func (s *Server) lookup(ctx context.Context, hash [32]byte, query string, args ...any) (credentialRow, bool, error) {
	var c credentialRow
	var stored []byte
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		c, qerr = scanCredential(tx.QueryRow(ctx, query, append([]any{hash[:]}, args...)...), &stored)
		return qerr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return credentialRow{}, false, nil
	}
	if err != nil {
		return credentialRow{}, false, fmt.Errorf("mcpserver: credential lookup: %w", err)
	}
	if subtle.ConstantTimeCompare(stored, hash[:]) != 1 {
		return credentialRow{}, false, nil
	}
	return c, true, nil
}

// admit applies the rules every request must pass.
func (s *Server) admit(c credentialRow, now time.Time) (Principal, error) {
	switch {
	case c.RevokedAt != nil:
		return Principal{}, unauthenticated("this credential was revoked")
	case c.ExpiresAt != nil && !c.ExpiresAt.After(now):
		return Principal{}, unauthenticated("this mcp access key expired")
	case c.Resource != s.resource:
		// audience binding (rfc 8707): a credential issued for another
		// resource uri is not for this server
		return Principal{}, unauthenticated("this credential was issued for another resource")
	}
	p := Principal{
		CredentialID: c.ID, Kind: c.Kind, Scope: c.Scope,
		User: session.User{ID: c.UserID, GitHubID: c.GitHubID, GitHubLogin: c.GitHubLogin, IsOperator: c.IsOperator},
	}
	switch c.Scope {
	case ScopeTenant:
		if !c.TenantID.Valid || c.Role == nil || c.TenantStatus == nil {
			return Principal{}, unauthenticated("the user of this credential is no longer a member of the project")
		}
		if !usableTenantStatus[*c.TenantStatus] {
			return Principal{}, unauthenticated("the project of this credential is " + *c.TenantStatus)
		}
		p.TenantID, p.Role, p.TenantStatus = c.TenantID.UUID, *c.Role, *c.TenantStatus
	case ScopeOperator:
		if !s.guard.Allowed(p.User) {
			return Principal{}, unauthenticated("the user of this credential is no longer an operator")
		}
	default:
		return Principal{}, fmt.Errorf("mcpserver: credential %s has scope %q", c.ID, c.Scope)
	}
	return p, nil
}

// touch stamps last_seen_at at most once a minute per credential, so a
// busy client costs one write a minute, not one per request.
func (s *Server) touch(ctx context.Context, id uuid.UUID, now time.Time) error {
	err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.mcp_credentials set last_seen_at = $2
			where id = $1 and (last_seen_at is null or last_seen_at < $2::timestamptz - interval '1 minute')`, id, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("mcpserver: last_seen_at: %w", err)
	}
	return nil
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principalFrom returns the principal authenticate put in the request.
func principalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// challenge is the WWW-Authenticate of a refused request: rfc 6750 with the
// protected resource metadata url of rfc 9728, which mcp clients follow to
// find the authorization server.
func (s *Server) challenge(w http.ResponseWriter, errCode, description string) {
	v := fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, s.resourceMetadataURL(), Scope)
	if errCode != "" {
		v += fmt.Sprintf(`, error=%q, error_description=%q`, errCode, description)
	}
	w.Header().Set("WWW-Authenticate", v)
}
