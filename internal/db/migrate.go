package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// MigrateResult says what a migration run did.
type MigrateResult struct {
	// Applied lists the versions applied by this run, in order.
	Applied []int64
	// Version is the database version after the run.
	Version int64
}

// Migrate brings the platform database to the newest migration in fsys and
// then checks row-level security coverage. it is safe to run on every
// deploy: it applies nothing when the database is current, and a session
// advisory lock keeps two nodes from migrating at once.
//
// it connects as the admin login, makes sure platform_owner exists and owns
// the database, and then runs every migration with set role platform_owner,
// so all objects (and goose's version table) are owned by platform_owner.
func Migrate(ctx context.Context, cfg AdminConfig, fsys fs.FS, log *slog.Logger) (MigrateResult, error) {
	var res MigrateResult
	if err := ensureOwner(ctx, cfg); err != nil {
		return res, err
	}

	cc, err := pgx.ParseConfig(cfg.Server.connString(cfg.Admin, "platformctl-migrate"))
	if err != nil {
		return res, errors.New("db: invalid admin connection settings")
	}
	sqlDB := stdlib.OpenDB(*cc, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "set role "+pgx.Identifier{OwnerRole}.Sanitize())
		return err
	}))
	defer func() { _ = sqlDB.Close() }()

	p, err := newProvider(sqlDB, fsys)
	if err != nil {
		return res, err
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			res.Applied = append(res.Applied, r.Source.Version)
			log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
		}
	}
	if err != nil {
		return res, fmt.Errorf("db: migrate: %w", err)
	}
	if res.Version, err = p.GetDBVersion(ctx); err != nil {
		return res, fmt.Errorf("db: read migration version: %w", err)
	}

	conn, err := pgx.Connect(ctx, cfg.Server.connString(cfg.Admin, "platformctl-migrate"))
	if err != nil {
		return res, fmt.Errorf("db: connect as admin: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	violations, err := CheckPolicyCoverage(ctx, conn)
	if err != nil {
		return res, err
	}
	if len(violations) > 0 {
		errs := make([]error, 0, len(violations))
		for _, v := range violations {
			errs = append(errs, v)
		}
		return res, fmt.Errorf("db: row-level security coverage check failed after migrating to version %d: %w",
			res.Version, errors.Join(errs...))
	}
	return res, nil
}

// newProvider builds the goose provider. the version table stays in public,
// where it exists before the first migration creates the cp schema.
func newProvider(sqlDB *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("db: migration lock: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("db: load migrations: %w", err)
	}
	return p, nil
}

// ensureOwner creates platform_owner on a fresh cluster and gives it the
// database. it refuses an existing platform_owner with superuser or
// bypassrls, because forced row-level security would not bind it.
func ensureOwner(ctx context.Context, cfg AdminConfig) error {
	conn, err := pgx.Connect(ctx, cfg.Server.connString(cfg.Admin, "platformctl-migrate"))
	if err != nil {
		return fmt.Errorf("db: connect as admin: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	owner := pgx.Identifier{OwnerRole}.Sanitize()
	var exists, super, bypass bool
	err = conn.QueryRow(ctx,
		`select count(*) > 0, coalesce(bool_or(rolsuper), false), coalesce(bool_or(rolbypassrls), false)
		 from pg_roles where rolname = $1`, OwnerRole).Scan(&exists, &super, &bypass)
	if err != nil {
		return fmt.Errorf("db: look up %s: %w", OwnerRole, err)
	}
	switch {
	case !exists:
		// createrole lets the first migration create the application roles
		if _, err := conn.Exec(ctx, "create role "+owner+
			" nologin createrole nosuperuser nocreatedb noreplication nobypassrls"); err != nil {
			return fmt.Errorf("db: create %s: %w", OwnerRole, err)
		}
	case super || bypass:
		return fmt.Errorf("db: role %s exists with superuser or bypassrls; remove those attributes first", OwnerRole)
	}

	var dbOwner string
	if err := conn.QueryRow(ctx,
		`select pg_get_userbyid(datdba) from pg_database where datname = current_database()`).Scan(&dbOwner); err != nil {
		return fmt.Errorf("db: read database owner: %w", err)
	}
	if dbOwner != OwnerRole {
		// owning the database lets the first migration grant connect, and
		// makes platform_owner the owner of the public schema as well
		if _, err := conn.Exec(ctx, "alter database "+pgx.Identifier{cfg.Server.Database}.Sanitize()+" owner to "+owner); err != nil {
			return fmt.Errorf("db: give database %s to %s: %w", cfg.Server.Database, OwnerRole, err)
		}
	}
	return nil
}
