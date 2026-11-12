// Package audit writes cp.audit_log rows (control-plane-api 3.3). every
// mutation writes its row in the same transaction as the change, so a change
// without its audit row cannot be committed, and a failed audit write rolls
// the change back.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/db/tenantq"
)

// actions of contract 3.3 written by the tenants, members and sites routes
const (
	TenantCreate      = "tenant.create"
	TenantUpdate      = "tenant.update"
	TenantTermsAccept = "tenant.terms_accept"
	MemberAdd         = "member.add"
	MemberRoleChange  = "member.role_change"
	MemberRemove      = "member.remove"
	InviteCreate      = "invite.create"
	InviteRevoke      = "invite.revoke"
	SiteCreate        = "site.create"
	SiteUpdate        = "site.update"
	SiteDelete        = "site.delete"
)

// Entry is one audit row. Detail is encoded as a json object; it must never
// hold a token, cookie, password or secret.
type Entry struct {
	Actor  string
	Action string
	Target string
	Detail map[string]any
}

// User is the actor of a console action.
func User(id uuid.UUID) string { return "user:" + id.String() }

// TenantTarget, SiteTarget, UserTarget and InviteTarget name the object.
func TenantTarget(id uuid.UUID) string { return "tenant:" + id.String() }

// SiteTarget names a site.
func SiteTarget(id uuid.UUID) string { return "site:" + id.String() }

// UserTarget names a user.
func UserTarget(id uuid.UUID) string { return "user:" + id.String() }

// InviteTarget names an invite.
func InviteTarget(id uuid.UUID) string { return "invite:" + id.String() }

// Change is the detail value of an updated field.
func Change(old, new any) map[string]any { return map[string]any{"old": old, "new": new} }

func (e Entry) detail() ([]byte, error) {
	if e.Actor == "" || e.Action == "" || e.Target == "" {
		return nil, fmt.Errorf("audit: actor, action and target are required (%q, %q, %q)", e.Actor, e.Action, e.Target)
	}
	d := e.Detail
	if d == nil {
		d = map[string]any{}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("audit: detail of %s: %w", e.Action, err)
	}
	return b, nil
}

// InTenant appends e inside a db.WithTenant transaction (cp_tenant may
// insert into the audit log but not read it, migration 00007).
func InTenant(ctx context.Context, tx pgx.Tx, e Entry) error {
	d, err := e.detail()
	if err != nil {
		return err
	}
	if err := tenantq.New(tx).InsertTenantAudit(ctx, tenantq.InsertTenantAuditParams{
		Actor: e.Actor, Action: e.Action, Target: e.Target, Detail: d,
	}); err != nil {
		return fmt.Errorf("audit: %s: %w", e.Action, err)
	}
	return nil
}

// InSystem appends e inside a db.WithSystem transaction.
func InSystem(ctx context.Context, tx pgx.Tx, e Entry) error {
	d, err := e.detail()
	if err != nil {
		return err
	}
	if _, err := systemq.New(tx).InsertAuditLog(ctx, systemq.InsertAuditLogParams{
		Actor: e.Actor, Action: e.Action, Target: e.Target, Detail: d,
	}); err != nil {
		return fmt.Errorf("audit: %s: %w", e.Action, err)
	}
	return nil
}
