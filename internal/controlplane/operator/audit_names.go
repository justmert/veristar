package operator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/identity"
)

// placeholders of an audit row's actor or target whose object no longer
// names anything: a deleted account keeps its emptied row (4.1.6), and the
// signed reports of a removed project are deleted with it. the strings are
// part of contract 5.9, so the console can match them.
const (
	DeletedAccountName = "Deleted account"
	DeletedProjectName = "Deleted project"
	DeletedSiteName    = "Deleted site"
	DeletedReportName  = "Deleted report"
)

// auditNames collects the ids of one audit page by kind
type auditNames struct {
	users   map[uuid.UUID]string
	tenants map[uuid.UUID]string
	sites   map[uuid.UUID]string
	reports map[string]string
}

// refID splits "kind:id" for the kinds whose display name the audit route
// resolves; any other kind, or an id that does not parse, gives false.
func refID(ref, kind string) (string, bool) {
	id, ok := strings.CutPrefix(ref, kind+":")
	return id, ok && id != ""
}

func uuidRef(ref, kind string) (uuid.UUID, bool) {
	s, ok := refID(ref, kind)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	return id, err == nil
}

// resolveAuditNames sets the actor and target display names of entries,
// with one query per kind of object. it runs under cp_system, which reads
// cp.users, cp.tenants, cp.sites and cp.usage_reports.
func resolveAuditNames(ctx context.Context, tx pgx.Tx, entries []AuditEntry) error {
	n := auditNames{users: map[uuid.UUID]string{}, tenants: map[uuid.UUID]string{}, sites: map[uuid.UUID]string{}, reports: map[string]string{}}
	for _, e := range entries {
		if id, ok := uuidRef(e.Actor, "user"); ok {
			n.users[id] = ""
		}
		if id, ok := uuidRef(e.Target, "user"); ok {
			n.users[id] = ""
		}
		if id, ok := uuidRef(e.Target, "tenant"); ok {
			n.tenants[id] = ""
		}
		if id, ok := uuidRef(e.Target, "site"); ok {
			n.sites[id] = ""
		}
		if id, ok := refID(e.Target, "report"); ok {
			n.reports[id] = ""
		}
	}
	if err := n.load(ctx, tx); err != nil {
		return err
	}
	for i := range entries {
		entries[i].ActorDisplayName = n.name(entries[i].Actor, true)
		entries[i].TargetDisplayName = n.name(entries[i].Target, false)
	}
	return nil
}

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (n auditNames) load(ctx context.Context, tx pgx.Tx) error {
	if len(n.users) > 0 {
		rows, err := tx.Query(ctx, `select id, github_login, stellar_address, passkey_address
			from cp.users where id = any($1) and deleted_at is null`, keys(n.users))
		if err != nil {
			return fmt.Errorf("operator: audit user names: %w", err)
		}
		var id uuid.UUID
		var login, stellar, passkey *string
		if _, err := pgx.ForEachRow(rows, []any{&id, &login, &stellar, &passkey}, func() error {
			n.users[id] = identity.DisplayName(login, stellar, passkey)
			return nil
		}); err != nil {
			return fmt.Errorf("operator: audit user names: %w", err)
		}
	}
	for _, table := range []struct {
		names map[uuid.UUID]string
		sql   string
	}{
		{n.tenants, `select id, name from cp.tenants where id = any($1)`},
		{n.sites, `select id, name from cp.sites where id = any($1)`},
	} {
		if len(table.names) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, table.sql, keys(table.names))
		if err != nil {
			return fmt.Errorf("operator: audit target names: %w", err)
		}
		var id uuid.UUID
		var name string
		if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error {
			table.names[id] = name
			return nil
		}); err != nil {
			return fmt.Errorf("operator: audit target names: %w", err)
		}
	}
	if len(n.reports) > 0 {
		rows, err := tx.Query(ctx, `select id, period_from, period_to from cp.usage_reports where id = any($1)`, keys(n.reports))
		if err != nil {
			return fmt.Errorf("operator: audit report names: %w", err)
		}
		var id string
		var from, to time.Time
		if _, err := pgx.ForEachRow(rows, []any{&id, &from, &to}, func() error {
			n.reports[id] = "Report " + from.Format(time.DateOnly) + " to " + to.Format(time.DateOnly)
			return nil
		}); err != nil {
			return fmt.Errorf("operator: audit report names: %w", err)
		}
	}
	return nil
}

// name is the display name of an actor or target reference, the
// placeholder of its kind when the object is gone, and nil for a kind the
// route does not resolve (system tasks, credentials, operator views).
func (n auditNames) name(ref string, actor bool) *string {
	pick := func(name, placeholder string) *string {
		if name == "" {
			name = placeholder
		}
		return &name
	}
	if id, ok := uuidRef(ref, "user"); ok {
		return pick(n.users[id], DeletedAccountName)
	}
	if actor {
		return nil
	}
	if id, ok := uuidRef(ref, "tenant"); ok {
		return pick(n.tenants[id], DeletedProjectName)
	}
	if id, ok := uuidRef(ref, "site"); ok {
		return pick(n.sites[id], DeletedSiteName)
	}
	if id, ok := refID(ref, "report"); ok {
		return pick(n.reports[id], DeletedReportName)
	}
	return nil
}
