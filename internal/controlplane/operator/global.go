package operator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/db"
)

// the global web and api views (contract section 8). every
// read runs as cp_operator over aggregate tables only: agg.web_daily,
// agg.web_daily_dim, api.operator_daily (daily rows plus pending hours, 00022), cp.web_ingest_hourly, cp.tenants
// and cp.operator_sites. internal tenants are never shown.

// MaxRangeDays bounds the day range of every global route.
const MaxRangeDays = 366

// DefaultRangeDays is the range when from and to are both absent: the 30
// utc days ending today.
const DefaultRangeDays = 30

// MaxCompare is how many projects one compare request may name.
const MaxCompare = 5

// Global serves the four global routes.
type Global struct {
	db  *db.DB
	now func() time.Time
}

// NewGlobal needs a database with the cp_operator pool.
func NewGlobal(d *db.DB) (*Global, error) {
	if d == nil {
		return nil, errors.New("operator: database is required")
	}
	return &Global{db: d, now: time.Now}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (g *Global) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/web/projects", httpx.HandlerFunc(g.webProjects))
	r.Method(http.MethodGet, "/web/compare", httpx.HandlerFunc(g.webCompare))
	r.Method(http.MethodGet, "/web/integrity", httpx.HandlerFunc(g.webIntegrity))
	r.Method(http.MethodGet, "/api/projects", httpx.HandlerFunc(g.apiProjects))
	r.Method(http.MethodGet, "/api/compare", httpx.HandlerFunc(g.apiCompare))
}

// dayRange is a closed range of utc days.
type dayRange struct {
	From time.Time
	To   time.Time
}

// days lists every day of the range in order.
func (d dayRange) days() []time.Time {
	var out []time.Time
	for x := d.From; !x.After(d.To); x = x.AddDate(0, 0, 1) {
		out = append(out, x)
	}
	return out
}

// end is the first instant after the range, for timestamptz columns.
func (d dayRange) end() time.Time { return d.To.AddDate(0, 0, 1) }

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// parseDay reads a yyyy-mm-dd query value.
func parseDay(q url.Values, key string) (time.Time, bool, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	d, perr := time.Parse(time.DateOnly, v)
	if perr != nil {
		return time.Time{}, false, httpx.BadQuery(key + " must be a date yyyy-mm-dd")
	}
	return d, true, nil
}

// parseRange reads from and to (contract 8.1): both utc days, both
// included, to not after today, at most MaxRangeDays days.
func (g *Global) parseRange(q url.Values) (dayRange, error) {
	return parseDayRange(q, g.now(), DefaultRangeDays, MaxRangeDays)
}

// parseDayRange reads from and to as utc days, both included. without to
// the range ends today (or on from, when from is in the future, which the
// to check then refuses); without from it holds defDays days. to after
// today, to before from and more than maxDays days are 400 invalid_query.
func parseDayRange(q url.Values, now time.Time, defDays, maxDays int) (dayRange, error) {
	today := utcDay(now)
	from, hasFrom, err := parseDay(q, "from")
	if err != nil {
		return dayRange{}, err
	}
	to, hasTo, err := parseDay(q, "to")
	if err != nil {
		return dayRange{}, err
	}
	if !hasTo {
		to = today
		if hasFrom && from.After(today) {
			to = from
		}
	}
	if !hasFrom {
		from = to.AddDate(0, 0, -(defDays - 1))
	}
	switch {
	case to.After(today):
		return dayRange{}, httpx.BadQuery("to must not be after today (utc)")
	case to.Before(from):
		return dayRange{}, httpx.BadQuery("to must not be before from")
	case int(to.Sub(from).Hours()/24)+1 > maxDays:
		return dayRange{}, httpx.BadQuery(fmt.Sprintf("the range may hold at most %d days", maxDays))
	}
	return dayRange{From: from, To: to}, nil
}

// parseBool reads an optional true or false.
func parseBool(q url.Values, key string) (*bool, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return nil, err
	}
	switch v {
	case "true":
		b := true
		return &b, nil
	case "false":
		b := false
		return &b, nil
	}
	return nil, httpx.BadQuery(key + " must be true or false")
}

// projectFilter holds the filters both project lists share (contract 8.2).
type projectFilter struct {
	Statuses     []string   `json:"statuses"`
	Verified     *bool      `json:"verified"`
	StellarToml  *bool      `json:"stellar_toml"`
	HasWebData   *bool      `json:"has_web_data"`
	HasAPIData   *bool      `json:"has_api_data"`
	EnrolledFrom *time.Time `json:"enrolled_from"`
	EnrolledTo   *time.Time `json:"enrolled_to"`
	Q            *string    `json:"q"`
}

