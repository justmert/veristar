package operator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// query handling shared by the machine routes (contract 9.3).

// machine list page size (the design: 500)
const (
	machineDefaultLimit = 500
	machineMaxLimit     = 500
)

// hour range limits: /api/hourly reads a stored table and may span 31
// days; /web/hourly asks umami once per site, band and hour, so it is kept
// to one day.
const (
	maxAPIHours = 31 * 24
	maxWebHours = 24
	defHours    = 24
)

// checkParams refuses a parameter the route does not list (contract 9.3,
// as 7.1), so a typo never silently widens an answer.
func checkParams(q url.Values, allowed ...string) error {
	for k := range q {
		if !slices.Contains(allowed, k) {
			return httpx.BadQuery("unknown query parameter " + k)
		}
	}
	return nil
}

// tenantScope is the set of tenants a request reads. One is nil when the
// request reads every non-internal tenant (a key for all tenants without a
// tenant parameter).
type tenantScope struct {
	One *uuid.UUID
}

// arg is the value of @tenant in tenantScopeCTE.
func (s tenantScope) arg() *uuid.UUID { return s.One }

// tenantScopeCTE is the cte t of the tenants a request may read: one named
// tenant, or every non-internal one.
const tenantScopeCTE = `t as (
  select t.id, t.slug, t.name, t.status, t.created_at
  from cp.tenants t
  where (@tenant::uuid is null and not t.is_internal) or t.id = @tenant::uuid
)`

// resolveScope applies the key's scope to the tenant parameter. a scoped
// key reads its own tenant whatever its status, internal included, because
// an operator chose that tenant for it (the probe's key, this stage); another
// tenant was already refused with 403 by authenticate. a key for all
// tenants reads one named tenant, which must exist and not be internal
// (404 otherwise, as on every operator route), or all of them.
func resolveScope(ctx context.Context, tx pgx.Tx, key machineKey, q url.Values) (tenantScope, error) {
	v, ok, err := oneValue(q, "tenant")
	if err != nil {
		return tenantScope{}, err
	}
	if ok && !canonicalUUID.MatchString(v) {
		return tenantScope{}, httpx.BadQuery("tenant must be a lowercase uuid")
	}
	if key.Tenant != nil {
		if ok && v != key.Tenant.String() {
			// authenticate refuses this first; kept so the rule does not
			// depend on the order of two functions
			return tenantScope{}, httpx.Forbidden(CodeScopeForbidden, "this key reads only tenant "+key.Tenant.String())
		}
		id := *key.Tenant
		return tenantScope{One: &id}, nil
	}
	if !ok {
		return tenantScope{}, nil
	}
	id := uuid.MustParse(v)
	var internal bool
	err = tx.QueryRow(ctx, `select is_internal from cp.tenants where id = $1`, id).Scan(&internal)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && internal) {
		return tenantScope{}, httpx.NotFound()
	}
	if err != nil {
		return tenantScope{}, err
	}
	return tenantScope{One: &id}, nil
}

// parseLimit reads limit for the machine lists: 1 to 500, default 500.
func parseMachineLimit(q url.Values) (int, error) {
	v, ok, err := oneValue(q, "limit")
	if err != nil || !ok {
		return machineDefaultLimit, err
	}
	n, perr := strconv.Atoi(v)
	if perr != nil || n < 1 || n > machineMaxLimit {
		return 0, httpx.BadQuery(fmt.Sprintf("limit must be an integer between 1 and %d", machineMaxLimit))
	}
	return n, nil
}

// parseService reads the service filter with the rules of contract 7.1:
// valid utf-8, no control characters, at most 128 bytes. an empty value
// matches points stored without a service name.
func parseService(q url.Values) (*string, error) {
	v, ok, err := oneValue(q, "service")
	if err != nil || !ok {
		return nil, err
	}
	if len(v) > 128 || !utf8.ValidString(v) || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, httpx.BadQuery("service must be valid utf-8 of at most 128 bytes without control characters")
	}
	return &v, nil
}

// parseEnum reads an optional value out of a fixed set.
func parseEnum(q url.Values, key string, values ...string) (*string, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return nil, err
	}
	if !slices.Contains(values, v) {
		return nil, httpx.BadQuery(key + " must be one of " + strings.Join(values, ", "))
	}
	return &v, nil
}

