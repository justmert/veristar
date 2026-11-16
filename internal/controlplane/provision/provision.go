// Package provision keeps umami and the site registry in step with the cp
// tables. it is a reconciler, not a set of inline
// calls: the desired state is what cp.tenants, cp.tenant_members, cp.users
// and cp.sites say, the actual state is umami (one team per tenant, a primary
// and a quarantine website per site, one view-only user per console user,
// their team memberships) and the registry_sites bucket. Reconcile compares
// the two for one tenant and changes only what differs, so running it again
// changes nothing, and anything removed or changed directly in umami is put
// back on the next run.
//
// the control plane calls Reconcile after every change (tenants.Reconciler),
// and the jobs task reconcile runs ReconcileAll every 10 minutes.
package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// audit actions (docs/reference/control-plane-api.md section 3.3)
const (
	ActionTeamCreate       = "provision.team_create"
	ActionWebsiteCreate    = "provision.website_create"
	ActionWebsiteUpdate    = "provision.website_update"
	ActionWebsiteDelete    = "provision.website_delete"
	ActionUserCreate       = "provision.user_create"
	ActionUserUpdate       = "provision.user_update"
	ActionMembershipAdd    = "provision.membership_add"
	ActionMembershipRemove = "provision.membership_remove"
	ActionRegistryPut      = "provision.registry_put"
	ActionRegistryDelete   = "provision.registry_delete"
	// a registry_otlp entry written to match the cp tables
	ActionOTLPRegistryPut = "provision.otlp_registry_put"
)

// Actor is the audit actor of every change the reconciler makes, whoever
// triggered the run.
const Actor = "system:reconcile"

// tenant and site statuses of the cp tables
const (
	statusActive   = "active"
	statusPending  = "pending"
	statusRejected = "rejected"
	statusRemoved  = "removed"
)

// TxFunc runs fn in one cp_system transaction. the control plane passes
// db.WithSystem; the jobs task passes a transaction that also advances the
// jobs fence, so a former leader cannot commit.
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Reconciler holds what a run needs. all fields are required.
type Reconciler struct {
	Admin    *umami.Admin
	Registry *registry.Publisher
	Key      umami.PasswordKey
	Tx       TxFunc
	Now      func() time.Time
	Log      *slog.Logger
}

func (r *Reconciler) check() error {
	if r.Admin == nil || r.Registry == nil || r.Key.Empty() || r.Tx == nil || r.Now == nil || r.Log == nil {
		return errors.New("provision: reconciler needs Admin, Registry, Key, Tx, Now and Log")
	}
	return nil
}

// Change is one change a run made, as written to cp.audit_log.
type Change struct {
	Action string
	Target string
}

// TeamName is the umami team of a tenant. the tenant name can change and
// umami allows 50 characters, so the id is used (38 characters).
func TeamName(tenantID uuid.UUID) string { return "t-" + tenantID.String() }

// Username is the name the reconciler gives the umami user of a console
// account it creates. accounts whose umami user was made before migration
// 00034 keep the gh-<github id> name stored in cp.users.umami_username.
func Username(userID uuid.UUID) string { return "u-" + userID.String() }

// IsPlatformUsername reports whether an umami user name is one the platform
// gives console accounts: u-<account id>, or gh-<github id> from before
// migration 00034. the admin service account and anything made outside the
// platform carry neither prefix.
func IsPlatformUsername(name string) bool {
	return strings.HasPrefix(name, "u-") || strings.HasPrefix(name, "gh-")
}

// umami limits website names to 100 characters
const websiteNameMax = 100

func websiteName(name string, quarantine bool) string {
	if quarantine {
		name = "Quarantine: " + name
	}
	if utf8.RuneCountInString(name) <= websiteNameMax {
		return name
	}
	r := []rune(name)
	return string(r[:websiteNameMax])
}

