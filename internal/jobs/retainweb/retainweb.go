// Package retainweb is `jobs retain-web` (the design): it deletes raw
// umami event and session rows older than WEB_RAW_RETENTION_MONTHS. umami
// has no retention feature and no delete-by-date route (the design,
// correction 7), so the job deletes in umami's own tables, as the role
// jobs_retention, which may read and delete those tables and nothing else.
// before it deletes anything it checks that the database holds exactly the
// umami migrations it was tested with, and the columns it uses.
package retainweb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
)

// TaskName is the jobs subcommand.
const TaskName = "retain-web"

// Role is the database role of the job, in the umami database.
const Role = "jobs_retention"

// Migration is one row of _prisma_migrations.
type Migration struct {
	Name     string
	Checksum string
}

// timeTables are the per-event tables of umami 3.5.0 that the job deletes
// by their own created_at. session replay, heatmaps, revenue and error
// tracking are disabled here (their client code is not shipped and their
// routes are blocked, docs/compliance/privacy-strategy.md), so those tables
// stay empty; they are covered anyway, so a row that reached one does not
// outlive the retention period. event_data goes before website_event, its
// parent.
var timeTables = []string{
	"event_data", "website_event", "revenue", "heatmap_event", "error_event",
	"session_replay", "session_replay_saved",
}

// sessionTables hang off a session; their rows go when the session goes.
var sessionTables = []string{"session_data", "session_link"}

// Tables are every table the job deletes from; Provision grants select and
// delete on exactly these, and select on _prisma_migrations.
var Tables = append(append(append([]string(nil), timeTables...), "session"), sessionTables...)

// columns the job's statements use, per table
var columns = map[string][]string{
	"event_data":           {"created_at", "website_event_id"},
	"website_event":        {"created_at", "event_id", "session_id"},
	"revenue":              {"created_at"},
	"heatmap_event":        {"created_at"},
	"error_event":          {"created_at"},
	"session_replay":       {"created_at"},
	"session_replay_saved": {"created_at"},
	"session":              {"created_at", "session_id"},
	"session_data":         {"session_id"},
	"session_link":         {"session_id"},
}

// orphanWindow bounds the sweep for events whose session was deleted. such
// an event appears only when an erasure deletes a session while the replayer
// delivers one more event of that visitor with the old cache token
// (docs/compliance/erasure.md), so only recent rows are searched, through
// the created_at index.
const orphanWindow = 72 * time.Hour

// batch is the rows deleted per statement, so no single transaction holds
// a lock on a large part of a table.
const batch = 5000

// Config is what the job needs. the umami database lives on the platform's
// postgresql cluster, so host, port and tls mode are the
// PLATFORM_DB_* keys.
type Config struct {
	Host     string
	Port     int
	Database string
	SSLMode  string
	Password config.Secret
	Months   int
}

// Load reads PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_SSLMODE,
// UMAMI_DB_NAME, UMAMI_DB_JOBS_RETENTION_PASSWORD and
// WEB_RAW_RETENTION_MONTHS (default 25).
func Load(e *config.Env) (Config, error) {
	ret, _ := config.LoadWebRetention(e)
	c := Config{
		Host:     e.String("PLATFORM_DB_HOST"),
		Port:     e.Int("PLATFORM_DB_PORT", 1, 65535),
		SSLMode:  e.String("PLATFORM_DB_SSLMODE"),
		Database: e.String("UMAMI_DB_NAME"),
		Password: e.Secret("UMAMI_DB_JOBS_RETENTION_PASSWORD", 32),
		Months:   ret.Months,
	}
	return c, e.Err()
}

func quote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// ConnString is the keyword/value connection string of the job's role.
func (c Config) ConnString() string {
	return "host=" + quote(c.Host) + " port=" + strconv.Itoa(c.Port) + " dbname=" + quote(c.Database) +
		" sslmode=" + quote(c.SSLMode) + " user=" + Role + " password=" + quote(c.Password.Reveal()) +
		" application_name=jobs-retain-web"
}

// Cutoff is the start of the utc day months months before now's utc day.
// rows created before it are deleted. a day the target month does not have
// becomes that month's last day: time.Date would roll 2024-04-31 over to
// 2024-05-01 and delete one day too many.
func Cutoff(now time.Time, months int) time.Time {
	d := now.UTC()
	first := time.Date(d.Year(), d.Month()-time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d.Day(), last)-1)
}

// ErrUntested means the database is not the umami schema the job was
// tested with; nothing was deleted.
var ErrUntested = errors.New("retainweb: umami schema is not the tested one")

