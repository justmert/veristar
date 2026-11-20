package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB holds one pool per role a service uses.
type DB struct {
	pools map[Role]*pgxpool.Pool
}

// Open connects one pool per role in cfg. appName shows up in
// pg_stat_activity, so an operator can tell services apart.
func Open(ctx context.Context, cfg PoolConfig, appName string) (*DB, error) {
	d := &DB{pools: make(map[Role]*pgxpool.Pool, len(cfg.Roles))}
	for _, l := range cfg.Roles {
		p, err := openPool(ctx, cfg.Server, l, appName, cfg.MaxConns[Role(l.User)])
		if err != nil {
			d.Close()
			return nil, err
		}
		d.pools[Role(l.User)] = p
	}
	return d, nil
}

func openPool(ctx context.Context, s Server, l Login, appName string, maxConns int32) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(s.connString(l, appName))
	if err != nil {
		// the parse error can quote the connection string, password included
		return nil, fmt.Errorf("db: invalid connection settings for role %s", l.User)
	}
	if maxConns < 0 {
		return nil, fmt.Errorf("db: pool size for role %s must not be negative", l.User)
	}
	if maxConns > 0 {
		pc.MaxConns = maxConns
	}
	role := l.User
	pc.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		return checkSessionRole(ctx, c, role)
	}
	p, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("db: pool for role %s: %w", role, err)
	}
	// connect once now, so a wrong password or a missing role stops the
	// service at startup instead of at its first request
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("db: connect as role %s: %w", role, err)
	}
	return p, nil
}

// checkSessionRole refuses a connection that is not the expected role, or
// that could skip row-level security. a misconfigured login (for example the
// admin password in a service's env) fails here rather than quietly reading
// every tenant's rows.
func checkSessionRole(ctx context.Context, c *pgx.Conn, want string) error {
	var user string
	var super, bypass bool
	err := c.QueryRow(ctx,
		`select current_user, r.rolsuper, r.rolbypassrls from pg_roles r where r.rolname = current_user`,
	).Scan(&user, &super, &bypass)
	if err != nil {
		return fmt.Errorf("db: check session role: %w", err)
	}
	if user != want || super || bypass {
		return fmt.Errorf("db: connected as %q (superuser %t, bypassrls %t), want plain role %q", user, super, bypass, want)
	}
	return nil
}

// Close closes every pool.
func (d *DB) Close() {
	for _, p := range d.pools {
		p.Close()
	}
}

// Pool returns the pool of a role, for code that manages its own
// transactions (the nats fencing helpers, for example).
func (d *DB) Pool(r Role) (*pgxpool.Pool, error) {
	p, ok := d.pools[r]
	if !ok {
		return nil, fmt.Errorf("db: no pool for role %s; load it with LoadPoolConfig", r)
	}
	return p, nil
}

// ErrNoTenant is returned by WithTenant for the zero tenant id.
var ErrNoTenant = errors.New("db: tenant id is required")

// WithTenant runs fn in a cp_tenant transaction with app.tenant_id set for
// that transaction only. row-level security then limits every statement to
// the tenant. the setting ends with the transaction, so a pooled connection
// never carries a tenant into the next caller's work.
func (d *DB) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return ErrNoTenant
	}
	return d.inTx(ctx, RoleCPTenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return fmt.Errorf("db: set tenant: %w", err)
		}
		if InDemo(ctx) {
			if err := demoTenant(ctx, tx, tenantID); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

// WithSystem runs fn in a cp_system transaction: control plane logic that is
// not scoped to one tenant (sign-in, provisioning, operator actions).
func (d *DB) WithSystem(ctx context.Context, fn func(pgx.Tx) error) error {
	return d.inTx(ctx, RoleCPSystem, fn)
}

// WithOperator runs fn in a cp_operator transaction: operator views and the
// machine metrics api.
// in a demo context the transaction runs as cp_demo (demo.go).
func (d *DB) WithOperator(ctx context.Context, fn func(pgx.Tx) error) error {
	if InDemo(ctx) {
		return d.inTx(ctx, RoleCPOperator, func(tx pgx.Tx) error {
			if err := demoOperator(ctx, tx); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	return d.inTx(ctx, RoleCPOperator, fn)
}

// WithRole runs fn in a transaction of metrics_writer or jobs. tenant and
// control plane work must use WithTenant, WithSystem or WithOperator.
func (d *DB) WithRole(ctx context.Context, r Role, fn func(pgx.Tx) error) error {
	if r != RoleMetricsWriter && r != RoleJobs {
		return fmt.Errorf("db: WithRole is for metrics_writer and jobs, not %s", r)
	}
	return d.inTx(ctx, r, fn)
}

func (d *DB) inTx(ctx context.Context, r Role, fn func(pgx.Tx) error) error {
	p, err := d.Pool(r)
	if err != nil {
		return err
	}
	if InDemo(ctx) {
		// read only from the start, so no statement of a demo request can
		// write, whatever its handler does
		return pgx.BeginTxFunc(ctx, p, pgx.TxOptions{AccessMode: pgx.ReadOnly}, fn)
	}
	return pgx.BeginFunc(ctx, p, fn)
}
