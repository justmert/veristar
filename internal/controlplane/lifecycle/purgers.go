package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/justmert/stellar-analytics/internal/controlplane/otlptokens"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// names of the this stage purgers, the keys of cp.tenant_removals.steps
const (
	PurgerRegistry  = "registry"
	PurgerOTLP      = "otlp_tokens"
	PurgerUmami     = "umami"
	PurgerWebIngest = "web_ingest"
)

// Purgers returns the purgers of every store, in the order of contract 5.5:
// registry (web ingestion stops), otlp_tokens (api ingestion stops), then
// umami, then cp.web_ingest_hourly. phases 9 and 11 append api and agg.
func Purgers(reg *registry.Publisher, admin *umami.Admin) ([]Purger, error) {
	if reg == nil || admin == nil {
		return nil, errors.New("lifecycle: purgers need the registry publisher and the umami admin")
	}
	return []Purger{
		RegistryPurger{Registry: reg},
		OTLPPurger{Registry: reg},
		UmamiPurger{Admin: admin},
		WebIngestPurger{},
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

// WebIngestPurger deletes the tenant's cp.web_ingest_hourly rows, unless
// the removal keeps aggregates (contract 5.13).
type WebIngestPurger struct{}

// Name is the step key.
func (WebIngestPurger) Name() string { return PurgerWebIngest }

// Purge deletes in the run's transaction.
func (WebIngestPurger) Purge(ctx context.Context, r Removal) (map[string]any, error) {
	if r.KeepAggregates {
		return map[string]any{"skipped": "keep_aggregates"}, nil
	}
	n, err := systemq.New(r.Tx).PurgeWebIngest(ctx, r.TenantID)
	if err != nil {
		return nil, &RecordError{fmt.Errorf("web ingest rows: %w", err)}
	}
	return map[string]any{"rows_deleted": n}, nil
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
