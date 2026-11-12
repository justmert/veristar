// Package umamirotate is the jobs task umami-rotate: shortly after
// 00:00 utc it sets every tenant user's umami password to the derived
// password of the new day. umami binds a token to the hash of the password
// (src/app/api/auth/login/route.ts), so the change ends every token issued
// on an earlier day, which bounds an umami token to about a day without
// redis. this task is the only place that changes an existing user's
// password.
package umamirotate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// TaskName is the name of the task in the jobs daemon.
const TaskName = "umami-rotate"

// Every is how often the task checks whether the day's rotation is done.
// it rotates at most once per utc day, so the period only sets how soon
// after midnight that happens.
const Every = 5 * time.Minute

// Actor and Action of the one audit row each rotation writes
// (docs/reference/control-plane-api.md 3.3).
const (
	Actor  = "system:umami-rotate"
	Action = "umami.password_rotate"
)

// userPrefix marks the umami users the platform made (gh-<github id>). the
// admin service account and anything else are never touched.
const userPrefix = "gh-"

// TxFunc runs fn in one fenced cp_system transaction (scheduler.Leader.SystemTx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Rotator holds what a rotation needs. all fields are required.
type Rotator struct {
	Admin *umami.Admin
	Key   umami.PasswordKey
	Now   func() time.Time
	Log   *slog.Logger
}

// Result says what one call did.
type Result struct {
	Day     int64
	Users   int
	Skipped bool
}

// Rotate sets the passwords of the current utc day unless that rotation is
// already recorded. the day is recorded, with its audit row, only after
// every user was set; a run that fails part way is repeated in full by the
// next one.
func (r *Rotator) Rotate(ctx context.Context, tx TxFunc) (Result, error) {
	if r.Admin == nil || r.Key.Empty() || r.Now == nil || r.Log == nil || tx == nil {
		return Result{}, errors.New("umamirotate: needs Admin, Key, Now, Log and a transaction")
	}
	today := umami.EpochDay(r.Now())
	var done int64
	err := tx(ctx, func(t pgx.Tx) error {
		d, err := systemq.New(t).UmamiPasswordDay(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		done = d
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("umamirotate: read rotated day: %w", err)
	}
	if done >= today {
		return Result{Day: today, Skipped: true}, nil
	}

	users, err := r.Admin.ListUsers(ctx)
	if err != nil {
		return Result{}, err
	}
	n := 0
	for _, u := range users {
		if !strings.HasPrefix(u.Username, userPrefix) || u.Role == umami.RoleAdmin {
			continue
		}
		pw, err := r.Key.Password(u.ID, today)
		if err != nil {
			return Result{}, err
		}
		if _, err := r.Admin.UpdateUser(ctx, u.ID, umami.UserUpdate{Password: pw}); err != nil {
			return Result{}, fmt.Errorf("umamirotate: user %s after %d of %d: %w", u.ID, n, len(users), err)
		}
		n++
	}

	detail, err := json.Marshal(map[string]any{"day": today, "users": n})
	if err != nil {
		return Result{}, err
	}
	err = tx(ctx, func(t pgx.Tx) error {
		q := systemq.New(t)
		if err := q.SetUmamiPasswordDay(ctx, today); err != nil {
			return err
		}
		_, err := q.InsertAuditLog(ctx, systemq.InsertAuditLogParams{
			// the row is about all users at once, so it names no single target
			Actor: Actor, Action: Action, Target: "", Detail: detail,
		})
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("umamirotate: record day %d: %w", today, err)
	}
	r.Log.Info("umami passwords rotated", "day", today, "users", n)
	return Result{Day: today, Users: n}, nil
}
