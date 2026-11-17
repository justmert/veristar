package exports

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/onchain"
)

// FunctionsHeader is the header row of a contract's calls per function
// (contract 15.8): one row per utc day, function and caller.
var FunctionsHeader = []string{
	"day", "strkey", "function", "caller", "day_state", "day_missing", "successful", "failed", "accounts",
}

// WriteFunctions writes the rows of a contract's calls per function, the
// numbers the console shows: a day whose calls by that caller were not
// read has empty cells, never 0, and day_missing says why.
func WriteFunctions(w http.ResponseWriter, name string, c onchain.ContractFunctions) error {
	cw, err := NewWriter(w, name, FunctionsHeader)
	if err != nil {
		return err
	}
	for i, d := range c.Days {
		for _, f := range c.Functions.Functions {
			p := f.Series[i]
			if err := cw.Row(d.Day, c.Strkey, Text(f.Function), f.Caller, StrPtr(d.State), StrPtr(d.Missing),
				IntPtr(p.Successful), IntPtr(p.Failed), IntPtr(p.Accounts)); err != nil {
				return err
			}
		}
	}
	return cw.Close()
}

// FunctionRows is how many rows WriteFunctions writes.
func FunctionRows(c onchain.ContractFunctions) int { return len(c.Days) * len(c.Functions.Functions) }

// functions serves GET /api/tenants/{tenant_id}/onchain/ids/{chain_id}/functions.csv
// under cp_tenant, with an export.csv audit row.
func (o *Overview) functions(w http.ResponseWriter, r *http.Request) error {
	a, err := o.scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "chain_id"))
	if err != nil {
		return err
	}
	now := o.now()
	rg, err := onchain.ParseRange(r.URL.Query(), now, onchain.DefaultRangeDays, onchain.MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var c onchain.ContractFunctions
	err = o.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		v, ok, err := onchain.LoadFunctions(ctx, tx, a.TenantID, id, rg, today)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound()
		}
		c = v
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionExport, Target: audit.TenantTarget(a.TenantID),
			Detail: map[string]any{
				"tenant_id": a.TenantID.String(), "view": "contract_functions", "strkey": c.Strkey,
				"from": rg.From.Format(time.DateOnly), "to": rg.To.Format(time.DateOnly), "rows": FunctionRows(c),
			},
		})
	})
	if err != nil {
		return err
	}
	name := FileName("veristar-functions", c.Strkey[:8], rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly))
	if err := WriteFunctions(w, name, c); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
