package onchain

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/chain"
)

// how a contract's calls per function are read (contract 15.8). only
// verified contracts carry them: anyone can declare any public contract,
// so the calls of one the project never proved control of count nowhere.
const (
	// MissingNotVerified: the contract is declared but not verified
	MissingNotVerified = "not_verified"
	// MissingNotAContract: the id is an account, which has no functions
	MissingNotAContract = "not_a_contract"
	// the states of a day whose calls were read
	FunctionsRead       = chain.FunctionsRead
	FunctionsNoCallTree = chain.CodeNoCallTree
)

// FunctionsDay is one day of a contract's calls per function: State is
// "read" (the calls of accounts and of contracts), "no_call_tree" (the
// calls of accounts only) or null with the reason in Missing.
type FunctionsDay struct {
	Day     string  `json:"day"`
	State   *string `json:"state"`
	Missing *string `json:"missing"`
}

// FunctionPoint is one day of one function and caller. the numbers are
// null on a day whose calls by that caller were not read, and zero on a
// read day without calls. Failed is always null for the calls of accounts.
type FunctionPoint struct {
	Day        string `json:"day"`
	Successful *int64 `json:"successful"`
	Failed     *int64 `json:"failed"`
	Accounts   *int64 `json:"accounts"`
}

// FunctionUsage is one function of a contract and one kind of caller over
// the read days of a range. Accounts are distinct per day, so the range
// carries the busiest day's count and never a sum.
type FunctionUsage struct {
	Function          string          `json:"function"`
	Caller            string          `json:"caller"`
	Successful        int64           `json:"successful"`
	Failed            *int64          `json:"failed"`
	PeakDailyAccounts int64           `json:"peak_daily_accounts"`
	DaysWithCalls     int             `json:"days_with_calls"`
	Series            []FunctionPoint `json:"series,omitempty"`
}

// Functions is a verified contract's calls per function over a range.
type Functions struct {
	// days whose calls of accounts and of contracts were read
	DaysRead int `json:"days_read"`
	// days whose calls of accounts only were read
	DaysNoCallTree int `json:"days_no_call_tree"`
	// complete days without calls read, today excluded
	DaysMissing int             `json:"days_missing"`
	Functions   []FunctionUsage `json:"functions"`
}

