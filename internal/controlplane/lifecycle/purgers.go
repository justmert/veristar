package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// names of the purgers, the keys of cp.tenant_removals.steps
const (
	PurgerRegistry  = "registry"
	PurgerOTLP      = "otlp_tokens"
	PurgerUmami     = "umami"
	PurgerWebIngest = "web_ingest"
	// PurgerAPI is the purger of the api schema
	PurgerAPI = "api"
	// PurgerAgg is the purger of the agg schema
	PurgerAgg = "agg"
	// PurgerChain is the purger of the on-chain read state and counts
	PurgerChain = "chain"
)

// Purgers returns the purgers of every store, in the order of contract 5.5:
// registry (web ingestion stops), otlp_tokens (api ingestion stops), then
// umami, then cp.web_ingest_hourly, then the api schema, then the agg
// schema, then the on-chain rows.
func Purgers(reg *registry.Publisher, admin *umami.Admin) ([]Purger, error) {
	if reg == nil || admin == nil {
		return nil, errors.New("lifecycle: purgers need the registry publisher and the umami admin")
	}
	return []Purger{
		RegistryPurger{Registry: reg},
		OTLPPurger{Registry: reg},
		UmamiPurger{Admin: admin},
		WebIngestPurger{},
		APIPurger{},
		AggPurger{},
		ChainPurger{},
	}, nil
}

// RegistryPurger deletes the registry_sites key of every site, so the
// gateway refuses the tenant's events from then on.
type RegistryPurger struct{ Registry *registry.Publisher }

// Name is the step key.
func (RegistryPurger) Name() string { return PurgerRegistry }

// Purge deletes the keys that exist. an unreadable value is deleted too.
func (p RegistryPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	deleted := []string{}
	for _, s := range r.Sites {
		id := s.ID.String()
		_, ok, err := p.Registry.GetSite(ctx, id)
		if err != nil && !errors.Is(err, registry.ErrUnreadable) {
			return nil, err
		}
		if !ok && err == nil {
			continue
		}
		if err := p.Registry.DeleteSite(ctx, id); err != nil {
			return nil, err
		}
		deleted = append(deleted, id)
	}
	return map[string]any{"sites_deleted": deleted}, nil
}

// UmamiPurger deletes both websites of every site with their data, the
// tenant's team, and the umami users of members who belong to no other
// tenant that is not removed. it always runs: raw web data is never kept
// (contract 5.13).
type UmamiPurger struct{ Admin *umami.Admin }

// Name is the step key.
func (UmamiPurger) Name() string { return PurgerUmami }

// Purge looks before every delete, because umami answers 500 for a delete
// of an unknown id, which makes the purge idempotent.
func (p UmamiPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	team := ""
	if r.UmamiTeamID.Valid {
		team = r.UmamiTeamID.UUID.String()
	}
	websites := []string{}
	for _, s := range r.Sites {
		ids := []string{s.ID.String()}
		if s.QuarantineWebsiteID.Valid {
			ids = append(ids, s.QuarantineWebsiteID.UUID.String())
		}
		for _, id := range ids {
			w, ok, err := p.Admin.GetWebsite(ctx, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if w.TeamID != team {
				// a website of another team under this id is not the
				// tenant's data; a person must look at the clash
				return nil, fmt.Errorf("umami website %s belongs to team %q, not the tenant's team %q", id, w.TeamID, team)
			}
			if err := p.Admin.DeleteWebsite(ctx, id); err != nil {
				return nil, err
			}
			websites = append(websites, id)
		}
	}

	teamDeleted := false
	if team != "" {
		_, ok, err := p.Admin.GetTeam(ctx, team)
		if err != nil {
			return nil, err
		}
		if ok {
			if err := p.Admin.DeleteTeam(ctx, team); err != nil {
				return nil, err
			}
			teamDeleted = true
		}
	}

	orphans, err := systemq.New(r.Tx).PurgeOrphanedUsers(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("orphaned users: %w", err)}
	}
	users := []string{}
	for _, uid := range orphans {
		id := uid.String()
		_, ok, err := p.Admin.GetUser(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		// deleting the user also ends every umami token it holds
		if err := p.Admin.DeleteUser(ctx, id); err != nil {
			return nil, err
		}
		users = append(users, id)
	}
	return map[string]any{"websites_deleted": websites, "team_deleted": teamDeleted, "users_deleted": users}, nil
}

// WebIngestPurger deletes the tenant's cp.web_ingest_hourly and
// cp.web_evidence_daily rows, unless the removal keeps aggregates (contract
// 5.13). the network keys of cp.web_networks_daily are no aggregate, only
// the input of a distinct count, so they go in either case.
type WebIngestPurger struct{}

// Name is the step key.
func (WebIngestPurger) Name() string { return PurgerWebIngest }

// Purge deletes in the run's transaction.
func (WebIngestPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	q := systemq.New(r.Tx)
	nets, err := q.PurgeWebNetworks(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("web network rows: %w", err)}
	}
	if r.KeepAggregates {
		return map[string]any{"skipped": "keep_aggregates", "network_rows_deleted": nets}, nil
	}
	n, err := q.PurgeWebIngest(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("web ingest rows: %w", err)}
	}
	ev, err := q.PurgeWebEvidence(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("web evidence rows: %w", err)}
	}
	return map[string]any{"rows_deleted": n, "evidence_rows_deleted": ev, "network_rows_deleted": nets}, nil
}