// CheckSchema compares _prisma_migrations and the used columns with what the
// job was tested against.
func CheckSchema(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx, `select migration_name, checksum, finished_at is not null, rolled_back_at is not null
		from _prisma_migrations order by migration_name`)
	if err != nil {
		return fmt.Errorf("retainweb: read _prisma_migrations: %w", err)
	}
	var got []Migration
	for rows.Next() {
		var m Migration
		var finished, rolledBack bool
		if err := rows.Scan(&m.Name, &m.Checksum, &finished, &rolledBack); err != nil {
			rows.Close()
			return fmt.Errorf("retainweb: read _prisma_migrations: %w", err)
		}
		if rolledBack {
			continue
		}
		if !finished {
			rows.Close()
			return fmt.Errorf("%w: migration %s is not finished", ErrUntested, m.Name)
		}
		got = append(got, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("retainweb: read _prisma_migrations: %w", err)
	}
	if !slices.Equal(got, TestedMigrations) {
		return fmt.Errorf("%w: %s", ErrUntested, diff(got))
	}
	for table, cols := range columns {
		var n int
		if err := conn.QueryRow(ctx, `select count(*) from information_schema.columns
			where table_schema = current_schema() and table_name = $1 and column_name = any($2)`, table, cols).Scan(&n); err != nil {
			return fmt.Errorf("retainweb: read columns of %s: %w", table, err)
		}
		if n != len(cols) {
			return fmt.Errorf("%w: table %s lacks one of the columns %v", ErrUntested, table, cols)
		}
	}
	return nil
}

func diff(got []Migration) string {
	want := map[Migration]bool{}
	for _, m := range TestedMigrations {
		want[m] = true
	}
	have := map[Migration]bool{}
	var extra, missing []string
	for _, m := range got {
		have[m] = true
		if !want[m] {
			extra = append(extra, m.Name)
		}
	}
	for _, m := range TestedMigrations {
		if !have[m] {
			missing = append(missing, m.Name)
		}
	}
	return fmt.Sprintf("untested or changed migrations %v, missing or changed %v", extra, missing)
}

// Result counts the rows deleted per table.
type Result struct {
	Cutoff  time.Time
	Deleted map[string]int64
}

// Run checks the schema, then deletes in batches: every per-event row
// created before cutoff, recent events whose session is gone, sessions
// created before cutoff that have no event left, and the session data of
// deleted sessions. each batch commits on its own, so a run that stops
// halfway leaves consistent tables and the next run continues.
func Run(ctx context.Context, conn *pgx.Conn, cutoff, now time.Time, log *slog.Logger) (Result, error) {
	res := Result{Cutoff: cutoff, Deleted: map[string]int64{}}
	if err := CheckSchema(ctx, conn); err != nil {
		return res, err
	}
	for _, t := range timeTables {
		if err := sweep(ctx, conn, &res, t, `created_at < $1`, cutoff); err != nil {
			return res, err
		}
	}
	since := now.Add(-orphanWindow)
	if err := sweep(ctx, conn, &res, "website_event",
		`created_at >= $1 and not exists (select 1 from session s where s.session_id = website_event.session_id)`, since); err != nil {
		return res, err
	}
	if err := sweep(ctx, conn, &res, "event_data",
		`created_at >= $1 and not exists (select 1 from website_event e where e.event_id = event_data.website_event_id)`, since); err != nil {
		return res, err
	}
	if err := sweep(ctx, conn, &res, "session",
		`created_at < $1 and not exists (select 1 from website_event e where e.session_id = session.session_id)`, cutoff); err != nil {
		return res, err
	}
	for _, t := range sessionTables {
		if err := sweep(ctx, conn, &res, t,
			`not exists (select 1 from session s where s.session_id = `+t+`.session_id)`); err != nil {
			return res, err
		}
	}
	log.Info("raw web retention", "cutoff", cutoff.Format(time.DateOnly), "deleted", res.Deleted)
	return res, nil
}

func sweep(ctx context.Context, conn *pgx.Conn, res *Result, table, cond string, args ...any) error {
	t := pgx.Identifier{table}.Sanitize()
	stmt := `delete from ` + t + ` where ctid = any(array(select ctid from ` + t + ` where ` + cond +
		` limit ` + strconv.Itoa(batch) + `))`
	for {
		tag, err := conn.Exec(ctx, stmt, args...)
		if err != nil {
			return fmt.Errorf("retainweb: delete from %s: %w", table, err)
		}
		res.Deleted[table] += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Provision creates the role when it is absent, sets its password and grants
// it exactly what the job needs in the umami database. admin is a superuser
// connection to that database, made after umami ran its migrations.
func Provision(ctx context.Context, admin *pgx.Conn, password string) error {
	return pgx.BeginFunc(ctx, admin, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from pg_roles where rolname = $1)`, Role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := tx.Exec(ctx, `create role `+Role+
				` nologin nosuperuser nocreatedb nocreaterole noinherit noreplication nobypassrls`); err != nil {
				return fmt.Errorf("retainweb: create role: %w", err)
			}
		}
		if err := pdb.SetLoginPassword(ctx, tx, Role, password); err != nil {
			return err
		}
		var dbname string
		if err := tx.QueryRow(ctx, `select current_database()`).Scan(&dbname); err != nil {
			return err
		}
		stmts := []string{
			`grant connect on database ` + pgx.Identifier{dbname}.Sanitize() + ` to ` + Role,
			`grant usage on schema public to ` + Role,
			`grant select on _prisma_migrations to ` + Role,
		}
		for _, t := range Tables {
			stmts = append(stmts, `grant select, delete on `+pgx.Identifier{t}.Sanitize()+` to `+Role)
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s); err != nil {
				return fmt.Errorf("retainweb: %s: %w", s, err)
			}
		}
		return nil
	})
}
