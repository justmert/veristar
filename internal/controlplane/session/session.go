package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// cookie and lifetime settings of contract 1.1. the __Host- prefix makes
// the browser accept the cookie only from this host, with Secure, Path=/
// and no Domain, so no other host of the domain can set or overwrite it
const (
	CookieName  = "__Host-sa_session"
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
// the request state it adds first lets the store's commit tell the token
// the request arrived with from one made in it.
func (m *Manager) Load(next http.Handler) http.Handler {
	h := m.scs.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(withRequestState(r.Context())))
	})
}

// SignIn starts a signed-in session for userID. the token is renewed first,
// so a session id planted before sign-in is useless afterwards, and every
// value the browser's previous session held is dropped: a step-up or a
// ceremony of another sign-in never carries over into this one.
func (m *Manager) SignIn(ctx context.Context, userID uuid.UUID) error {
	// a sign-in starts a new session: the token it makes is inserted
	// whatever happened to the one the request arrived with
	if st := stateOf(ctx); st != nil {
		st.renewedFrom = nil
	}
	if err := m.scs.RenewToken(ctx); err != nil {
		return fmt.Errorf("session: renew token: %w", err)
	}
	if err := m.scs.Clear(ctx); err != nil {
		return fmt.Errorf("session: clear: %w", err)
	}
	m.scs.Put(ctx, userIDKey, userID.String())
	m.scs.Put(ctx, signedInAtKey, strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}

// SignedInAt is when the session's sign-in happened; false for a session
// without one.
func (m *Manager) SignedInAt(ctx context.Context) (time.Time, bool) {
	n, err := strconv.ParseInt(m.scs.GetString(ctx, signedInAtKey), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

// Value reads a string the request's session holds under key, "" when
// there is none. other packages keep their own state in the session
// through Value, SetValue and RemoveValue (the stepup package's ceremony
// state and step-up time); the keys are theirs to choose and must not be
// userIDKey or signedInAtKey.
func (m *Manager) Value(ctx context.Context, key string) string {
	return m.scs.GetString(ctx, key)
}

// SetValue stores a string in the request's session. Load writes it with
// the response.
func (m *Manager) SetValue(ctx context.Context, key, value string) {
	m.scs.Put(ctx, key, value)
}

// RemoveValue removes a value from the request's session.
func (m *Manager) RemoveValue(ctx context.Context, key string) {
	m.scs.Remove(ctx, key)
}

// Renew gives the session a new token and keeps its values and its
// deadline, for a privilege change inside a session (a step-up), so a token
// seen before the change is worth nothing after it. the deadline stays the
// one of the sign-in: renewing does not stretch the absolute lifetime. the
// new token is stored at commit only while the old row still exists, so a
// session ended while this request ran does not come back under it.
func (m *Manager) Renew(ctx context.Context) error {
	st := stateOf(ctx)
	if st == nil {
		return errors.New("session: renew outside the Load middleware")
	}
	if st.renewedFrom == nil {
		st.renewedFrom = st.loaded
	}
	deadline := m.scs.Deadline(ctx)
	if err := m.scs.RenewToken(ctx); err != nil {
		return fmt.Errorf("session: renew token: %w", err)
	}
	m.scs.SetDeadline(ctx, deadline)
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
		q := systemq.New(tx)
		if err := q.DeleteSession(ctx, sum[:]); err != nil {
			return err
		}
		// a token renewed in this request is not stored yet; the row is
		// still the old token's
		if st := stateOf(ctx); st != nil && st.renewedFrom != nil {
			if err := q.DeleteSession(ctx, st.renewedFrom); err != nil {
				return err
			}
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
		// the lifetime counts from the sign-in, whatever token the session
		// holds now; a session older than it is over even if its row says
		// otherwise
		if at, ok := m.SignedInAt(ctx); !ok || time.Since(at) >= Lifetime {
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
	return context.WithValue(context.WithValue(ctx, userKey{}, u), credentialKey{}, true)
}

type credentialKey struct{}

// ViaCredential reports whether the request's user came from WithUser (an
// mcp credential) rather than a console session. a console step-up does not
// apply to such a request: it has no session to record one on.
func ViaCredential(ctx context.Context) bool {
	v, _ := ctx.Value(credentialKey{}).(bool)
	return v
}

// DemoUserID is the user of every request of the public demo (contract
// 21). no cp.users row has it: uuid version 7 ids never take the all-ones
// value, so it can match no membership, invite or audit row.
var DemoUserID = uuid.Max

type demoKey struct{}

// WithDemo returns ctx carrying the anonymous viewer of the public demo as
// its user. the demo handler puts it there instead of a session; such a
// user is no operator and no member, and the tenant scope lets it read
// demo projects only.
func WithDemo(ctx context.Context) context.Context {
	return context.WithValue(context.WithValue(ctx, userKey{}, User{ID: DemoUserID}), demoKey{}, true)
}

// Demo reports whether the request's user is the viewer of the public demo.
func Demo(ctx context.Context) bool {
	v, _ := ctx.Value(demoKey{}).(bool)
	return v
}
