package retainweb_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/jobs/retainweb"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func superuser(t *testing.T, um *umamictr.Stack) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(t.Context(), fmt.Sprintf("postgres://postgres:%s@%s:%d/umami?sslmode=disable",
		url.QueryEscape(um.PostgresPassword), um.PostgresHost, um.PostgresPort))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func send(t *testing.T, um *umamiStack, website, salt string, at time.Time, name string) {
	t.Helper()
	r, err := um.Client.Send(t.Context(), umami.SendRequest{
		Event: umami.Event{Website: website, Hostname: "retain.example", URL: "/p", Title: "t", Screen: "1440x900",
			Language: "en-US", Name: name, Timestamp: at.Unix(), Salt: salt},
		UserAgent: ua,
	})
	require.NoError(t, err)
	require.False(t, r.Bot)
}

type umamiStack = umamictr.Stack

func count(t *testing.T, c *pgx.Conn, q string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, c.QueryRow(t.Context(), q, args...).Scan(&n))
	return n
}

// TestRetainWeb runs the job as jobs_retention against the pinned umami
// image: rows older than the cutoff go, newer rows and other websites'
// newer rows stay, an event whose session was deleted goes, the role cannot
// touch any other table, and a changed migration set stops the job before
// it deletes anything.
func TestRetainWeb(t *testing.T) {
	ctx := t.Context()
	um := umamictr.Start(t)
	su := superuser(t, um)
	pw := "retention-" + uuid.NewString()
	require.NoError(t, retainweb.Provision(ctx, su, pw))
	// provisioning twice is harmless and sets the password again
	require.NoError(t, retainweb.Provision(ctx, su, pw))

	team, _, err := um.Admin.CreateTeam(ctx, "retain", "")
	require.NoError(t, err)
	site := uuid.NewString()
	_, err = um.Admin.CreateWebsite(ctx, umami.NewWebsite{ID: site, Name: "retain", Domain: "retain.example", TeamID: team.ID})
	require.NoError(t, err)

	now := time.Now().UTC()
	months := config.DefaultWebRawRetentionMonths
	cutoff := retainweb.Cutoff(now, months)
	old := cutoff.Add(-30 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)
	send(t, um, site, "old-visitor", old, "")
	send(t, um, site, "old-visitor", old.Add(time.Minute), "signup")
	send(t, um, site, "edge-visitor", cutoff.Add(time.Hour), "")
	send(t, um, site, "recent-visitor", recent, "")
	send(t, um, site, "recent-visitor", recent.Add(time.Minute), "signup")
	send(t, um, site, "orphan-visitor", now.Add(-time.Hour), "")

	require.Equal(t, int64(2), count(t, su, `select count(*) from website_event where created_at < $1`, cutoff))
	require.Equal(t, int64(1), count(t, su, `select count(*) from session where created_at < $1`, cutoff))
	// an event left behind by a session deleted under it (the erasure race
	// of docs/compliance/erasure.md)
	var orphanSession string
	require.NoError(t, su.QueryRow(ctx, `select session_id::text from website_event where created_at > $1 order by created_at desc limit 1`,
		now.Add(-2*time.Hour)).Scan(&orphanSession))
	_, err = su.Exec(ctx, `delete from session where session_id = $1`, orphanSession)
	require.NoError(t, err)
	totalBefore := count(t, su, `select count(*) from website_event`)

	cfg := retainweb.Config{Host: um.PostgresHost, Port: um.PostgresPort, Database: "umami", SSLMode: "disable",
		Password: config.FromMap(map[string]string{"P": pw}).Secret("P", 1), Months: months}
	conn, err := pgx.Connect(ctx, cfg.ConnString())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	res, err := retainweb.Run(ctx, conn, cutoff, now, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.Equal(t, int64(3), res.Deleted["website_event"], "two old events and the orphan")
	require.Equal(t, int64(1), res.Deleted["session"])
	require.Zero(t, count(t, su, `select count(*) from website_event where created_at < $1`, cutoff))
	require.Zero(t, count(t, su, `select count(*) from session where created_at < $1`, cutoff))
	require.Zero(t, count(t, su, `select count(*) from website_event where session_id = $1`, orphanSession))
	require.Equal(t, totalBefore-3, count(t, su, `select count(*) from website_event`))
	// the event just after the cutoff and the recent visitor stay
	require.Equal(t, int64(1), count(t, su, `select count(*) from website_event where created_at >= $1 and created_at < $2`, cutoff, cutoff.Add(2*time.Hour)))
	require.Equal(t, int64(2), count(t, su, `select count(*) from website_event where created_at between $1 and $2`, recent.Add(-time.Second), recent.Add(2*time.Minute)))

	// a second run finds nothing more
	res, err = retainweb.Run(ctx, conn, cutoff, now, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	for table, n := range res.Deleted {
		require.Zero(t, n, table)
	}

	// the role reaches nothing else
	for _, stmt := range []string{
		`delete from website`, `delete from "user"`, `delete from team`, `update website_event set url_path = '/x'`,
		`insert into session (session_id, website_id) values (gen_random_uuid(), gen_random_uuid())`,
		`delete from _prisma_migrations`,
	} {
		_, err := conn.Exec(ctx, stmt)
		require.ErrorContains(t, err, "permission denied", stmt)
	}

	// an untested migration set: the job refuses before deleting anything
	send(t, um, site, "late-visitor", old, "")
	_, err = su.Exec(ctx, `insert into _prisma_migrations (id, checksum, finished_at, migration_name, started_at, applied_steps_count)
		values ($1, 'f00d', now(), '28_future_change', now(), 1)`, uuid.NewString())
	require.NoError(t, err)
	_, err = retainweb.Run(ctx, conn, cutoff, now, slog.New(slog.DiscardHandler))
	require.ErrorIs(t, err, retainweb.ErrUntested)
	require.ErrorContains(t, err, "28_future_change")
	require.Equal(t, int64(1), count(t, su, `select count(*) from website_event where created_at < $1`, cutoff), "nothing was deleted")

	// a changed checksum of a known migration counts as untested too
	_, err = su.Exec(ctx, `delete from _prisma_migrations where migration_name = '28_future_change'`)
	require.NoError(t, err)
	_, err = su.Exec(ctx, `update _prisma_migrations set checksum = 'changed' where migration_name = '27_add_error_tracking'`)
	require.NoError(t, err)
	_, err = retainweb.Run(ctx, conn, cutoff, now, slog.New(slog.DiscardHandler))
	require.ErrorIs(t, err, retainweb.ErrUntested)
	require.Equal(t, int64(1), count(t, su, `select count(*) from website_event where created_at < $1`, cutoff))
}

func TestCutoff(t *testing.T) {
	now := time.Date(2026, 11, 12, 15, 4, 5, 0, time.UTC)
	require.Equal(t, time.Date(2024, 10, 12, 0, 0, 0, 0, time.UTC), retainweb.Cutoff(now, 25))
	require.Equal(t, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), retainweb.Cutoff(now, 1))
}
