package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RoleCPDemo is the role the operator views of the public demo run as
// (migration 00053). it never logs in: a cp_operator transaction of a demo
// request switches to it with set local role, and its policies keep the
// demo projects only. it is not in AppRoles, which are the login roles.
const RoleCPDemo Role = "cp_demo"

type demoKey struct{}

// WithDemo marks ctx as a request of the public demo. every transaction
// such a context starts is read only; WithOperator runs as cp_demo instead
// of cp_operator, and WithTenant refuses a tenant that is not a demo.
func WithDemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, demoKey{}, true)
}

// InDemo reports whether ctx is a request of the public demo.
func InDemo(ctx context.Context) bool {
	v, _ := ctx.Value(demoKey{}).(bool)
	return v
}

// ErrNotDemo is returned by WithTenant in a demo context for a tenant that
// is not a demo project, or not one any more.
var ErrNotDemo = errors.New("db: the tenant is not a demo project")

// demoOperator switches a cp_operator transaction of a demo request to
// cp_demo. app.demo tells the api functions that run with their owner's
// rights to keep demo tenants only (00053).
func demoOperator(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `set local role cp_demo`); err != nil {
		return fmt.Errorf("db: demo role: %w", err)
	}
	if _, err := tx.Exec(ctx, `select set_config('app.demo', 'on', true)`); err != nil {
		return fmt.Errorf("db: demo setting: %w", err)
	}
	return nil
}

// demoTenant checks, inside the cp_tenant transaction and under its
// row-level security, that the tenant is a live demo project. the control
// plane checked the same before, under cp_system; this keeps a tenant
// transaction of a demo request from ever reading another tenant even if
// that check were skipped.
func demoTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`select demo and status <> 'removed' from cp.tenants where id = $1`, tenantID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ok) {
		return ErrNotDemo
	}
	if err != nil {
		return fmt.Errorf("db: demo tenant: %w", err)
	}
	return nil
}