// run is the state of one tenant's reconcile.
type run struct {
	r       *Reconciler
	q       *systemq.Queries
	tx      pgx.Tx
	tenant  systemq.ProvisionLockTenantRow
	adminID string
	changes []Change
	// scales are the sites' typical daily visitors as registry scales
	// (integrity.ScaleOf of the integrity task's baseline)
	scales map[uuid.UUID]int
}

// record writes one audit row in the run's transaction.
func (s *run) record(ctx context.Context, action, target string, detail map[string]any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if _, err := s.q.InsertAuditLog(ctx, systemq.InsertAuditLogParams{
		Actor: Actor, Action: action, Target: target, Detail: b,
	}); err != nil {
		return fmt.Errorf("provision: audit %s: %w", action, err)
	}
	s.changes = append(s.changes, Change{Action: action, Target: target})
	return nil
}

// ErrTenantNotFound is returned by Reconcile for an id with no tenant row.
var ErrTenantNotFound = errors.New("provision: tenant not found")

// Reconcile is Changes without the list of changes: the entry point of the
// control plane routes (tenants.Reconciler).
func (r *Reconciler) Reconcile(ctx context.Context, tenantID uuid.UUID) error {
	_, err := r.Changes(ctx, tenantID)
	return err
}

// Changes brings umami and the registry in line with the cp tables for
// one tenant and returns the changes it made. a run holds the tenant row
// lock for its whole length, so two runs for one tenant (the control plane
// and the jobs task) never interleave.
//
// changes made before an error are kept: the audit rows and the stored team
// id commit with the error returned afterwards, because the umami side of
// those changes has already happened.
func (r *Reconciler) Changes(ctx context.Context, tenantID uuid.UUID) ([]Change, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	var changes []Change
	var runErr error
	err := r.Tx(ctx, func(tx pgx.Tx) error {
		s := &run{r: r, q: systemq.New(tx), tx: tx}
		t, err := s.q.ProvisionLockTenant(ctx, tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
		}
		if err != nil {
			return fmt.Errorf("provision: lock tenant: %w", err)
		}
		s.tenant = t
		runErr = s.reconcile(ctx)
		changes = s.changes
		// a failed audit insert leaves the transaction unusable; only then
		// is there nothing to commit
		var auditErr *auditError
		if errors.As(runErr, &auditErr) {
			return runErr
		}
		return nil
	})
	if err != nil {
		return changes, err
	}
	return changes, runErr
}

type auditError struct{ err error }

func (e *auditError) Error() string { return e.err.Error() }
func (e *auditError) Unwrap() error { return e.err }

func (s *run) audit(ctx context.Context, action, target string, detail map[string]any) error {
	if err := s.record(ctx, action, target, detail); err != nil {
		return &auditError{err: err}
	}
	return nil
}

func (s *run) reconcile(ctx context.Context) error {
	members, err := s.q.ProvisionMembers(ctx, s.tenant.ID)
	if err != nil {
		return fmt.Errorf("provision: members: %w", err)
	}
	sites, err := s.q.ProvisionSites(ctx, s.tenant.ID)
	if err != nil {
		return fmt.Errorf("provision: sites: %w", err)
	}

	if s.tenant.Status == statusRemoved {
		if err := s.removedTenant(ctx); err != nil {
			return err
		}
		// the purge revoked every row; this keeps the keys revoked if the
		// bucket was ever rebuilt
		return s.otlpTokens(ctx)
	}
	// tokens first and without umami: a suspension must reach the collector
	// even while umami is unreachable
	if err := s.otlpTokens(ctx); err != nil {
		return err
	}
	if s.tenant.Status == statusPending || s.tenant.Status == statusRejected {
		return s.unapprovedTenant(ctx, sites)
	}

	self, err := s.r.Admin.Self(ctx)
	if err != nil {
		return err
	}
	s.adminID = self.ID
	teamID, err := s.team(ctx)
	if err != nil {
		return err
	}
	if err := s.users(ctx, members); err != nil {
		return err
	}
	if err := s.memberships(ctx, teamID, members); err != nil {
		return err
	}
	baselines, err := s.q.ProvisionSiteBaselines(ctx, s.tenant.ID)
	if err != nil {
		return fmt.Errorf("provision: site baselines: %w", err)
	}
	s.scales = make(map[uuid.UUID]int, len(baselines))
	for _, b := range baselines {
		s.scales[b.SiteID] = integrity.ScaleOf(b.BaselineVisitors)
	}
	for _, site := range sites {
		if err := s.site(ctx, teamID, site); err != nil {
			return err
		}
	}
	return nil
}

