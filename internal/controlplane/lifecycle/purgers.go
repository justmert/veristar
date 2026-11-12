package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// names of the this stage purgers, the keys of cp.tenant_removals.steps
const (
	PurgerRegistry  = "registry"
	PurgerUmami     = "umami"
	PurgerWebIngest = "web_ingest"
)

// Purgers returns the purgers of every store, in the order of contract 5.5:
// registry (ingestion stops), then umami, then cp.web_ingest_hourly. this stage
// inserts otlp_tokens after registry, phases 9 and 11 append api and agg.
func Purgers(reg *registry.Publisher, admin *umami.Admin) ([]Purger, error) {
	if reg == nil || admin == nil {
		return nil, errors.New("lifecycle: purgers need the registry publisher and the umami admin")
	}
	return []Purger{
		RegistryPurger{Registry: reg},
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
