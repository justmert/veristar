package operator

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/integrity"
)

// integritySQL is integrity.RangeOf in sql over the days_a, days_b and
// days_c columns, as a rank (1 for A, 2 for B, 3 for C, null without a
// graded day), so the project list can sort by it. TestIntegrityRankSQL
// keeps the two in step.
const integritySQL = `(case
           when days_a + days_b + days_c = 0 then null
           when days_c >= 0.25 * (days_a + days_b + days_c) then 3
           when days_b + days_c >= 0.25 * (days_a + days_b + days_c) then 2
           else 1 end)`

// IntegrityGrade is the range grade of a project row: how many of its site
// days in the range were graded A, B and C, and the grade they combine to.
// the reasons are in GET /api/operator/web/integrity.
type IntegrityGrade struct {
	Grade      *string `json:"grade"`
	GradedDays int64   `json:"graded_days"`
	DaysA      int64   `json:"days_a"`
	DaysB      int64   `json:"days_b"`
	DaysC      int64   `json:"days_c"`
}

func (g *IntegrityGrade) derive() {
	g.GradedDays = g.DaysA + g.DaysB + g.DaysC
	g.Grade = nil
	if s := integrity.RangeOf(int(g.DaysA), int(g.DaysB), int(g.DaysC)); s != "" {
		g.Grade = &s
	}
}

// webIntegrity answers GET /api/operator/web/integrity?tenant=<id> (contract
// 17.3): one project's integrity report over the range, the same report the
// project reads at GET /api/tenants/{tenant_id}/integrity.
func (g *Global) webIntegrity(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	for k := range q {
		if k != "tenant" && k != "from" && k != "to" {
			return httpx.BadQuery("unknown parameter " + k)
		}
	}
	rg, err := g.parseRange(q)
	if err != nil {
		return err
	}
	v, ok, err := oneValue(q, "tenant")
	if err != nil {
		return err
	}
	if !ok {
		return httpx.BadQuery("tenant is required")
	}
	id, err := uuid.Parse(v)
	if err != nil || id.String() != v {
		return httpx.BadQuery("tenant must be a project id")
	}
	ctx := r.Context()
	var rep *integrityview.Report
	err = g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		// an unknown or internal project is 404, as on every operator route
		if _, err := compareTenants(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		reps, err := integrityview.Load(ctx, tx, []uuid.UUID{id}, rg.From, rg.To)
		if err != nil {
			return err
		}
		rep = reps[id]
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: web integrity: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}