// removedTenant runs the removal purge (contract 5.5) in
// the run's transaction, which holds the tenant row lock: the registry keys,
// the umami websites, team and orphaned users, and the ingest aggregates,
// store by store. a store that fails is retried on the next run; stores
// already done are skipped.
func (s *run) removedTenant(ctx context.Context) error {
	purgers, err := lifecycle.Purgers(s.r.Registry, s.r.Admin)
	if err != nil {
		return err
	}
	runner, err := lifecycle.NewRunner(purgers...)
	if err != nil {
		return err
	}
	err = runner.Run(ctx, s.tx, s.tenant.ID, s.tenant.UmamiTeamID)
	var rec *lifecycle.RecordError
	if errors.As(err, &rec) {
		// the transaction is unusable; nothing of this run may commit
		return &auditError{err: err}
	}
	return err
}

// unapprovedTenant handles a pending or rejected tenant (contract 5.7): no
// operator has approved it, so nothing is created in umami (no team, users,
// memberships or websites) and nothing is published to the registry, and the
// gateway answers rejected_unknown_site for its sites. a registry key that
// exists anyway is deleted, so an unapproved tenant never takes traffic.
// umami objects that exist are left alone: approval reconciles the tenant
// again and adopts them.
func (s *run) unapprovedTenant(ctx context.Context, sites []systemq.ProvisionSitesRow) error {
	for _, site := range sites {
		if err := s.registryDelete(ctx, site.ID); err != nil {
			return err
		}
	}
	return nil
}

// team returns the tenant's umami team, creating it when it does not exist.
// a team made by an earlier run whose transaction did not commit is found by
// its name and adopted, so a crash never leaves a second team behind.
func (s *run) team(ctx context.Context) (string, error) {
	if s.tenant.UmamiTeamID.Valid {
		id := s.tenant.UmamiTeamID.UUID.String()
		_, ok, err := s.r.Admin.GetTeam(ctx, id)
		if err != nil {
			return "", err
		}
		if ok {
			return id, nil
		}
		s.r.Log.Warn("umami team of tenant is gone, creating it again", "tenant_id", s.tenant.ID.String(), "team_id", id)
	}
	name := TeamName(s.tenant.ID)
	found, err := s.r.Admin.FindTeamsByName(ctx, name)
	if err != nil {
		return "", err
	}
	var teamID string
	adopted := false
	switch len(found) {
	case 0:
		// an empty owner makes the admin service account the owner
		t, _, err := s.r.Admin.CreateTeam(ctx, name, "")
		if err != nil {
			return "", err
		}
		teamID = t.ID
	case 1:
		teamID = found[0].ID
		adopted = true
	default:
		return "", fmt.Errorf("provision: %d umami teams are named %s; remove the extra ones by hand", len(found), name)
	}
	id, err := uuid.Parse(teamID)
	if err != nil {
		return "", fmt.Errorf("provision: umami team id %q is not a uuid", teamID)
	}
	if err := s.q.ProvisionSetTeam(ctx, systemq.ProvisionSetTeamParams{
		UmamiTeamID: uuid.NullUUID{UUID: id, Valid: true}, ID: s.tenant.ID,
	}); err != nil {
		return "", &auditError{err: fmt.Errorf("provision: store team id: %w", err)}
	}
	return teamID, s.audit(ctx, ActionTeamCreate, "tenant:"+s.tenant.ID.String(),
		map[string]any{"team_id": teamID, "adopted": adopted})
}

