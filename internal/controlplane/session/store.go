// Package session holds console sessions (contract 1.1): an scs session
// manager over cp.sessions, the middleware that loads it, and the current
// user of a request.
package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// userIDKey is the session value that holds the signed-in user id, as a
// string so the gob codec needs no type registration.
const userIDKey = "user_id"

// Store is an scs.CtxStore over cp.sessions. pgxstore cannot be used: its
// sql is fixed to a token text column, while cp.sessions keeps only the
// 32-byte sha-256 of the token and a user_id column (the reference docs 3.1).
//
// the manager must run with HashTokenInStore, so the "token" this store
// receives is base64url(sha256(cookie value)); the store decodes it back to
// the 32 raw bytes. the cookie value itself never reaches the database.
type Store struct {
	db    *db.DB
	codec scs.Codec
}

var _ scs.CtxStore = (*Store)(nil)

// NewStore returns a store that runs every statement through db.WithSystem.
// codec must be the manager's codec: Commit decodes the session data with it
// to fill cp.sessions.user_id.
func NewStore(d *db.DB, codec scs.Codec) *Store {
	return &Store{db: d, codec: codec}
}

// hashBytes turns the hashed token scs passes in into token_hash. anything
// else (a manager without HashTokenInStore) is refused rather than stored.
func hashBytes(hashed string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(hashed)
	if err != nil || len(b) != 32 {
		return nil, errors.New("session: store expects a sha-256 token hash; is HashTokenInStore set?")
	}
	return b, nil
}

// FindCtx returns the data of an unexpired session.
func (s *Store) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	h, err := hashBytes(token)
	if err != nil {
		return nil, false, err
	}
	var data []byte
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		data, qerr = systemq.New(tx).FindSession(ctx, h)
		return qerr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("session: find: %w", err)
	}
	return data, true, nil
}

// CommitCtx writes the session and fills user_id from its data in the same
// statement, so all sessions of a user can be found and ended together.
func (s *Store) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	h, err := hashBytes(token)
	if err != nil {
		return err
	}
	_, values, err := s.codec.Decode(b)
	if err != nil {
		return fmt.Errorf("session: decode for commit: %w", err)
	}
	var uid uuid.NullUUID
	if v, ok := values[userIDKey]; ok {
		str, ok := v.(string)
		if !ok {
			return fmt.Errorf("session: %s has type %T, want string", userIDKey, v)
		}
		id, err := uuid.Parse(str)
		if err != nil {
			return fmt.Errorf("session: %s is not a uuid", userIDKey)
		}
		uid = uuid.NullUUID{UUID: id, Valid: true}
	}
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return systemq.New(tx).CommitSession(ctx, systemq.CommitSessionParams{
			TokenHash: h, UserID: uid, Data: b, Expiry: expiry,
		})
	})
	if err != nil {
		return fmt.Errorf("session: commit: %w", err)
	}
	return nil
}

// DeleteCtx removes a session; an unknown one is not an error.
func (s *Store) DeleteCtx(ctx context.Context, token string) error {
	h, err := hashBytes(token)
	if err != nil {
		return err
	}
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return systemq.New(tx).DeleteSession(ctx, h)
	})
	if err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	return nil
}

// Find, Commit and Delete complete scs.Store. scs calls the Ctx methods
// whenever they exist, so these only serve callers without a context.
func (s *Store) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

// Commit is CommitCtx without a context.
func (s *Store) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

// Delete is DeleteCtx without a context.
func (s *Store) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}
