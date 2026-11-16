package chainactivity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/chain"
)

// weekly active accounts per project and iso week in utc, by the scf
// growth hack official rules: "an active account means the account that
// initiated at least one operation".

// WeekGraceDays is how long after its sunday a week waits for days that
// failed to read before it closes as missing. rpc events stay readable for
// about seven days, horizon for a year, so a week that can still be read
// whole gets that chance.
const WeekGraceDays = 7

// reasons a closed week has no count, next to the read codes of package
// chain (day_over_read_cap, before_source_history and the last error of an
// id)
const (
	MissingNoVerifiedIDs = "no_verified_ids"
	MissingNotReadYet    = "not_read_yet"
)

// initiatorsRead marks a stored day whose initiators are in its week
// (agg.chain_daily.initiators)
const initiatorsRead = "read"

// WeekStart is the monday of t's iso week, utc.
func WeekStart(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// OpenFrom is the monday of the oldest week that can still be open on
// today: a week closes at the latest WeekGraceDays after its sunday.
func OpenFrom(today time.Time) time.Time {
	return WeekStart(today.AddDate(0, 0, -WeekGraceDays))
}

func newKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("chainactivity: week key: %w", err)
	}
	return k, nil
}

// Member is the keyed hash of an account under a week's key.
func Member(key []byte, account string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(account))
	return m.Sum(nil)
}

// openWeek creates the open row of a project's week with a fresh key, and
// leaves an existing row as it is.
func openWeek(ctx context.Context, x pgx.Tx, tenant uuid.UUID, week time.Time) error {
	key, err := newKey()
	if err != nil {
		return err
	}
	_, err = x.Exec(ctx, `insert into agg.chain_weeks (tenant_id, week, hash_key) values ($1, $2, $3)
		on conflict (tenant_id, week) do nothing`, tenant, week, key)
	if err != nil {
		return fmt.Errorf("open week: %w", err)
	}
	return nil
}

