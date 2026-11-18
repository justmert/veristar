package scfawards

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
)

// SourceSCFImport marks a cohort proposal the award directory wrote
// (cp.tenant_cohorts.proposed_source); a member's own proposal is
// SourceMember.
const (
	SourceSCFImport = "scf_import"
	SourceMember    = "member"
)

// ActionCohortPropose is the audit action of a proposal; the same action a
// member's proposal writes, with source scf_import in the detail.
const ActionCohortPropose = "cohort.propose"

// ActorMatch is the audit actor of the matching that runs when a project
// enrolls or adds a site.
const ActorMatch = "system:scf-match"

// Cohort is a program, round, category and award date.
type Cohort struct {
	Program   *string `json:"program"`
	Round     *string `json:"round"`
	Category  *string `json:"category"`
	AwardDate *string `json:"award_date"`
}

func strp(s string) *string { return &s }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

// CohortOf is the cohort an award places a project in.
func CohortOf(a AwardRow) Cohort {
	return Cohort{Program: strp(a.Program), Round: strp(a.Round), Category: strp(a.Category), AwardDate: dateString(a.AwardDate)}
}

func eq(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// Equal reports whether two cohorts hold the same four values.
func (c Cohort) Equal(o Cohort) bool {
	return eq(c.Program, o.Program) && eq(c.Round, o.Round) && eq(c.Category, o.Category) && eq(c.AwardDate, o.AwardDate)
}

// Differs reports whether c, the cohort of a project, contradicts award:
// a value the award states that c holds differently. an award without a
// date says nothing about the date.
func (c Cohort) Differs(award Cohort) bool {
	differ := func(have, want *string) bool { return want != nil && !eq(have, want) }
	return differ(c.Program, award.Program) || differ(c.Round, award.Round) ||
		differ(c.Category, award.Category) || differ(c.AwardDate, award.AwardDate)
}

func (c Cohort) detail() map[string]any {
	return map[string]any{"program": c.Program, "round": c.Round, "category": c.Category, "award_date": c.AwardDate}
}

// CohortRow is the stored cohort of a project.
type CohortRow struct {
	Confirmed      *Cohort
	Proposal       *Cohort
	ProposalSource *string
	// SCFAwardID is the award the import last proposed, kept after the
	// proposal is confirmed or dismissed
	SCFAwardID *uuid.UUID
}

// LoadCohorts reads the cohort rows of the named projects.
func LoadCohorts(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]CohortRow, error) {
	out := map[uuid.UUID]CohortRow{}
	rows, err := tx.Query(ctx, `select tenant_id, confirmed_at is not null, program, round, category, award_date,
		       proposed_at is not null, proposed_program, proposed_round, proposed_category, proposed_award_date,
		       proposed_source, scf_award_id
		from cp.tenant_cohorts where tenant_id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("scfawards: cohorts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var row CohortRow
		var confirmed, proposed bool
		var c, p Cohort
		var cd, pd *time.Time
		if err := rows.Scan(&id, &confirmed, &c.Program, &c.Round, &c.Category, &cd,
			&proposed, &p.Program, &p.Round, &p.Category, &pd, &row.ProposalSource, &row.SCFAwardID); err != nil {
			return nil, fmt.Errorf("scfawards: cohorts: %w", err)
		}
		if confirmed {
			c.AwardDate = dateString(cd)
			row.Confirmed = &c
		}
		if proposed {
			p.AwardDate = dateString(pd)
			row.Proposal = &p
		}
		out[id] = row
	}
	return out, rows.Err()
}

// cohort states of a matched award, comparing the project's cohort with
// the award
const (
	// the confirmed cohort holds the award's values
	CohortConfirmed = "confirmed"
	// the confirmed cohort contradicts the award: a mismatch the operator
	// resolves; the import never overwrites a confirmed cohort
	CohortConfirmedDiffers = "confirmed_differs"
	// a proposal from this award waits for the operator
	CohortProposed = "proposed"
	// the member's own proposal waits; it holds the award's values
	CohortMemberProposal = "member_proposal"
	// the member's own proposal contradicts the award; the import leaves
	// it in place
	CohortMemberProposalDiffers = "member_proposal_differs"
	// the operator dismissed the proposal of this award
	CohortDismissed = "dismissed"
	// nothing proposed yet: the evidence appeared after the last matching
	// run (a verified on-chain id, for example); the next import or
	// platformctl scf match proposes it
	CohortNotProposed = "not_proposed"
)

// CohortState compares a matched project's cohort row with its award.
func CohortState(row CohortRow, a AwardRow) string {
	award := CohortOf(a)
	switch {
	case row.Confirmed != nil && row.Confirmed.Differs(award):
		return CohortConfirmedDiffers
	case row.Confirmed != nil:
		return CohortConfirmed
	case row.Proposal != nil && row.ProposalSource != nil && *row.ProposalSource == SourceSCFImport:
		if row.SCFAwardID != nil && *row.SCFAwardID == a.ID && row.Proposal.Equal(award) {
			return CohortProposed
		}
		return CohortNotProposed
	case row.Proposal != nil && row.Proposal.Differs(award):
		return CohortMemberProposalDiffers
	case row.Proposal != nil:
		return CohortMemberProposal
	case row.SCFAwardID != nil && *row.SCFAwardID == a.ID:
		return CohortDismissed
	default:
		return CohortNotProposed
	}
}

// AutofillResult counts what one matching run found and wrote.
type AutofillResult struct {
	Matched     int
	Ambiguous   int
	NotEnrolled int
	// Proposed counts the proposals written in this run
	Proposed int
	// states counts the matched awards by cohort state after the run
	States map[string]int
	// AmbiguousAwards lists the ambiguous awards and their candidates
	AmbiguousAwards []AmbiguousAward
}

// AmbiguousAward is an award that matched nothing because the evidence is
// not one to one.
type AmbiguousAward struct {
	Award      AwardRow
	Candidates []*Tenant
	SharedWith []uuid.UUID
}

// Autofill matches every award and writes a cohort proposal for each
// matched project in only (every matched project when only is nil) whose
// cohort lacks one: no confirmed cohort, no proposal of the member's own,
// and no earlier proposal of the same award that the operator confirmed or
// dismissed. a proposal of an earlier import is replaced when the award or
// the match changed. each proposal writes a cohort.propose audit row as
// actor, with source scf_import, in tx, which runs under cp_system.
func Autofill(ctx context.Context, tx pgx.Tx, actor string, only []uuid.UUID) (AutofillResult, error) {
	res := AutofillResult{States: map[string]int{}}
	// one matching run at a time, so two runs never both propose for the
	// same project from different snapshots
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.scf_autofill', 0))`); err != nil {
		return res, err
	}
	awards, err := LoadAwards(ctx, tx)
	if err != nil {
		return res, err
	}
	tenants, err := LoadTenants(ctx, tx)
	if err != nil {
		return res, err
	}
	matches := MatchAll(awards, tenants)
	want := map[uuid.UUID]bool{}
	for _, id := range only {
		want[id] = true
	}
	for _, a := range awards {
		m := matches[a.ID]
		switch m.State {
		case StateNotEnrolled:
			res.NotEnrolled++
			continue
		case StateAmbiguous:
			res.Ambiguous++
			amb := AmbiguousAward{Award: a, SharedWith: m.SharedWith}
			for _, c := range m.Candidates {
				amb.Candidates = append(amb.Candidates, tenants[c.TenantID])
			}
			res.AmbiguousAwards = append(res.AmbiguousAwards, amb)
			continue
		}
		res.Matched++
		tid := m.Tenant()
		if only != nil && !want[tid] {
			continue
		}
		state, wrote, err := proposeFor(ctx, tx, actor, tid, a, m.Candidates[0].Evidence)
		if err != nil {
			return res, err
		}
		if wrote {
			res.Proposed++
		}
		res.States[state]++
	}
	return res, nil
}

