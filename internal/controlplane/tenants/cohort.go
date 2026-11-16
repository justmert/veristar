package tenants

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// the cohort a project belongs to (contract 19.1): program, round,
// category and award date. a project proposes them, at enrollment or later,
// and an operator confirms them; only confirmed values place a project in
// a cohort.

// CohortInput is the cohort object of a request: every field optional,
// at least one present.
type CohortInput struct {
	Program   *string `json:"program"`
	Round     *string `json:"round"`
	Category  *string `json:"category"`
	AwardDate *string `json:"award_date"`
}

// CohortValues are checked cohort values; a nil field is not set.
type CohortValues struct {
	Program   *string
	Round     *string
	Category  *string
	AwardDate *time.Time
}

// Empty reports whether no value is set.
func (c CohortValues) Empty() bool {
	return c.Program == nil && c.Round == nil && c.Category == nil && c.AwardDate == nil
}

// AuditDetail is the values as an audit detail object.
func (c CohortValues) AuditDetail() map[string]any {
	var award *string
	if c.AwardDate != nil {
		s := c.AwardDate.Format(time.DateOnly)
		award = &s
	}
	return map[string]any{"program": c.Program, "round": c.Round, "category": c.Category, "award_date": award}
}

// earliestAward is the first award date accepted; scf began awarding in 2018
var earliestAward = time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

func cohortText(f *Fields, path string, v *string) *string {
	if v == nil {
		return nil
	}
	s := Text(f, path, *v, 1, 80)
	if strings.ContainsAny(s, "\n\t") {
		f.Add(path, httpx.FieldInvalidFormat)
	}
	return &s
}

// CheckCohort validates a cohort object under path. allowEmpty lets an
// operator clear a project's cohort with an object of nulls; a proposal
// must name at least one value.
func CheckCohort(f *Fields, path string, c *CohortInput, today time.Time, allowEmpty bool) CohortValues {
	var out CohortValues
	if c == nil {
		f.Add(path, httpx.FieldRequired)
		return out
	}
	out.Program = cohortText(f, path+".program", c.Program)
	out.Round = cohortText(f, path+".round", c.Round)
	out.Category = cohortText(f, path+".category", c.Category)
	if c.AwardDate != nil {
		d, err := time.Parse(time.DateOnly, *c.AwardDate)
		switch {
		case err != nil:
			f.Add(path+".award_date", httpx.FieldInvalidFormat)
		case d.Before(earliestAward) || d.After(today):
			f.Add(path+".award_date", httpx.FieldInvalidValue)
		default:
			out.AwardDate = &d
		}
	}
	if !allowEmpty && c.Program == nil && c.Round == nil && c.Category == nil && c.AwardDate == nil {
		f.Add(path, httpx.FieldRequired)
	}
	return out
}

// ProposeCohort writes a proposal of tenant by user in tx: under cp_system
// at enrollment, under cp_tenant from the proposal route. the confirmed
// values are untouched.
func ProposeCohort(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, v CohortValues) error {
	_, err := tx.Exec(ctx, `
		insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_round, proposed_category,
		                               proposed_award_date, proposed_by, proposed_at, updated_at)
		values ($1, $2, $3, $4, $5, $6, now(), now())
		on conflict (tenant_id) do update set
		  proposed_program = excluded.proposed_program, proposed_round = excluded.proposed_round,
		  proposed_category = excluded.proposed_category, proposed_award_date = excluded.proposed_award_date,
		  proposed_by = excluded.proposed_by, proposed_at = excluded.proposed_at, updated_at = excluded.updated_at`,
		tenant, v.Program, v.Round, v.Category, v.AwardDate, user)
	return err
}