// openWeeks opens the current week of every project that declares an id,
// so a week in which nothing could be read still gets a row and closes
// with a reason rather than leaving a gap.
func (t *Task) openWeeks(ctx context.Context, tx TxFunc, today time.Time) error {
	week := WeekStart(today)
	var tenants []uuid.UUID
	err := tx(ctx, func(x pgx.Tx) error {
		rows, err := x.Query(ctx, `
			select distinct c.tenant_id from cp.chain_ids c
			join cp.tenants t on t.id = c.tenant_id and t.status <> 'removed'
			where not exists (select 1 from agg.chain_weeks w where w.tenant_id = c.tenant_id and w.week = $1)`, week)
		if err != nil {
			return err
		}
		tenants, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		return fmt.Errorf("chainactivity: list weeks to open: %w", err)
	}
	for _, id := range tenants {
		err := tx(ctx, func(x pgx.Tx) error {
			if err := lockTenant(ctx, x, id); err != nil {
				return err
			}
			removed, err := isRemoved(ctx, x, id)
			if err != nil || removed {
				return err
			}
			return openWeek(ctx, x, id, week)
		})
		if err != nil {
			return fmt.Errorf("chainactivity: open week of %s: %w", id, err)
		}
	}
	return nil
}

func lockTenant(ctx context.Context, x pgx.Tx, tenant uuid.UUID) error {
	if _, err := x.Exec(ctx, `select pg_advisory_xact_lock_shared(hashtextextended('agg.tenant:' || $1::text, 0))`,
		tenant.String()); err != nil {
		return fmt.Errorf("lock tenant: %w", err)
	}
	return nil
}

func isRemoved(ctx context.Context, x pgx.Tx, tenant uuid.UUID) (bool, error) {
	var status string
	if err := x.QueryRow(ctx, `select status from cp.tenants where id = $1`, tenant).Scan(&status); err != nil {
		return false, fmt.Errorf("tenant status: %w", err)
	}
	return status == "removed", nil
}

// weekKeys caches the keys of the weeks one store touches; a nil key is a
// closed week.
type weekKeys map[time.Time][]byte

// add puts a day's initiators into its open week and returns the day's
// initiators state. a closed week takes no members, and the day keeps the
// state it had (nil).
func (w weekKeys) add(ctx context.Context, x pgx.Tx, tenant uuid.UUID, strkey string, day chain.Day) (*string, error) {
	week := WeekStart(day.Day)
	key, ok := w[week]
	if !ok {
		if err := openWeek(ctx, x, tenant, week); err != nil {
			return nil, err
		}
		if err := x.QueryRow(ctx, `select hash_key from agg.chain_weeks where tenant_id = $1 and week = $2 for update`,
			tenant, week).Scan(&key); err != nil {
			return nil, fmt.Errorf("week key: %w", err)
		}
		w[week] = key
	}
	if key == nil {
		return nil, nil
	}
	if day.InitiatorsMissing != "" {
		v := day.InitiatorsMissing
		return &v, nil
	}
	if len(day.Initiators) > 0 {
		members := make([][]byte, len(day.Initiators))
		for i, a := range day.Initiators {
			members[i] = Member(key, a)
		}
		if _, err := x.Exec(ctx, `
			insert into agg.chain_week_members (tenant_id, week, strkey, member)
			select $1, $2, $3, m from unnest($4::bytea[]) as m
			on conflict do nothing`, tenant, week, strkey, members); err != nil {
			return nil, fmt.Errorf("week members: %w", err)
		}
	}
	v := initiatorsRead
	return &v, nil
}

// weekState is what one verified id contributes to closing a week.
type weekState struct {
	strkey    string
	kind      string
	read      int
	capped    int
	before    int
	lastError *string
	// the first day the id's source held when it was first read
	coveredFrom *time.Time
}

// closeWeeks closes every open week whose sunday is over: with the count
// of distinct initiators over the project's verified ids when each of them
// has all seven days read, as missing at once when a day can never be read
// whole, and as missing with the first reason found once the grace days
// are over. it returns how many weeks it closed.
func (t *Task) closeWeeks(ctx context.Context, tx TxFunc, today, now time.Time) (int, error) {
	type open struct {
		tenant uuid.UUID
		week   time.Time
	}
	var list []open
	err := tx(ctx, func(x pgx.Tx) error {
		rows, err := x.Query(ctx, `
			select w.tenant_id, w.week from agg.chain_weeks w
			join cp.tenants t on t.id = w.tenant_id and t.status <> 'removed'
			where w.closed_at is null and w.week + 7 <= $1::date
			order by w.week, w.tenant_id`, today)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (open, error) {
			var o open
			return o, r.Scan(&o.tenant, &o.week)
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("chainactivity: list open weeks: %w", err)
	}
	closed := 0
	var errs []error
	for _, o := range list {
		err := tx(ctx, func(x pgx.Tx) error {
			if err := lockTenant(ctx, x, o.tenant); err != nil {
				return err
			}
			if removed, err := isRemoved(ctx, x, o.tenant); err != nil || removed {
				return err
			}
			done, err := closeWeek(ctx, x, o.tenant, o.week, today, now)
			if done {
				closed++
			}
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("week %s of %s: %w", o.week.Format(time.DateOnly), o.tenant, err))
		}
	}
	if len(errs) > 0 {
		return closed, fmt.Errorf("chainactivity: %d weeks could not close: %w", len(errs), errors.Join(errs...))
	}
	return closed, nil
}

func closeWeek(ctx context.Context, x pgx.Tx, tenant uuid.UUID, week, today, now time.Time) (bool, error) {
	rows, err := x.Query(ctx, `
		select c.strkey, c.kind,
		       count(d.day) filter (where d.initiators = 'read')::int,
		       count(d.day) filter (where d.initiators = 'day_over_read_cap')::int,
		       count(d.day) filter (where d.initiators = 'before_source_history')::int,
		       r.last_error, r.covered_from
		from cp.chain_ids c
		left join agg.chain_daily d on d.tenant_id = c.tenant_id and d.strkey = c.strkey
		  and d.day between $2::date and $2::date + 6
		left join agg.chain_reads r on r.tenant_id = c.tenant_id and r.strkey = c.strkey
		where c.tenant_id = $1 and c.verified_at is not null
		group by c.strkey, c.kind, r.last_error, r.covered_from
		order by c.kind, c.strkey`, tenant, week)
	if err != nil {
		return false, fmt.Errorf("week state: %w", err)
	}
	ids, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (weekState, error) {
		var s weekState
		return s, r.Scan(&s.strkey, &s.kind, &s.read, &s.capped, &s.before, &s.lastError, &s.coveredFrom)
	})
	if err != nil {
		return false, fmt.Errorf("week state: %w", err)
	}
	reason, complete, isFinal := weekReason(ids, week)
	graceOver := !today.Before(week.AddDate(0, 0, 7+WeekGraceDays))
	switch {
	case complete:
		strkeys := make([]string, len(ids))
		for i, s := range ids {
			strkeys[i] = s.strkey
		}
		var n int64
		if err := x.QueryRow(ctx, `
			select count(distinct member) from agg.chain_week_members
			where tenant_id = $1 and week = $2 and strkey = any($3::text[])`, tenant, week, strkeys).Scan(&n); err != nil {
			return false, fmt.Errorf("count members: %w", err)
		}
		return true, finish(ctx, x, tenant, week, &n, nil, now)
	case reason != "" && (graceOver || isFinal):
		return true, finish(ctx, x, tenant, week, nil, &reason, now)
	}
	return false, nil
}

// weekReason says whether every verified id has its seven days read, and
// otherwise why not. a day whose initiators can never be read (over the
// read cap, or no longer held by the source) is final and closes the week
// at once; otherwise the reason is the first unread id's last read error,
// or not_read_yet, and more reads can still complete the week. an id whose
// source first held a day after the week's monday cannot complete it
// either. a project without a verified id has no footprint to count.
func weekReason(ids []weekState, week time.Time) (reason string, complete, final bool) {
	if len(ids) == 0 {
		return MissingNoVerifiedIDs, false, false
	}
	for _, s := range ids {
		switch {
		case s.capped > 0:
			return chain.CodeDayTooLarge, false, true
		case s.before > 0, s.read < 7 && s.coveredFrom != nil && s.coveredFrom.After(week):
			return chain.CodeBeforeHistory, false, true
		case s.read == 7:
		case reason == "" && s.lastError != nil:
			reason = *s.lastError
		case reason == "":
			reason = MissingNotReadYet
		}
	}
	return reason, reason == "", false
}

// finish stores the week's result and deletes its members and key.
func finish(ctx context.Context, x pgx.Tx, tenant uuid.UUID, week time.Time, n *int64, missing *string, now time.Time) error {
	if _, err := x.Exec(ctx, `delete from agg.chain_week_members where tenant_id = $1 and week = $2`, tenant, week); err != nil {
		return fmt.Errorf("delete members: %w", err)
	}
	if _, err := x.Exec(ctx, `
		update agg.chain_weeks set active_accounts = $3, missing = $4, closed_at = $5, hash_key = null
		where tenant_id = $1 and week = $2`, tenant, week, n, missing, now); err != nil {
		return fmt.Errorf("close week: %w", err)
	}
	return nil
}