func parseFilter(q url.Values) (projectFilter, error) {
	f := projectFilter{Statuses: []string{}}
	if v, ok, err := oneValue(q, "status"); err != nil {
		return f, err
	} else if ok {
		for _, s := range strings.Split(v, ",") {
			if !tenantStatuses[s] {
				return f, httpx.BadQuery("unknown status " + s)
			}
			if !slices.Contains(f.Statuses, s) {
				f.Statuses = append(f.Statuses, s)
			}
		}
		slices.Sort(f.Statuses)
	}
	var err error
	if f.Verified, err = parseBool(q, "verified"); err != nil {
		return f, err
	}
	if f.StellarToml, err = parseBool(q, "stellar_toml"); err != nil {
		return f, err
	}
	if f.HasWebData, err = parseBool(q, "has_web_data"); err != nil {
		return f, err
	}
	if f.HasAPIData, err = parseBool(q, "has_api_data"); err != nil {
		return f, err
	}
	if d, ok, err := parseDay(q, "enrolled_from"); err != nil {
		return f, err
	} else if ok {
		f.EnrolledFrom = &d
	}
	if d, ok, err := parseDay(q, "enrolled_to"); err != nil {
		return f, err
	} else if ok {
		f.EnrolledTo = &d
	}
	if f.EnrolledFrom != nil && f.EnrolledTo != nil && f.EnrolledTo.Before(*f.EnrolledFrom) {
		return f, httpx.BadQuery("enrolled_to must not be before enrolled_from")
	}
	if v, ok, err := oneValue(q, "q"); err != nil {
		return f, err
	} else if ok {
		if n := utf8.RuneCountInString(v); n < 1 || n > 100 {
			return f, httpx.BadQuery("q must be 1 to 100 characters")
		}
		f.Q = &v
	}
	return f, nil
}

// listQuery is a parsed project list request.
type listQuery struct {
	Range      dayRange
	Filter     projectFilter
	Sort       string
	Descending bool
	Limit      int
	Offset     int
}

// listCursor is the position in a sorted project list. sort values are
// range sums that the hourly aggregation changes, so no keyset over them
// stays stable either; the cursor is an offset bound to the exact query it
// was made for (route, range, filters, sort and order), and a cursor used
// with any other query is 400 invalid_cursor.
type listCursor struct {
	R string `json:"r"`
	H string `json:"h"`
	O int    `json:"o"`
}