// users makes sure every member has an umami user: id equal to cp.users.id,
// name cp.users.umami_username (assigned here as Username(id) when the
// account has none yet), global role view-only, and the current day's derived
// password set once at creation. an existing user's password is
// never touched here; only the umami-rotate task changes passwords.
func (s *run) users(ctx context.Context, members []systemq.ProvisionMembersRow) error {
	for _, m := range members {
		id := m.ID.String()
		var want string
		if m.UmamiUsername != nil {
			want = *m.UmamiUsername
		} else {
			want = Username(m.ID)
			// stored before the umami call, so member sessions read the name the user is
			// created with. a failed create leaves it stored, which is
			// harmless: the next run creates the user under the same name.
			// a failed statement leaves the transaction unusable, so it is
			// returned as an auditError, which commits nothing
			if err := s.q.ProvisionSetUmamiUsername(ctx, systemq.ProvisionSetUmamiUsernameParams{
				UmamiUsername: &want, ID: m.ID,
			}); err != nil {
				return &auditError{err: fmt.Errorf("provision: store umami username: %w", err)}
			}
		}
		u, ok, err := s.r.Admin.GetUser(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			pw, err := s.r.Key.Password(id, umami.EpochDay(s.r.Now()))
			if err != nil {
				return err
			}
			if _, err := s.r.Admin.CreateUser(ctx, umami.NewUser{
				ID: id, Username: want, Password: pw, Role: umami.RoleViewOnly,
			}); err != nil {
				return err
			}
			if err := s.audit(ctx, ActionUserCreate, "user:"+id, map[string]any{"username": want}); err != nil {
				return err
			}
			continue
		}
		if u.Username != want {
			// the stored name never changes, so another name under this id
			// was made outside the platform
			return fmt.Errorf("provision: umami user %s is named %q, want %q", id, u.Username, want)
		}
		if u.Role != umami.RoleViewOnly {
			if _, err := s.r.Admin.UpdateUser(ctx, id, umami.UserUpdate{Role: umami.RoleViewOnly}); err != nil {
				return err
			}
			if err := s.audit(ctx, ActionUserUpdate, "user:"+id,
				map[string]any{"role": map[string]string{"old": u.Role, "new": umami.RoleViewOnly}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// memberships makes the team's members exactly the tenant's members, each
// with team-view-only, plus the admin's own owner membership.
func (s *run) memberships(ctx context.Context, teamID string, members []systemq.ProvisionMembersRow) error {
	team, ok, err := s.r.Admin.GetTeam(ctx, teamID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("provision: umami team %s disappeared during reconcile", teamID)
	}
	want := map[string]bool{}
	for _, m := range members {
		want[m.ID.String()] = true
	}
	have := map[string]bool{}
	for _, m := range team.Members {
		if m.UserID == s.adminID {
			continue
		}
		if want[m.UserID] && m.Role == umami.TeamRoleViewOnly {
			have[m.UserID] = true
			continue
		}
		reason := "not a tenant member"
		if want[m.UserID] {
			// a member with more rights than view-only is taken out and
			// added again with the right role
			reason = "role " + m.Role
		}
		if err := s.removeMember(ctx, teamID, m, reason); err != nil {
			return err
		}
	}
	for _, m := range members {
		id := m.ID.String()
		if have[id] {
			continue
		}
		if _, err := s.r.Admin.AddTeamUser(ctx, teamID, id, umami.TeamRoleViewOnly); err != nil {
			return err
		}
		if err := s.audit(ctx, ActionMembershipAdd, "user:"+id,
			map[string]any{"tenant_id": s.tenant.ID.String(), "team_id": teamID, "role": umami.TeamRoleViewOnly}); err != nil {
			return err
		}
	}
	return nil
}

func (s *run) removeMember(ctx context.Context, teamID string, m umami.TeamUser, reason string) error {
	if err := s.r.Admin.RemoveTeamUser(ctx, teamID, m.UserID); err != nil {
		return err
	}
	return s.audit(ctx, ActionMembershipRemove, "user:"+m.UserID,
		map[string]any{"tenant_id": s.tenant.ID.String(), "team_id": teamID, "role": m.Role, "reason": reason})
}

// site reconciles one site's two websites and its registry entry.
func (s *run) site(ctx context.Context, teamID string, site systemq.ProvisionSitesRow) error {
	if !site.QuarantineWebsiteID.Valid {
		return fmt.Errorf("provision: site %s has no quarantine website id", site.ID)
	}
	ids := []struct {
		id         string
		quarantine bool
	}{{site.ID.String(), false}, {site.QuarantineWebsiteID.UUID.String(), true}}

	if site.Status == statusRemoved {
		// the registry key goes first, so the gateway stops accepting
		// events before the websites they would be replayed into are gone
		if err := s.registryDelete(ctx, site.ID); err != nil {
			return err
		}
		for _, w := range ids {
			if err := s.websiteDelete(ctx, teamID, site.ID, w.id); err != nil {
				return err
			}
		}
		return nil
	}
	if site.Status == statusPending {
		// a site in review (contract 5.6) is treated like a pending tenant:
		// no websites, no registry entry until an operator approves it
		return s.registryDelete(ctx, site.ID)
	}

	for _, w := range ids {
		if err := s.website(ctx, teamID, site, w.id, websiteName(site.Name, w.quarantine)); err != nil {
			return err
		}
	}
	// the registry entry only after both websites exist, so the replayer
	// can deliver every event the gateway accepts
	return s.registryPut(ctx, site)
}

func (s *run) website(ctx context.Context, teamID string, site systemq.ProvisionSitesRow, id, name string) error {
	target := "site:" + site.ID.String()
	w, ok, err := s.r.Admin.GetWebsite(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.r.Admin.CreateWebsite(ctx, umami.NewWebsite{ID: id, Name: name, Domain: site.Domain, TeamID: teamID}); err != nil {
			return err
		}
		return s.audit(ctx, ActionWebsiteCreate, target, map[string]any{"website_id": id, "team_id": teamID})
	}
	if w.TeamID != teamID {
		// moving a website between teams would hand its data to other
		// people; an id clash like this needs a person to look at it
		return fmt.Errorf("provision: umami website %s belongs to team %q, not the tenant's team %s", id, w.TeamID, teamID)
	}
	if w.Name == name && w.Domain == site.Domain {
		return nil
	}
	if _, err := s.r.Admin.UpdateWebsite(ctx, id, umami.WebsiteUpdate{Name: name, Domain: site.Domain}); err != nil {
		return err
	}
	return s.audit(ctx, ActionWebsiteUpdate, target, map[string]any{
		"website_id": id,
		"name":       map[string]string{"old": w.Name, "new": name},
		"domain":     map[string]string{"old": w.Domain, "new": site.Domain},
	})
}

// websiteDelete deletes a website if it exists. umami answers 500 for a
// delete of an unknown id, so existence is checked first.
func (s *run) websiteDelete(ctx context.Context, teamID string, siteID uuid.UUID, id string) error {
	w, ok, err := s.r.Admin.GetWebsite(ctx, id)
	if err != nil || !ok {
		return err
	}
	if w.TeamID != teamID {
		return fmt.Errorf("provision: will not delete umami website %s of team %q, the tenant's team is %s", id, w.TeamID, teamID)
	}
	if err := s.r.Admin.DeleteWebsite(ctx, id); err != nil {
		return err
	}
	return s.audit(ctx, ActionWebsiteDelete, "site:"+siteID.String(), map[string]any{"website_id": id, "team_id": teamID})
}

// registryStatus is the status the gateway sees. a site only takes traffic
// while both it and its tenant are active; otherwise the tenant's status
// (suspended) is published, which the gateway refuses. pending and rejected
// tenants never reach this point, they publish nothing (unapprovedTenant).
func (s *run) registryStatus(site systemq.ProvisionSitesRow) string {
	if s.tenant.Status != statusActive {
		return s.tenant.Status
	}
	return site.Status
}

func (s *run) registryPut(ctx context.Context, site systemq.ProvisionSitesRow) error {
	want := registry.Site{
		ID:                  site.ID.String(),
		TenantID:            s.tenant.ID.String(),
		Status:              s.registryStatus(site),
		Origins:             site.Origins,
		QuarantineWebsiteID: site.QuarantineWebsiteID.UUID.String(),
		CollectUTM:          site.CollectUtm,
		Scale:               s.scales[site.ID],
	}
	if want.Origins == nil {
		want.Origins = []string{}
	}
	have, ok, err := s.r.Registry.GetSite(ctx, want.ID)
	if err != nil && !errors.Is(err, registry.ErrUnreadable) {
		return err
	}
	if ok && have.TenantID == want.TenantID && have.Status == want.Status &&
		have.QuarantineWebsiteID == want.QuarantineWebsiteID && slices.Equal(have.Origins, want.Origins) &&
		have.CollectUTM == want.CollectUTM && have.Scale == want.Scale {
		return nil
	}
	if err := s.r.Registry.PutSite(ctx, want); err != nil {
		return err
	}
	return s.audit(ctx, ActionRegistryPut, "site:"+want.ID, map[string]any{
		"status": want.Status, "origins": want.Origins, "quarantine_website_id": want.QuarantineWebsiteID,
		"collect_utm": want.CollectUTM, "scale": want.Scale,
	})
}

func (s *run) registryDelete(ctx context.Context, siteID uuid.UUID) error {
	_, ok, err := s.r.Registry.GetSite(ctx, siteID.String())
	if err != nil && !errors.Is(err, registry.ErrUnreadable) {
		return err
	}
	if !ok && err == nil {
		return nil
	}
	if err := s.r.Registry.DeleteSite(ctx, siteID.String()); err != nil {
		return err
	}
	return s.audit(ctx, ActionRegistryDelete, "site:"+siteID.String(), map[string]any{})
}

// otlpTokens makes every registry_otlp entry of the tenant match its row
// and the tenant's status (otlptokens.RegistryStatus): suspended, pending
// and rejected tenants publish their status, revoked rows publish revoked.
// keys are kept, never deleted, so a token that turns inactive is refused
// as such instead of as unknown.
func (s *run) otlpTokens(ctx context.Context) error {
	rows, err := s.q.ProvisionOTLPTokens(ctx, s.tenant.ID)
	if err != nil {
		return fmt.Errorf("provision: otlp tokens: %w", err)
	}
	for _, t := range rows {
		want, err := otlptokens.Entry(s.tenant.ID, t.ID, t.Hash, t.Status, s.tenant.Status)
		if err != nil {
			return err
		}
		wrote, err := s.r.Registry.SyncOTLPToken(ctx, want)
		if err != nil {
			return err
		}
		if !wrote {
			continue
		}
		if err := s.audit(ctx, ActionOTLPRegistryPut, "otlp_token:"+t.ID.String(),
			map[string]any{"tenant_id": s.tenant.ID.String(), "status": want.Status}); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileAll runs Changes for every tenant. a failing tenant does not
// stop the others; every failure is returned, joined.
func (r *Reconciler) ReconcileAll(ctx context.Context) ([]Change, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	err := r.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		ids, err = systemq.New(tx).ProvisionTenantIDs(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("provision: list tenants: %w", err)
	}
	var all []Change
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		c, err := r.Changes(ctx, id)
		all = append(all, c...)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", id, err))
		}
	}
	return all, errors.Join(errs...)
}