// hourLayout is the only accepted form of an hour: an rfc 3339 utc hour start.
const hourLayout = "2006-01-02T15:04:05Z"

func parseHour(q url.Values, key string) (time.Time, bool, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	h, perr := time.Parse(hourLayout, v)
	if perr != nil || !h.Equal(h.Truncate(time.Hour)) {
		return time.Time{}, false, httpx.BadQuery(key + " must be an hour start such as 2026-11-12T09:00:00Z")
	}
	return h.UTC(), true, nil
}

// hourRange is a closed range of utc hour starts.
type hourRange struct {
	From time.Time
	To   time.Time
}

// end is the first instant after the range.
func (h hourRange) end() time.Time { return h.To.Add(time.Hour) }

func (h hourRange) hours() []time.Time {
	var out []time.Time
	for x := h.From; !x.After(h.To); x = x.Add(time.Hour) {
		out = append(out, x)
	}
	return out
}

// parseHourRange reads from and to as utc hour starts, both included.
// without to the range ends with the current hour; without from it holds
// defHours hours. to after the current hour, to before from and more than
// maxHours hours are 400 invalid_query.
func parseHourRange(q url.Values, now time.Time, maxHours int) (hourRange, error) {
	current := now.UTC().Truncate(time.Hour)
	from, hasFrom, err := parseHour(q, "from")
	if err != nil {
		return hourRange{}, err
	}
	to, hasTo, err := parseHour(q, "to")
	if err != nil {
		return hourRange{}, err
	}
	if !hasTo {
		to = current
		if hasFrom && from.After(current) {
			to = from
		}
	}
	if !hasFrom {
		from = to.Add(-(defHours - 1) * time.Hour)
	}
	switch {
	case to.After(current):
		return hourRange{}, httpx.BadQuery("to must not be after the current hour (utc)")
	case to.Before(from):
		return hourRange{}, httpx.BadQuery("to must not be before from")
	case int(to.Sub(from).Hours())+1 > maxHours:
		return hourRange{}, httpx.BadQuery(fmt.Sprintf("the range may hold at most %d hours", maxHours))
	}
	return hourRange{From: from, To: to}, nil
}

// machinePage is the keyset paging of a machine list. a cursor holds the
// sort key of the last item, the range the first page resolved (so a page
// fetched after midnight continues the same days), and a hash of every
// other parameter, the key and that range, so it works only for the query
// and the key it was made for (400 invalid_cursor otherwise). the hash has
// no secret, so a key holder can recompute it: the range limits are
// therefore checked again on every page (days, hourSpan), and a cursor
// can never carry more than a first page could have asked for.
type machinePage struct {
	route string
	// base is what the hash covers besides the range
	base  string
	Limit int
	// After is the sort key of the last item of the previous page, nil on
	// the first page.
	After []string
	// From and To are the range of the first page, set when a cursor was given.
	From, To string
}

type machineCursor struct {
	R string   `json:"r"`
	H string   `json:"h"`
	K []string `json:"k"`
	F string   `json:"f"`
	T string   `json:"t"`
}

// parseMachinePage reads limit and cursor. the hash covers the route, the
// key id and every query parameter except limit and cursor, so the page
// size may change between pages but nothing else.
func parseMachinePage(q url.Values, route string, key machineKey) (machinePage, error) {
	p := machinePage{route: route}
	var err error
	if p.Limit, err = parseMachineLimit(q); err != nil {
		return p, err
	}
	rest := url.Values{}
	for k, v := range q {
		if k != "limit" && k != "cursor" {
			rest[k] = v
		}
	}
	p.base = route + "\x00" + key.ID.String() + "\x00" + rest.Encode()
	v, ok, err := oneValue(q, "cursor")
	if err != nil {
		return p, httpx.BadCursor()
	}
	if !ok {
		return p, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return p, httpx.BadCursor()
	}
	var c machineCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.R != route || c.H != p.hash(c.F, c.T) || len(c.K) == 0 {
		return p, httpx.BadCursor()
	}
	p.After, p.From, p.To = c.K, c.F, c.T
	return p, nil
}

