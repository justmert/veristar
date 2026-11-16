// Package umamiread reads umami's rest api on the private network as a
// member's own umami user. the console's web dashboard
// (docs/reference/control-plane-api.md section 18) and the mcp web tools
// (section 12.4.2) read through it. every read is checked twice: the control
// plane first checks that the user is a member of the site's tenant, then
// umami itself checks that the user's team holds the website, so a mistake
// in either check alone never shows another tenant's numbers.
package umamiread

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/umami"
)

// failure codes of a session that could not be opened
const (
	CodeNotFound            = "not_found"
	CodeProvisioningPending = "provisioning_pending"
	CodeUmamiUnavailable    = "umami_unavailable"
)

// SystemTx runs fn in a cp_system transaction (db.WithSystem).
type SystemTx func(ctx context.Context, fn func(pgx.Tx) error) error

// Sessions logs in to umami as a member's umami user. all fields are required.
type Sessions struct {
	// Tx reads the site, the membership and the password day as cp_system.
	Tx SystemTx
	// Umami is the client on the private network; login needs no admin.
	Umami *umami.Client
	Key   umami.PasswordKey
	Now   func() time.Time
}

// Check reports a missing field, so a misconfigured server stops at startup.
func (s *Sessions) Check() error {
	if s.Tx == nil || s.Umami == nil || s.Key.Empty() || s.Now == nil {
		return errors.New("umamiread: sessions need Tx, Umami, Key and Now")
	}
	return nil
}

// Site is the part of a site a umami session was opened for.
type Site struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Status              string
	QuarantineWebsiteID uuid.NullUUID
}

// failure is a session that could not be opened for a reason the caller
// shows, rather than an internal error.
type failure struct {
	code string
	err  error
}

func (f *failure) Error() string {
	if f.err != nil {
		return f.code + ": " + f.err.Error()
	}
	return f.code
}

func (f *failure) Unwrap() error { return f.err }

func fail(code string, err error) error { return &failure{code: code, err: err} }

// FailureCode returns the failure code of an error from SiteSession or
// Reader.Read, and false for any other error.
func FailureCode(err error) (string, bool) {
	var f *failure
	if errors.As(err, &f) {
		return f.code, true
	}
	return "", false
}

// SiteSession logs in to umami as the umami user of user, for a site of a
// tenant user is a member of. the error is a failure whose code FailureCode
// reads, or an internal error.
func (s *Sessions) SiteSession(ctx context.Context, user, siteID uuid.UUID) (Site, umami.Session, error) {
	now := s.Now()
	var site systemq.MemberSiteRow
	var username *string
	var rotatedDay int64
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var err error
		if site, err = q.MemberSite(ctx, siteID); err != nil {
			return err
		}
		if username, err = q.MemberUmamiUser(ctx, systemq.MemberUmamiUserParams{TenantID: site.TenantID, UserID: user}); err != nil {
			return err
		}
		rotatedDay, err = q.UmamiPasswordDay(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			// no rotation has completed yet
			rotatedDay, err = 0, nil
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// unknown site or not a member: the same answer, so ids reveal nothing
		return Site{}, umami.Session{}, fail(CodeNotFound, nil)
	}
	if err != nil {
		return Site{}, umami.Session{}, fmt.Errorf("umamiread: read site: %w", err)
	}
	if site.TenantStatus == "removed" {
		return Site{}, umami.Session{}, fail(CodeNotFound, nil)
	}
	// a site in review (contract 5.6) is treated like a pending tenant: the
	// reconciler made no umami website for it, so there is nothing to read
	if site.Status == "removed" || site.Status == "pending" || !site.UmamiTeamID.Valid {
		return Site{}, umami.Session{}, fail(CodeProvisioningPending, nil)
	}
	// the reconciler names the umami user when it first creates it; until
	// then there is no user to log in as
	if username == nil {
		return Site{}, umami.Session{}, fail(CodeProvisioningPending, nil)
	}
	sess, err := s.login(ctx, user.String(), *username, now, rotatedDay)
	if err != nil {
		return Site{}, umami.Session{}, err
	}
	if sess.User.ID != user.String() {
		return Site{}, umami.Session{}, fmt.Errorf("umamiread: umami user %s belongs to id %s", *username, sess.User.ID)
	}
	// the reconciler adds the membership; until it has, every read would
	// be refused
	team := site.UmamiTeamID.UUID.String()
	member := false
	for _, t := range sess.User.Teams {
		member = member || t.ID == team
	}
	if !member {
		return Site{}, umami.Session{}, fail(CodeProvisioningPending, nil)
	}
	return Site{ID: site.ID, TenantID: site.TenantID, Status: site.Status, QuarantineWebsiteID: site.QuarantineWebsiteID}, sess, nil
}

// login logs in with the derived password of today. until the umami-rotate
// task has finished today's rotation, users made before midnight still have
// yesterday's password, so then, and only then, yesterday's is tried after
// today's is refused. a user whose password is older than that is refused,
// which surfaces a rotation that stopped running.
func (s *Sessions) login(ctx context.Context, userID, username string, now time.Time, rotatedDay int64) (umami.Session, error) {
	today := umami.EpochDay(now)
	days := []int64{today}
	if rotatedDay < today {
		days = append(days, today-1)
	}
	for _, day := range days {
		pw, err := s.Key.Password(userID, day)
		if err != nil {
			return umami.Session{}, err
		}
		sess, err := s.Umami.Login(ctx, username, pw)
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, umami.ErrUnauthorized) {
			return umami.Session{}, fail(CodeUmamiUnavailable, err)
		}
	}
	// no such user yet, or a password the reconciler and rotation have not
	// set: provisioning has not caught up with this user
	return umami.Session{}, fail(CodeProvisioningPending, nil)
}
