package tenants

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/integrityview"
)

// integrity answers GET /api/tenants/{tenant_id}/integrity (contract 17.2):
// the project's integrity grade over a range, per site and day, with the
// same reasons operators see. it reads under cp_tenant, so row-level
// security keeps it to the project's own rows.
func (h *Routes) integrity(w http.ResponseWriter, r *http.Request) error {
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	for k := range q {
		if k != "from" && k != "to" {
			return httpx.BadQuery("unknown parameter " + k)
		}
	}
	from, to, err := integrityview.ParseRange(q, time.Now())
	if err != nil {
		return err
	}
	ctx := r.Context()
	var rep *integrityview.Report
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		reps, err := integrityview.Load(ctx, tx, []uuid.UUID{a.TenantID}, from, to)
		if err != nil {
			return err
		}
		rep = reps[a.TenantID]
		return nil
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}
