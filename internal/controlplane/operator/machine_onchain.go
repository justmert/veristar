package operator

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// the on-chain routes of the machine api (contract 9.10 and 15.7): the
// declared ids with their proof and read state, and the stored daily rows.
// a day without a row was not read; the id's last_error says why.

// MachineOnchainID is one item of GET /onchain/ids.
type MachineOnchainID struct {
	TenantID      uuid.UUID        `json:"tenant_id"`
	Strkey        string           `json:"strkey"`
	Kind          string           `json:"kind"`
	AddedBy       string           `json:"added_by"`
	Verified      bool             `json:"verified"`
	VerifyMethod  *string          `json:"verify_method"`
	VerifiedAt    *httpx.Timestamp `json:"verified_at"`
	CoveredFrom   *string          `json:"covered_from"`
	CoveredTo     *string          `json:"covered_to"`
	LastSuccessAt *httpx.Timestamp `json:"last_success_at"`
	LastError     *string          `json:"last_error"`
	HomeDomain    *string          `json:"home_domain"`
}

var onchainIDKeys = keyset{cols: []string{"z.tenant_id", "z.strkey"}, casts: []string{"uuid", "text"}}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func (m *Machine) onchainIDs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-onchain-ids", key)
	if err != nil {
		return err
	}
	if err := onchainIDKeys.validAfter(p); err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineList[MachineOnchainID]{Items: []MachineOnchainID{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg()}
		inner := `with ` + tenantScopeCTE + `
			select c.tenant_id, c.strkey, c.kind, c.added_by, c.verify_method, c.verified_at,
			       r.covered_from, r.covered_to, r.last_success_at, r.last_error, r.home_domain
			from cp.chain_ids c join t on t.id = c.tenant_id
			left join agg.chain_reads r on r.tenant_id = c.tenant_id and r.strkey = c.strkey`
		rows, err := tx.Query(ctx, onchainIDKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineOnchainID, error) {
			var x MachineOnchainID
			var verifiedAt, success *time.Time
			var from, to *time.Time
			err := row.Scan(&x.TenantID, &x.Strkey, &x.Kind, &x.AddedBy, &x.VerifyMethod, &verifiedAt,
				&from, &to, &success, &x.LastError, &x.HomeDomain)
			x.Verified = verifiedAt != nil
			x.VerifiedAt, x.LastSuccessAt = httpx.TimestampPtr(verifiedAt), httpx.TimestampPtr(success)
			x.CoveredFrom, x.CoveredTo = dateString(from), dateString(to)
			return x, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			out.NextCursor = p.next([]string{l.TenantID.String(), l.Strkey}, "", "")
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine onchain ids: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// MachineOnchainDay is one item of GET /onchain/daily.
type MachineOnchainDay struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	Strkey         string    `json:"strkey"`
	Day            string    `json:"day"`
	Source         string    `json:"source"`
	Operations     int64     `json:"operations"`
	Transactions   int64     `json:"transactions"`
	Counterparties int64     `json:"counterparties"`
}

var onchainDayKeys = keyset{cols: []string{"z.tenant_id", "z.strkey", "z.day"}, casts: []string{"uuid", "text", "date"}}

func (m *Machine) onchainDaily(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	key := keyFrom(r.Context())
	p, err := parseMachinePage(q, "machine-onchain-daily", key)
	if err != nil {
		return err
	}
	if err := onchainDayKeys.validAfter(p); err != nil {
		return err
	}
	rg, err := p.days(q, m.now())
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := MachineRangeList[MachineOnchainDay]{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Items: []MachineOnchainDay{}}
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"tenant": sc.arg(), "from_day": rg.From, "to_day": rg.To}
		// only ids the project still declares
		inner := `with ` + tenantScopeCTE + `
			select d.tenant_id, d.strkey, d.day, d.source, d.operations, d.transactions, d.counterparties
			from agg.chain_daily d join t on t.id = d.tenant_id
			join cp.chain_ids c on c.tenant_id = d.tenant_id and c.strkey = d.strkey
			where d.day between @from_day::date and @to_day::date`
		rows, err := tx.Query(ctx, onchainDayKeys.wrap(inner, p, args), args)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MachineOnchainDay, error) {
			var d MachineOnchainDay
			var day time.Time
			err := row.Scan(&d.TenantID, &d.Strkey, &day, &d.Source, &d.Operations, &d.Transactions, &d.Counterparties)
			d.Day = day.Format(time.DateOnly)
			return d, err
		})
		if err != nil {
			return err
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			l := items[len(items)-1]
			out.NextCursor = p.next([]string{l.TenantID.String(), l.Strkey, l.Day}, out.From, out.To)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: machine onchain daily: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