// ContractFunctions is the answer of GET .../onchain/ids/{chain_id}/functions:
// one declared id's calls per function, caller and utc day. an account
// has none, and every day of it is missing not_a_contract.
type ContractFunctions struct {
	ID       uuid.UUID      `json:"id"`
	Strkey   string         `json:"strkey"`
	Kind     string         `json:"kind"`
	Verified bool           `json:"verified"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Days     []FunctionsDay `json:"days"`
	Functions
}

type fnDayRow struct {
	Strkey string
	Day    time.Time
	State  *string
}

type fnRow struct {
	Strkey     string
	Day        time.Time
	Function   string
	Caller     string
	Successful int64
	Failed     *int64
	Accounts   int64
}

// loadFunctions reads the calls per function of the verified contracts
// among ids over rg, with their daily series. an unverified contract gets
// days missing not_verified and no functions, an account days missing
// not_a_contract.
func loadFunctions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []idRow, rg Range, today time.Time) (map[string]*ContractFunctions, error) {
	out := map[string]*ContractFunctions{}
	var keys []string
	for _, r := range ids {
		out[r.Strkey] = &ContractFunctions{ID: r.ID, Strkey: r.Strkey, Kind: r.Kind, Verified: r.VerifiedAt != nil,
			From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Functions: Functions{Functions: []FunctionUsage{}}}
		if r.VerifiedAt != nil && r.Kind == "contract" {
			keys = append(keys, r.Strkey)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	states := map[string]*string{}
	stored := map[string]bool{}
	var rows []fnRow
	if len(keys) > 0 {
		dr, err := tx.Query(ctx, `
			select strkey, day, functions from agg.chain_daily
			where tenant_id = $1 and strkey = any($2::text[]) and day between $3::date and $4::date`,
			tenant, keys, rg.From, rg.To)
		if err != nil {
			return nil, fmt.Errorf("onchain: function days: %w", err)
		}
		days, err := pgx.CollectRows(dr, pgx.RowToStructByPos[fnDayRow])
		if err != nil {
			return nil, fmt.Errorf("onchain: function days: %w", err)
		}
		for _, d := range days {
			k := d.Strkey + "/" + d.Day.Format(time.DateOnly)
			states[k], stored[k] = d.State, true
		}
		fr, err := tx.Query(ctx, `
			select strkey, day, function_name, caller, successful, failed, accounts from agg.chain_function_daily
			where tenant_id = $1 and strkey = any($2::text[]) and day between $3::date and $4::date`,
			tenant, keys, rg.From, rg.To)
		if err != nil {
			return nil, fmt.Errorf("onchain: functions: %w", err)
		}
		rows, err = pgx.CollectRows(fr, pgx.RowToStructByPos[fnRow])
		if err != nil {
			return nil, fmt.Errorf("onchain: functions: %w", err)
		}
	}
	type usageKey struct{ strkey, function, caller string }
	byDay := map[string]fnRow{}
	usages := map[usageKey]bool{}
	for _, f := range rows {
		byDay[f.Strkey+"/"+f.Day.Format(time.DateOnly)+"/"+f.Caller+"/"+f.Function] = f
		usages[usageKey{f.Strkey, f.Function, f.Caller}] = true
	}
	byStrkey := map[string]idRow{}
	for _, r := range ids {
		byStrkey[r.Strkey] = r
	}
	for strkey, c := range out {
		r := byStrkey[strkey]
		read := map[string]string{}
		for _, d := range rg.Days() {
			day := d.Format(time.DateOnly)
			fd := FunctionsDay{Day: day}
			k := strkey + "/" + day
			var reason string
			switch s := states[k]; {
			case c.Kind != "contract":
				reason = MissingNotAContract
			case !c.Verified:
				reason = MissingNotVerified
			case !stored[k]:
				reason = missing(r, d, today)
			case s == nil:
				reason = MissingNotReadYet
			case *s == FunctionsRead || *s == FunctionsNoCallTree:
				v := *s
				fd.State = &v
				read[day] = v
			default:
				reason = *s
			}
			switch {
			case fd.State != nil && *fd.State == FunctionsRead:
				c.DaysRead++
			case fd.State != nil:
				c.DaysNoCallTree++
			case d.Before(today) && c.Kind == "contract":
				c.DaysMissing++
			}
			if reason != "" {
				fd.Missing = &reason
			}
			c.Days = append(c.Days, fd)
		}
		for u := range usages {
			if u.strkey != strkey {
				continue
			}
			x := FunctionUsage{Function: u.function, Caller: u.caller}
			if u.caller == chain.CallerContract {
				var zero int64
				x.Failed = &zero
			}
			for _, d := range rg.Days() {
				day := d.Format(time.DateOnly)
				p := FunctionPoint{Day: day}
				state, ok := read[day]
				if ok && (u.caller == chain.CallerAccount || state == FunctionsRead) {
					var s, a int64
					var f *int64
					if u.caller == chain.CallerContract {
						f = new(int64)
					}
					if row, ok := byDay[strkey+"/"+day+"/"+u.caller+"/"+u.function]; ok {
						s, a = row.Successful, row.Accounts
						if f != nil && row.Failed != nil {
							*f = *row.Failed
						}
						x.DaysWithCalls++
					}
					p.Successful, p.Accounts, p.Failed = &s, &a, f
					x.Successful += s
					if f != nil {
						*x.Failed += *f
					}
					x.PeakDailyAccounts = max(x.PeakDailyAccounts, a)
				}
				x.Series = append(x.Series, p)
			}
			c.Functions.Functions = append(c.Functions.Functions, x)
		}
		slices.SortFunc(c.Functions.Functions, func(a, b FunctionUsage) int {
			ca, cb := a.Successful, b.Successful
			if a.Failed != nil {
				ca += *a.Failed
			}
			if b.Failed != nil {
				cb += *b.Failed
			}
			return cmp.Or(cmp.Compare(cb, ca), cmp.Compare(a.Function, b.Function), cmp.Compare(a.Caller, b.Caller))
		})
	}
	return out, nil
}

// LoadFunctions reads one declared id's calls per function over rg. ok is
// false when the tenant has no id with that id.
func LoadFunctions(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, rg Range, today time.Time) (ContractFunctions, bool, error) {
	ids, err := loadIDs(ctx, tx, tenant)
	if err != nil {
		return ContractFunctions{}, false, err
	}
	ids = slices.DeleteFunc(ids, func(r idRow) bool { return r.ID != id })
	if len(ids) == 0 {
		return ContractFunctions{}, false, nil
	}
	m, err := loadFunctions(ctx, tx, tenant, ids, rg, today)
	if err != nil {
		return ContractFunctions{}, false, err
	}
	return *m[ids[0].Strkey], true, nil
}