// proposeFor writes the award's proposal for the project when its cohort
// row calls for one, and returns the cohort state after it.
func proposeFor(ctx context.Context, tx pgx.Tx, actor string, tenant uuid.UUID, a AwardRow, ev []Evidence) (string, bool, error) {
	// the lock and row lock of the operator's cohort routes, so the audit's
	// old values are the ones this write replaces
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.cohort:' || $1::text, 0))`, tenant); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(ctx, `select 1 from cp.tenant_cohorts where tenant_id = $1 for update`, tenant); err != nil {
		return "", false, err
	}
	rows, err := LoadCohorts(ctx, tx, []uuid.UUID{tenant})
	if err != nil {
		return "", false, err
	}
	row := rows[tenant]
	state := CohortState(row, a)
	if state != CohortNotProposed {
		return state, false, nil
	}
	award := CohortOf(a)
	if _, err := tx.Exec(ctx, `
		insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_round, proposed_category,
		                               proposed_award_date, proposed_by, proposed_at, proposed_source, scf_award_id, updated_at)
		values ($1, $2, $3, $4, $5, null, now(), 'scf_import', $6, now())
		on conflict (tenant_id) do update set
		  proposed_program = excluded.proposed_program, proposed_round = excluded.proposed_round,
		  proposed_category = excluded.proposed_category, proposed_award_date = excluded.proposed_award_date,
		  proposed_by = null, proposed_at = excluded.proposed_at, proposed_source = 'scf_import',
		  scf_award_id = excluded.scf_award_id, updated_at = excluded.updated_at`,
		tenant, a.Program, a.Round, a.Category, a.AwardDate, a.ID); err != nil {
		return "", false, fmt.Errorf("scfawards: propose: %w", err)
	}
	var old any
	if row.Proposal != nil {
		old = row.Proposal.detail()
	}
	evidence := make([]map[string]any, 0, len(ev))
	for _, e := range ev {
		x := map[string]any{"kind": e.Kind, "value": e.Value, "verified": e.Verified}
		if e.SiteID != nil {
			x["site_id"] = e.SiteID.String()
		}
		evidence = append(evidence, x)
	}
	if err := audit.InSystem(ctx, tx, audit.Entry{
		Actor: actor, Action: ActionCohortPropose, Target: audit.TenantTarget(tenant),
		Detail: map[string]any{
			"tenant_id": tenant.String(), "source": SourceSCFImport, "award_id": a.ID.String(),
			"evidence": evidence, "proposal": audit.Change(old, award.detail()),
		},
	}); err != nil {
		return "", false, err
	}
	return CohortProposed, true, nil
}

// SortedStates lists the cohort states of a result in a stable order.
func (r AutofillResult) SortedStates() []string {
	out := make([]string, 0, len(r.States))
	for k := range r.States {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
