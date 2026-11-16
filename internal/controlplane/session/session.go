package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// cookie and lifetime settings of contract 1.1
const (
	CookieName  = "sa_session"
	Lifetime    = 7 * 24 * time.Hour
	IdleTimeout = 24 * time.Hour
)

// User is the signed-in user of a request, read from cp.users on every
// request so a changed login, a linked or unlinked identity or a changed
// operator flag applies at once. an account has a github identity, a
// stellar identity or both (contract 4.1.8); the absent one is nil.
type User struct {
	ID             uuid.UUID
	GitHubID       *int64
	GitHubLogin    *string
	StellarAddress *string
	PasskeyAddress *string
	IsOperator     bool
}

// DisplayName is the github login, or the short address of a wallet or
// passkey account without one.
func (u User) DisplayName() string {
	return identity.DisplayName(u.GitHubLogin, u.StellarAddress, u.PasskeyAddress)
}

// FromRow builds the User of a cp.users row.
func FromRow(u systemq.CpUser) User {
	return User{
		ID: u.ID, GitHubID: u.GithubID, GitHubLogin: u.GithubLogin,
		StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress, IsOperator: u.IsOperator,
	}
}

// Manager holds the scs session manager and the database it reads users from.
type Manager struct {
	scs *scs.SessionManager
	db  *db.DB
}

// New returns a manager over cp.sessions. every cookie attribute is set
// explicitly: scs defaults Secure to false, and the others are part of the
// contract rather than of a library default.
func New(d *db.DB) *Manager {
	sm := scs.New()
	sm.Lifetime = Lifetime
	sm.IdleTimeout = IdleTimeout
	sm.HashTokenInStore = true
	sm.Codec = scs.GobCodec{}
	sm.Store = NewStore(d, sm.Codec)
	sm.Cookie = scs.SessionCookie{
		Name:     CookieName,
		Domain:   "", // host-only, never sent to web. or in.
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
		Persist:  true,
	}
	sm.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		// a store failure means the database is unreachable or refused the
		// statement; the client may retry, so 503 rather than 500
		httpx.Logger(r.Context()).Error("session store failed", "error", err.Error())
		httpx.WriteError(w, r, httpx.Unavailable("session store unavailable"))
	}
	return &Manager{scs: sm, db: d}
}

// Load is the middleware that loads the session of every request and
// writes the cookie when the session changed. it goes after httpx.Base.
func (m *Manager) Load(next http.Handler) http.Handler {
	return m.scs.LoadAndSave(next)
}

// SignIn starts a signed-in session for userID. the token is renewed first,
// so a session id planted before sign-in is useless afterwards.
func (m *Manager) SignIn(ctx context.Context, userID uuid.UUID) error {
	if err := m.scs.RenewToken(ctx); err != nil {
		return fmt.Errorf("session: renew token: %w", err)
	}
	m.scs.Put(ctx, userIDKey, userID.String())
	return nil
}

// SignOut deletes the session row and makes Load expire the cookie. inTx,
// when not nil, runs in the same cp_system transaction as the delete (the
// caller's audit row). without a session it does nothing, so logout is
// idempotent.
func (m *Manager) SignOut(ctx context.Context, inTx func(pgx.Tx) error) error {
	tok := m.scs.Token(ctx)
	if tok == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(tok))
	err := m.db.WithSystem(ctx, func(tx pgx.Tx) error {
		if err := systemq.New(tx).DeleteSession(ctx, sum[:]); err != nil {
			return err
		}
		if inTx != nil {
			return inTx(tx)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("session: sign out: %w", err)
	}
	// the row is gone already; Destroy clears the values and marks the
	// cookie for expiry
	if err := m.scs.Destroy(ctx); err != nil {
		return fmt.Errorf("session: destroy: %w", err)
	}
	return nil
}

// Destroy clears the request's session values and makes Load expire the
// cookie, for a request that deleted the session rows itself in its own
// transaction (account deletion). the store's delete then finds no row,
// which is not an error.
func (m *Manager) Destroy(ctx context.Context) error {
	if err := m.scs.Destroy(ctx); err != nil {
		return fmt.Errorf("session: destroy: %w", err)
	}
	return nil
}

// UserID returns the user id stored in the session, if any.
func (m *Manager) UserID(ctx context.Context) (uuid.UUID, bool) {
	s := m.scs.GetString(ctx, userIDKey)
	if s == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

type userKey struct{}

// RequireUser answers 401 unauthenticated unless the request has a
// signed-in session whose user still exists, and makes the user available
// through CurrentUser. it goes after Load.
func (m *Manager) RequireUser(next http.Handler) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, ok := m.UserID(ctx)
		if !ok {
			return httpx.Unauthenticated()
		}
		var u systemq.CpUser
		err := m.db.WithSystem(ctx, func(tx pgx.Tx) error {
			var qerr error
			u, qerr = systemq.New(tx).GetUser(ctx, id)
			return qerr
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// the user row is gone (sessions cascade with it, so this is a
			// race with a delete); the session is worthless
			return httpx.Unauthenticated()
		}
		if err != nil {
			return fmt.Errorf("session: load user: %w", err)
		}
		ctx = context.WithValue(ctx, userKey{}, FromRow(u))
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// CurrentUser returns the user that RequireUser loaded.
func CurrentUser(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// MustUser returns the user that RequireUser loaded, or 401 unauthenticated
// when the route was mounted without RequireUser.
func MustUser(ctx context.Context) (User, error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return User{}, httpx.Unauthenticated()
	}
	return u, nil
}

// WithUser returns ctx carrying u as the signed-in user, for requests that
// authenticate without a session cookie: the mcp server puts the user its
// credential acts as here before it calls the same read handlers as the
// console, so membership and operator checks stay the console's own. the
// caller must have read u from cp.users for this request.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}
