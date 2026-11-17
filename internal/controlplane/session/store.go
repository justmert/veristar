// Package session holds console sessions (contract 1.1): an scs session
// manager over cp.sessions, the middleware that loads it, and the current
// user of a request.
package session

import (
	"bytes"
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

// signedInAtKey holds the unix second of the sign-in that started the
// session, for checks that need a recent sign-in (the stepup package's
// enrollment window).
const signedInAtKey = "signed_in_at"

// Store is an scs.CtxStore over cp.sessions. pgxstore cannot be used: its
// sql is fixed to a token text column, while cp.sessions keeps only the
// 32-byte sha-256 of the token and a user_id column, so a
// database leak exposes no usable session token.
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

// requestState is what the store remembers of one request, so its commit
// can tell the token the request arrived with from a token made in it.
// scs commits every loaded session at the end of a request (the idle
// timeout moves its expiry), and that commit must not write back a row
// that a sign-out or a roster change deleted while the request ran.
type requestState struct {
	// loaded is the hash of the token whose row Find returned
	loaded []byte
	// renewedFrom is the hash of the token Renew replaced: its row is
	// deleted at commit, in the statement that inserts the new one
	renewedFrom []byte
}

type requestStateKey struct{}

// withRequestState returns ctx carrying a fresh request state; Load puts
// it on every request before scs reads the cookie.
func withRequestState(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestStateKey{}, &requestState{})
}

func stateOf(ctx context.Context) *requestState {
	st, _ := ctx.Value(requestStateKey{}).(*requestState)
	return st
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
	if st := stateOf(ctx); st != nil && st.loaded == nil {
		st.loaded = h
	}
	return data, true, nil
}

// CommitCtx writes the session and fills user_id from its data in the same
// statement, so all sessions of a user can be found and ended together.
//
// it never brings a deleted row back. the token the request arrived with
// is only updated, so a session ended while the request ran stays ended. a
// token renewed in this request replaces the old row only while that row
// still exists. any other token was made in this request and is inserted.
// a commit outside Load is refused: without the request state the store
// cannot tell the three apart.
func (s *Store) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	h, err := hashBytes(token)
	if err != nil {
		return err
	}
	st := stateOf(ctx)
	if st == nil {
		return errors.New("session: commit outside the Load middleware")
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
		q := systemq.New(tx)
		switch {
		case st.loaded != nil && bytes.Equal(h, st.loaded):
			_, err := q.UpdateSession(ctx, systemq.UpdateSessionParams{
				TokenHash: h, UserID: uid, Data: b, Expiry: expiry,
			})
			return err
		case st.renewedFrom != nil:
			_, err := q.RenewSession(ctx, systemq.RenewSessionParams{
				OldTokenHash: st.renewedFrom, TokenHash: h, UserID: uid, Data: b, Expiry: expiry,
			})
			return err
		default:
			return q.InsertSession(ctx, systemq.InsertSessionParams{
				TokenHash: h, UserID: uid, Data: b, Expiry: expiry,
			})
		}
	})
	if err != nil {
		return fmt.Errorf("session: commit: %w", err)
	}
	return nil
}

// DeleteCtx removes a session; an unknown one is not an error. the row of
// a token Renew replaced is left to the commit, which deletes it in the
// statement that inserts the new token.
func (s *Store) DeleteCtx(ctx context.Context, token string) error {
	h, err := hashBytes(token)
	if err != nil {
		return err
	}
	if st := stateOf(ctx); st != nil && st.renewedFrom != nil && bytes.Equal(h, st.renewedFrom) {
		return nil
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