// fingerprint is a hash of everything that decides the list's order.
func (lq listQuery) fingerprint(route string) string {
	b, err := json.Marshal(struct {
		R    string        `json:"r"`
		From string        `json:"from"`
		To   string        `json:"to"`
		F    projectFilter `json:"f"`
		S    string        `json:"s"`
		D    bool          `json:"d"`
	}{route, lq.Range.From.Format(time.DateOnly), lq.Range.To.Format(time.DateOnly), lq.Filter, lq.Sort, lq.Descending})
	if err != nil {
		panic(err) // strings, bools, times and string slices always encode
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func (lq listQuery) nextCursor(route string, offset int) *string {
	b, err := json.Marshal(listCursor{R: route, H: lq.fingerprint(route), O: offset})
	if err != nil {
		panic(err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

// parseList reads range, filters, sort, order, limit and cursor. sorts maps
// every accepted sort name; def is the default sort, which is descending.
func (g *Global) parseList(r *http.Request, route string, sorts map[string]string, def string) (listQuery, error) {
	q := r.URL.Query()
	lq := listQuery{Sort: def, Descending: true, Limit: httpx.DefaultLimit}
	var err error
	if lq.Range, err = g.parseRange(q); err != nil {
		return lq, err
	}
	if lq.Filter, err = parseFilter(q); err != nil {
		return lq, err
	}
	if v, ok, err := oneValue(q, "sort"); err != nil {
		return lq, err
	} else if ok {
		if _, known := sorts[v]; !known {
			names := make([]string, 0, len(sorts))
			for k := range sorts {
				names = append(names, k)
			}
			slices.Sort(names)
			return lq, httpx.BadQuery("sort must be one of " + strings.Join(names, ", "))
		}
		lq.Sort = v
	}
	if v, ok, err := oneValue(q, "order"); err != nil {
		return lq, err
	} else if ok {
		if v != "asc" && v != "desc" {
			return lq, httpx.BadQuery("order must be asc or desc")
		}
		lq.Descending = v == "desc"
	}
	if v, ok, err := oneValue(q, "limit"); err != nil {
		return lq, err
	} else if ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > httpx.MaxLimit {
			return lq, httpx.BadQuery("limit must be an integer between 1 and 200")
		}
		lq.Limit = n
	}
	if v, ok, err := oneValue(q, "cursor"); err != nil {
		return lq, httpx.BadCursor()
	} else if ok {
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			return lq, httpx.BadCursor()
		}
		var c listCursor
		if err := json.Unmarshal(raw, &c); err != nil || c.R != route || c.H != lq.fingerprint(route) || c.O < 1 {
			return lq, httpx.BadCursor()
		}
		lq.Offset = c.O
	}
	return lq, nil
}

// canonicalUUID is the only id form the api accepts (contract section 1)
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// parseTenants reads the tenants of a compare request: 1 to MaxCompare
// distinct ids, in the order given.
func parseTenants(q url.Values) ([]uuid.UUID, error) {
	v, ok, err := oneValue(q, "tenants")
	if err != nil {
		return nil, err
	}
	if !ok || v == "" {
		return nil, httpx.BadQuery("tenants is required: 1 to 5 comma-separated tenant ids")
	}
	parts := strings.Split(v, ",")
	if len(parts) > MaxCompare {
		return nil, httpx.BadQuery(fmt.Sprintf("tenants may name at most %d projects", MaxCompare))
	}
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		if !canonicalUUID.MatchString(p) {
			return nil, httpx.BadQuery("tenants must be lowercase uuids")
		}
		id := uuid.MustParse(p)
		if slices.Contains(out, id) {
			return nil, httpx.BadQuery("tenants lists " + p + " twice")
		}
		out = append(out, id)
	}
	return out, nil
}

// parseLimitParam reads an optional integer in [1, max].
func parseLimitParam(q url.Values, key string, def, maxV int) (int, error) {
	v, ok, err := oneValue(q, key)
	if err != nil || !ok {
		return def, err
	}
	n, perr := strconv.Atoi(v)
	if perr != nil || n < 1 || n > maxV {
		return 0, httpx.BadQuery(fmt.Sprintf("%s must be an integer between 1 and %d", key, maxV))
	}
	return n, nil
}

// TenantRef names a project in the global views.
type TenantRef struct {
	ID        uuid.UUID       `json:"id"`
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	CreatedAt httpx.Timestamp `json:"created_at"`
}

// SiteCounts are the site facts the filters use.
type SiteCounts struct {
	Sites                int64 `json:"sites"`
	SitesVerified        int64 `json:"sites_verified"`
	SitesWithStellarToml int64 `json:"sites_with_stellar_toml"`
}

// GlobalList is the list envelope of the project lists: contract 2.3 plus
// the range read and the number of projects that match the filters.
type GlobalList[T any] struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Total      int64   `json:"total"`
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// ratio is a / b, or null when b is zero.
func ratio(a, b int64) *float64 {
	if b == 0 {
		return nil
	}
	v := float64(a) / float64(b)
	return &v
}

// baseCTE selects the projects of a list and the facts its filters need:
// t the non-internal tenants passing the tenant filters, s their site
// counts (rules of contract 5.2), w their web sums over the range per band,
// ap whether they have api rows with requests in the range. every later
// cte joins t, so nothing outside the filtered tenants is read.
const baseCTE = `
t as (
  select t.id, t.slug, t.name, t.status, t.created_at
  from cp.tenants t
  where not t.is_internal
    and (cardinality(@statuses::text[]) = 0 or t.status = any(@statuses::text[]))
    and (@q::text is null
         or strpos(lower(t.name), lower(@q::text)) > 0
         or strpos(lower(t.slug), lower(@q::text)) > 0)
    and (@enrolled_from::timestamptz is null or t.created_at >= @enrolled_from::timestamptz)
    and (@enrolled_to_end::timestamptz is null or t.created_at < @enrolled_to_end::timestamptz)
),
s as (
  select v.tenant_id,
         count(*) filter (where v.status <> 'removed')::bigint as sites,
         count(*) filter (where v.status <> 'removed' and v.verified_at is not null
                            and v.last_verify_error is null)::bigint as sites_verified,
         count(*) filter (where v.status <> 'removed' and v.stellar_toml ->> 'status' = 'found')::bigint
           as sites_with_stellar_toml
  from cp.operator_sites v
  join t on t.id = v.tenant_id
  group by v.tenant_id
),
w as (
  select d.tenant_id,
         coalesce(sum(d.pageviews) filter (where d.band = 'primary'), 0)::bigint as pageviews,
         coalesce(sum(d.visitors) filter (where d.band = 'primary'), 0)::bigint as visitors,
         coalesce(sum(d.visits) filter (where d.band = 'primary'), 0)::bigint as visits,
         coalesce(sum(d.bounces) filter (where d.band = 'primary'), 0)::bigint as bounces,
         coalesce(sum(d.totaltime) filter (where d.band = 'primary'), 0)::bigint as totaltime,
         coalesce(sum(d.pageviews) filter (where d.band = 'quarantine'), 0)::bigint as quarantine_pageviews,
         coalesce(sum(d.visitors) filter (where d.band = 'quarantine'), 0)::bigint as quarantine_visitors
  from agg.web_daily d
  join t on t.id = d.tenant_id
  where d.day between @from_day::date and @to_day::date
  group by d.tenant_id
),
ap as (
  select m.tenant_id, bool_or(m.count > 0) as has_api
  from api.operator_daily(@from_at::timestamptz, @to_end::timestamptz, array(select id from t)) m
  join t on t.id = m.tenant_id
  group by m.tenant_id
)`

// derivedFilter applies the filters on facts computed per project. it runs
// on a cte that has sites_verified, sites_with_stellar_toml, has_web_data
// and has_api_data.
const derivedFilter = `
  (@verified::boolean is null or (sites_verified > 0) = @verified::boolean)
  and (@stellar_toml::boolean is null or (sites_with_stellar_toml > 0) = @stellar_toml::boolean)
  and (@has_web_data::boolean is null or has_web_data = @has_web_data::boolean)
  and (@has_api_data::boolean is null or has_api_data = @has_api_data::boolean)`

// args are the named arguments of baseCTE and derivedFilter.
func (lq listQuery) args() pgx.NamedArgs {
	var enrolledFrom, enrolledToEnd *time.Time
	if lq.Filter.EnrolledFrom != nil {
		v := *lq.Filter.EnrolledFrom
		enrolledFrom = &v
	}
	if lq.Filter.EnrolledTo != nil {
		v := lq.Filter.EnrolledTo.AddDate(0, 0, 1)
		enrolledToEnd = &v
	}
	return pgx.NamedArgs{
		"statuses":        lq.Filter.Statuses,
		"q":               lq.Filter.Q,
		"enrolled_from":   enrolledFrom,
		"enrolled_to_end": enrolledToEnd,
		"from_day":        lq.Range.From,
		"to_day":          lq.Range.To,
		"from_at":         lq.Range.From,
		"to_end":          lq.Range.end(),
		"verified":        lq.Filter.Verified,
		"stellar_toml":    lq.Filter.StellarToml,
		"has_web_data":    lq.Filter.HasWebData,
		"has_api_data":    lq.Filter.HasAPIData,
		"row_limit":       lq.Limit + 1,
		"row_offset":      lq.Offset,
	}
}

// orderBy is the order clause of a list: the whitelisted sort expression,
// projects without a value last in both directions, id as the tie-breaker.
func (lq listQuery) orderBy(sorts map[string]string) string {
	dir := "asc"
	if lq.Descending {
		dir = "desc"
	}
	return " order by " + sorts[lq.Sort] + " " + dir + " nulls last, id " + dir
}

// snapshot makes every statement of the transaction see one snapshot, so
// the total, the page and its breakdowns agree although the aggregation
// job may commit in between. it must be the transaction's first statement.
func snapshot(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `set transaction isolation level repeatable read, read only`)
	return err
}

// compareTenants loads the named projects in the order given. an unknown or
// internal id is 404 not_found, as on every operator route.
func compareTenants(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) ([]TenantRef, error) {
	rows, err := tx.Query(ctx, `select id, slug, name, status, created_at from cp.tenants
		where id = any(@ids::uuid[]) and not is_internal`, pgx.NamedArgs{"ids": ids})
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TenantRef, error) {
		var t TenantRef
		var at time.Time
		err := r.Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &at)
		t.CreatedAt = httpx.Timestamp(at)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]TenantRef, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(found, func(t TenantRef) bool { return t.ID == id })
		if i < 0 {
			return nil, httpx.NotFound()
		}
		out = append(out, found[i])
	}
	return out, nil
}