// OTLPPurger revokes every otlp token of the tenant in cp.otlp_tokens and
// flips each registry_otlp key to revoked, so the collector refuses the
// tenant's exports before any of its data is deleted. revocation is final:
// the tokens stay revoked even if the tenant row were ever restored.
type OTLPPurger struct{ Registry *registry.Publisher }

// Name is the step key.
func (OTLPPurger) Name() string { return PurgerOTLP }

// Purge revokes the rows in the run's transaction first, then writes the
// keys. a failed key write leaves the rows revoked; the next run finds no
// active row and writes the keys from the revoked rows.
func (p OTLPPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	q := systemq.New(r.Tx)
	revoked, err := q.PurgeRevokeOTLPTokens(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("revoke otlp tokens: %w", err)}
	}
	rows, err := q.ProvisionOTLPTokens(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("otlp tokens: %w", err)}
	}
	written := []string{}
	for _, t := range rows {
		want, err := otlptokens.Entry(r.TenantID, t.ID, t.Hash, t.Status, StatusRemoved)
		if err != nil {
			return nil, err
		}
		wrote, err := p.Registry.SyncOTLPToken(ctx, want)
		if err != nil {
			return nil, err
		}
		if wrote {
			written = append(written, t.ID.String())
		}
	}
	ids := make([]string, len(revoked))
	for i, id := range revoked {
		ids[i] = id.String()
	}
	return map[string]any{"tokens_revoked": ids, "keys_revoked": written}, nil
}

// APIPurger deletes the tenant's rows in the api schema
// through api.purge_tenant, the one api right cp_system has. minute rows and
// the writer's per-tenant state always go; hourly, daily and warning rows
// stay when the removal keeps aggregates (contract 5.13). it runs after
// otlp_tokens, so the collector already refuses the tenant's exports.
type APIPurger struct{}

// Name is the step key.
func (APIPurger) Name() string { return PurgerAPI }

// Purge deletes in the run's transaction, so the rows go with the step
// record or not at all.
func (APIPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	rows, err := systemq.New(r.Tx).PurgeAPITenant(ctx, systemq.PurgeAPITenantParams{
		TenantID: r.TenantID, KeepAggregates: r.KeepAggregates,
	})
	if err != nil {
		return nil, &RecordError{fmt.Errorf("api rows: %w", err)}
	}
	deleted := map[string]int64{}
	for _, row := range rows {
		deleted[row.TableName] = row.RowsDeleted
	}
	detail := map[string]any{"rows_deleted": deleted}
	if r.KeepAggregates {
		detail["kept"] = []string{"metrics_hourly", "metrics_daily", "ingest_warnings"}
	}
	return detail, nil
}

// AggPurger deletes the tenant's agg rows through
// agg.purge_tenant, the one agg right cp_system has. every agg row is an
// aggregate, so a removal that keeps aggregates keeps all of them (contract
// 5.13). it runs after umami: the registry step already stopped the
// aggregation task from reading the tenant's sites, and the function waits
// for a write of that task still in flight.
type AggPurger struct{}

// Name is the step key.
func (AggPurger) Name() string { return PurgerAgg }

// Purge deletes in the run's transaction, so the rows go with the step
// record or not at all.
func (AggPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	if r.KeepAggregates {
		return map[string]any{"skipped": "keep_aggregates"}, nil
	}
	rows, err := systemq.New(r.Tx).PurgeAggTenant(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("agg rows: %w", err)}
	}
	deleted := map[string]int64{}
	for _, row := range rows {
		deleted[row.TableName] = row.RowsDeleted
	}
	return map[string]any{"rows_deleted": deleted}, nil
}

// ChainPurger deletes the read state of the tenant's ledger ids, and the
// daily counts unless keep_aggregates. the ledger data itself is public;
// what goes is the platform's record of which project declared it.
type ChainPurger struct{}

// Name is the step key.
func (ChainPurger) Name() string { return PurgerChain }

// Purge deletes in the run's transaction through agg.purge_chain.
func (ChainPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	rows, err := r.Tx.Query(ctx, `select table_name, rows_deleted from agg.purge_chain($1, $2)`, r.TenantID, r.KeepAggregates)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("chain rows: %w", err)}
	}
	defer rows.Close()
	deleted := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, &RecordError{fmt.Errorf("chain rows: %w", err)}
		}
		deleted[name] = n
	}
	if err := rows.Err(); err != nil {
		return nil, &RecordError{fmt.Errorf("chain rows: %w", err)}
	}
	return map[string]any{"rows_deleted": deleted, "keep_aggregates": r.KeepAggregates}, nil
}