// hash binds a cursor to the route, key, parameters and range.
func (p machinePage) hash(from, to string) string {
	sum := sha256.Sum256([]byte(p.base + "\x00" + from + "\x00" + to))
	return hex.EncodeToString(sum[:16])
}

// next encodes the cursor after an item with sort key k.
func (p machinePage) next(k []string, from, to string) *string {
	b, err := json.Marshal(machineCursor{R: p.route, H: p.hash(from, to), K: k, F: from, T: to})
	if err != nil {
		panic(err) // strings always encode
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

// days returns the day range of the request: the one the first page
// resolved when a cursor was given, otherwise from and to as parsed now.
func (p machinePage) days(q url.Values, now time.Time) (dayRange, error) {
	if p.After == nil {
		return parseDayRange(q, now, DefaultRangeDays, MaxRangeDays)
	}
	from, err1 := time.Parse(time.DateOnly, p.From)
	to, err2 := time.Parse(time.DateOnly, p.To)
	// the same limits as a first page. today only moves
	// forward, so a range valid when the first page was read stays valid.
	if err1 != nil || err2 != nil || to.After(utcDay(now)) || to.Before(from) ||
		int(to.Sub(from).Hours()/24)+1 > MaxRangeDays {
		return dayRange{}, httpx.BadCursor()
	}
	return dayRange{From: from, To: to}, nil
}

// hourSpan is days for hour ranges.
func (p machinePage) hourSpan(q url.Values, now time.Time, maxHours int) (hourRange, error) {
	if p.After == nil {
		return parseHourRange(q, now, maxHours)
	}
	from, err1 := time.Parse(hourLayout, p.From)
	to, err2 := time.Parse(hourLayout, p.To)
	// the same limits as a first page
	if err1 != nil || err2 != nil || !from.Equal(from.Truncate(time.Hour)) || !to.Equal(to.Truncate(time.Hour)) ||
		to.After(now.UTC().Truncate(time.Hour)) || to.Before(from) || int(to.Sub(from).Hours())+1 > maxHours {
		return hourRange{}, httpx.BadCursor()
	}
	return hourRange{From: from, To: to}, nil
}

// keyset builds the paging clauses over the output columns of a query
// wrapped as z. cols are sql expressions on z, casts the types the cursor
// strings are read back as; text columns compare in the "C" collation so
// that the order and the comparison agree byte for byte.
type keyset struct {
	cols  []string
	casts []string
}

// wrap returns inner restricted to the rows after the cursor, in key order,
// at most limit + 1 rows (one more tells whether another page exists).
func (k keyset) wrap(inner string, p machinePage, args pgx.NamedArgs) string {
	order := make([]string, len(k.cols))
	params := make([]string, len(k.cols))
	for i, c := range k.cols {
		order[i] = c
		params[i] = fmt.Sprintf("@k%d::%s", i, k.casts[i])
		if k.casts[i] == "text" {
			order[i] = c + ` collate "C"`
			params[i] += ` collate "C"`
		}
	}
	where := ""
	if p.After != nil {
		for i, v := range p.After {
			args[fmt.Sprintf("k%d", i)] = v
		}
		where = " where (" + strings.Join(order, ", ") + ") > (" + strings.Join(params, ", ") + ")"
	}
	args["row_limit"] = p.Limit + 1
	return "select * from (" + inner + ") z" + where + " order by " + strings.Join(order, ", ") + " limit @row_limit"
}

// validAfter checks that a cursor's key has one value per key column, each
// readable as its type, before it reaches postgresql.
func (k keyset) validAfter(p machinePage) error {
	if p.After == nil {
		return nil
	}
	if len(p.After) != len(k.cols) {
		return httpx.BadCursor()
	}
	for i, v := range p.After {
		var err error
		switch k.casts[i] {
		case "uuid":
			_, err = uuid.Parse(v)
		case "date":
			_, err = time.Parse(time.DateOnly, v)
		case "timestamptz":
			_, err = time.Parse(time.RFC3339Nano, v)
		case "smallint":
			_, err = strconv.ParseInt(v, 10, 16)
		}
		if err != nil {
			return httpx.BadCursor()
		}
	}
	return nil
}
