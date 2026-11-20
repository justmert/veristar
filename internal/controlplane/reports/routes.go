package reports

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/canonjson"
	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
)

// limits of contract 20
const (
	// MaxPeriodDays bounds a report's period.
	MaxPeriodDays = 366
	// CreateLimit reports per project within CreateWindow.
	CreateLimit  = 20
	CreateWindow = 24 * time.Hour
	// RevokeLimit revocations per project within RevokeWindow.
	RevokeLimit  = 60
	RevokeWindow = time.Hour
	// ViewLimit public views of one report within ViewWindow; each one is
	// audited, so this also bounds the audit rows one link can cause.
	ViewLimit  = 300
	ViewWindow = time.Hour
	// PublicRate and PublicBurst bound the public route per control plane
	// instance, whatever id is asked for, so guessing ids costs time.
	PublicRate  = 20
	PublicBurst = 60
)

// audit actions of contract 3.3
const (
	ActionCreate = "report.create"
	ActionRevoke = "report.revoke"
	ActionView   = "report.view"
)

// PublicActor is the audit actor of a view through the public link: no
// account, and nothing that identifies the reader.
const PublicActor = "public"

// Target names a report in the audit log.
func Target(id string) string { return "report:" + id }

// idPattern is the shape of a report id: 128 random bits in lowercase
// base32 without padding.
var idPattern = regexp.MustCompile(`^[a-z2-7]{26}$`)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewID returns a fresh unguessable report id.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(idEncoding.EncodeToString(b)), nil
}

// Signature is the signature member of a signed file.
type Signature struct {
	Scheme string `json:"scheme"`
	Signer string `json:"signer"`
	Value  string `json:"value"`
}

// File is the signed file: the report and its sep-53 signature over the
// canonical json bytes of the report member.
type File struct {
	Report    json.RawMessage `json:"report"`
	Signature Signature       `json:"signature"`
}

// Keys are the platform's report keys: the current one and the ones it
// replaced, whose reports still verify.
type Keys struct {
	Current     string   `json:"current"`
	Retired     []string `json:"retired"`
	StellarTOML string   `json:"stellar_toml"`
}

// Config are the parts of the report routes.
type Config struct {
	Scope   *tenants.Scope
	DB      *db.DB
	Signing config.ReportSigning
	// ConsoleOrigin is https://app.<BASE_DOMAIN>: the share links and the
	// stellar.toml that publishes the keys live there.
	ConsoleOrigin string
	Platform      string
	Now           func() time.Time
}

// Routes serves the member routes, the public route and stellar.toml.
type Routes struct {
	c       Config
	limiter *bucket
}

// New checks the configuration.
func New(c Config) (*Routes, error) {
	if c.Scope == nil || c.DB == nil || c.Signing.Key.Full() == nil || c.Signing.Address == "" || c.ConsoleOrigin == "" ||
		c.Platform == "" || c.Now == nil {
		return nil, errors.New("reports: a scope, a database, the signing key, the console origin, the version and a clock are required")
	}
	return &Routes{c: c, limiter: &bucket{rate: PublicRate, burst: PublicBurst, tokens: PublicBurst}}, nil
}

// Keys returns the published report keys.
func (h *Routes) Keys() Keys {
	retired := append([]string{}, h.c.Signing.Retired...)
	return Keys{Current: h.c.Signing.Address, Retired: retired, StellarTOML: h.c.ConsoleOrigin + StellarTOMLPath}
}

// Private registers the member routes under a router mounted at /api.
func (h *Routes) Private(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/reports", httpx.HandlerFunc(h.list))
	r.Method(http.MethodPost, "/tenants/{tenant_id}/reports", httpx.HandlerFunc(h.create))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/reports/{report_id}", httpx.HandlerFunc(h.get))
	r.Method(http.MethodPost, "/tenants/{tenant_id}/reports/{report_id}/revoke", httpx.HandlerFunc(h.revoke))
}

// Public registers the routes that need no session under /api.
func (h *Routes) Public(r chi.Router) {
	r.Method(http.MethodGet, "/public/reports/{report_id}", httpx.HandlerFunc(h.public))
	r.Method(http.MethodGet, "/public/report-keys", httpx.HandlerFunc(h.keys))
}

// Summary is a stored report as its project lists it.
type Summary struct {
	ID           string           `json:"id"`
	URL          string           `json:"url"`
	From         string           `json:"from"`
	To           string           `json:"to"`
	Signer       string           `json:"signer"`
	CreatedAt    httpx.Timestamp  `json:"created_at"`
	CreatedBy    *uuid.UUID       `json:"created_by"`
	RevokedAt    *httpx.Timestamp `json:"revoked_at"`
	ViewCount    int64            `json:"view_count"`
	LastViewedAt *httpx.Timestamp `json:"last_viewed_at"`
}

// Detail is one report with its signed file, for a member.
type Detail struct {
	Summary
	File File `json:"file"`
}

// hideCreator drops the member who signed the report for the public
// demo's viewer, which sees no person (contract 21.4).
func (s *Summary) hideCreator(a tenants.Access) {
	if a.Role == tenants.RoleViewer {
		s.CreatedBy = nil
	}
}

func (h *Routes) link(id string) string { return h.c.ConsoleOrigin + "/r/" + id }

const summaryCols = `id, period_from, period_to, signer, created_at, created_by, revoked_at, view_count, last_viewed_at`

func (h *Routes) scanSummary(row pgx.Row, extra ...any) (Summary, error) {
	var s Summary
	var from, to, created time.Time
	var revoked, viewed *time.Time
	err := row.Scan(append([]any{&s.ID, &from, &to, &s.Signer, &created, &s.CreatedBy, &revoked, &s.ViewCount, &viewed}, extra...)...)
	s.URL = h.link(s.ID)
	s.From, s.To = from.Format(time.DateOnly), to.Format(time.DateOnly)
	s.CreatedAt = httpx.Timestamp(created)
	if revoked != nil {
		t := httpx.Timestamp(*revoked)
		s.RevokedAt = &t
	}
	if viewed != nil {
		t := httpx.Timestamp(*viewed)
		s.LastViewedAt = &t
	}
	return s, err
}

// ReportList is the answer of GET /api/tenants/{tenant_id}/reports.
type ReportList struct {
	Items []Summary `json:"items"`
}

// MaxListed bounds the reports a list returns, newest first.
const MaxListed = 200

func (h *Routes) list(w http.ResponseWriter, r *http.Request) error {
	a, err := h.c.Scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	ctx := r.Context()
	out := ReportList{Items: []Summary{}}
	err = h.c.Scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select `+summaryCols+` from cp.usage_reports
			order by created_at desc, id limit $1`, MaxListed)
		if err != nil {
			return err
		}
		out.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) { return h.scanSummary(row) })
		return err
	})
	if err != nil {
		return fmt.Errorf("reports: list: %w", err)
	}
	if out.Items == nil {
		out.Items = []Summary{}
	}
	for i := range out.Items {
		out.Items[i].hideCreator(a)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

type createBody struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// FinalDays is how many days before today the last day of a report may
// lie at the latest. the web aggregation and integrity grading read today
// and the three days before it again on every run, so a day is final only
// once it is four days old; a signed number must never change afterwards.
const FinalDays = 4

// CodePeriodNotFinal is the 422 for a period that ends on a day that can
// still change.
const CodePeriodNotFinal = "period_not_final"

// LastFinalDay is the last day a report created on today may end on.
func LastFinalDay(today time.Time) time.Time { return today.AddDate(0, 0, -FinalDays) }

// period checks a report period: final utc days, so a signed number never
// changes after signing; to on or before LastFinalDay; at most
// MaxPeriodDays days.
func period(b createBody, today time.Time) (time.Time, time.Time, error) {
	var f tenants.Fields
	var from, to time.Time
	parse := func(name string, v *string) time.Time {
		if v == nil {
			f.Add(name, httpx.FieldRequired)
			return time.Time{}
		}
		d, err := time.Parse(time.DateOnly, *v)
		if err != nil {
			f.Add(name, httpx.FieldInvalidFormat)
		}
		return d
	}
	from, to = parse("from", b.From), parse("to", b.To)
	if err := f.Err(); err != nil {
		return from, to, err
	}
	if last := LastFinalDay(today); to.After(last) {
		return from, to, httpx.E(http.StatusUnprocessableEntity, CodePeriodNotFinal, fmt.Sprintf(
			"the period must end on %s or earlier: the last %d days can still change until the daily aggregation has read them for the last time",
			last.Format(time.DateOnly), FinalDays))
	}
	switch {
	case to.Before(from):
		f.Add("from", httpx.FieldInvalidValue)
	case int(to.Sub(from).Hours()/24)+1 > MaxPeriodDays:
		f.Add("from", httpx.FieldInvalidValue)
	}
	if err := f.Err(); err != nil {
		return from, to, httpx.Validation(fmt.Sprintf("the period must start on or before its end and hold at most %d days", MaxPeriodDays),
			map[string]string{"from": httpx.FieldInvalidValue, "to": httpx.FieldInvalidValue})
	}
	return from, to, nil
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Sign writes the canonical form of rep and its sep-53 signature with key.
func Sign(rep Report, key config.ReportKey) ([]byte, Signature, error) {
	body, err := canonjson.Marshal(rep)
	if err != nil {
		return nil, Signature{}, err
	}
	sig, err := chain.SignSEP53(key.Full(), body)
	if err != nil {
		return nil, Signature{}, err
	}
	return body, Signature{Scheme: SignatureScheme, Signer: key.Full().Address(), Value: sig}, nil
}

func (h *Routes) create(w http.ResponseWriter, r *http.Request) error {
	a, err := h.c.Scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	var b createBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	now := h.c.Now()
	from, to, err := period(b, utcDay(now))
	if err != nil {
		return err
	}
	ctx := r.Context()
	// the creation limit is read under cp_tenant, which sees only its own
	// project's reports: once here, so a refused request builds nothing,
	// and again under the project's lock in the insert transaction, so
	// concurrent requests cannot all pass it
	underLimit := func(tx pgx.Tx) error {
		var n int
		var oldest time.Time
		if err := tx.QueryRow(ctx, `select count(*), coalesce(min(created_at), now()) from cp.usage_reports
			where created_at > $1`, now.Add(-CreateWindow)).Scan(&n, &oldest); err != nil {
			return err
		}
		if n >= CreateLimit {
			return httpx.RateLimited(max(oldest.Add(CreateWindow).Sub(now), time.Second))
		}
		return nil
	}
	if err := h.c.Scope.InTenant(ctx, a, underLimit); err != nil {
		return err
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	issuer := Issuer{Name: "Veristar", Console: h.c.ConsoleOrigin, StellarTOML: h.c.ConsoleOrigin + StellarTOMLPath,
		SigningKey: h.c.Signing.Address}
	var rep Report
	err = h.c.DB.WithOperator(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set transaction isolation level repeatable read, read only`); err != nil {
			return err
		}
		var err error
		rep, err = Build(ctx, tx, id, a.TenantID, from, to, now, issuer, h.c.Platform)
		return err
	})
	if errors.Is(err, ErrNotListed) {
		return httpx.Conflict(CodeNotReportable, "this project is not listed in the operator views and has no report")
	}
	if err != nil {
		return fmt.Errorf("reports: build: %w", err)
	}
	body, sig, err := Sign(rep, h.c.Signing.Key)
	if err != nil {
		return fmt.Errorf("reports: sign: %w", err)
	}
	var out Summary
	err = h.c.Scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.reports:' || $1::text, 0))`, a.TenantID.String()); err != nil {
			return err
		}
		if err := underLimit(tx); err != nil {
			return err
		}
		var err error
		out, err = h.scanSummary(tx.QueryRow(ctx, `insert into cp.usage_reports
			(id, tenant_id, period_from, period_to, body, signature, signer, created_by)
			values ($1, $2, $3, $4, $5, $6, $7, $8) returning `+summaryCols,
			id, a.TenantID, from, to, string(body), sig.Value, sig.Signer, a.User.ID))
		if err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionCreate, Target: Target(id),
			Detail: map[string]any{"tenant_id": a.TenantID.String(), "from": out.From, "to": out.To, "signer": sig.Signer},
		})
	})
	if err != nil {
		return fmt.Errorf("reports: store: %w", err)
	}
	d := Detail{Summary: out, File: File{Report: body, Signature: sig}}
	w.Header().Set("Location", "/api/tenants/"+a.TenantID.String()+"/reports/"+id)
	writeWithReport(w, http.StatusCreated, &d, &d.File)
	return nil
}

// reportSlot stands in for the report member while the answer around it
// is encoded: encoding/json escapes u+2028 and u+2029 even inside a raw
// message, so the signed bytes are put back verbatim afterwards. no other
// member can hold the slot, since it holds nul characters no server-built
// value has.
var reportSlot = []byte(`"\u0000signed report\u0000"`)

// writeWithReport writes v, whose file is f, with f's report member as the
// exact stored bytes, so the served member is byte for byte what was signed.
func writeWithReport(w http.ResponseWriter, status int, v any, f *File) {
	signed := f.Report
	f.Report = reportSlot
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	f.Report = signed
	if err != nil || bytes.Count(buf.Bytes(), reportSlot) != 1 {
		http.Error(w, `{"error":{"code":"internal","message":"internal error","request_id":""}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.Replace(buf.Bytes(), reportSlot, signed, 1))
}

// CodeNotReportable is the 409 of a project the operator views do not list.
const CodeNotReportable = "not_reportable"

func reportID(r *http.Request) (string, error) {
	id := chi.URLParam(r, "report_id")
	if !idPattern.MatchString(id) {
		return "", httpx.NotFound()
	}
	return id, nil
}

func (h *Routes) get(w http.ResponseWriter, r *http.Request) error {
	a, err := h.c.Scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := reportID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out Detail
	err = h.c.Scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var body string
		var sig string
		out.Summary, err = h.scanSummary(tx.QueryRow(ctx, `select `+summaryCols+`, body, signature
			from cp.usage_reports where id = $1`, id), &body, &sig)
		out.File = File{Report: json.RawMessage(body), Signature: Signature{Scheme: SignatureScheme, Signer: out.Signer, Value: sig}}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if err != nil {
		return fmt.Errorf("reports: get: %w", err)
	}
	out.hideCreator(a)
	writeWithReport(w, http.StatusOK, &out, &out.File)
	return nil
}

func (h *Routes) revoke(w http.ResponseWriter, r *http.Request) error {
	a, err := h.c.Scope.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	id, err := reportID(r)
	if err != nil {
		return err
	}
	if r.ContentLength > 0 {
		return httpx.BadBody("this route takes no body")
	}
	ctx := r.Context()
	now := h.c.Now()
	var out Summary
	err = h.c.Scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		// one revocation at a time per project, so the count below holds
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.reports:' || $1::text, 0))`, a.TenantID.String()); err != nil {
			return err
		}
		var n int
		var oldest time.Time
		if err := tx.QueryRow(ctx, `select count(*), coalesce(min(revoked_at), now()) from cp.usage_reports
			where revoked_at > $1`, now.Add(-RevokeWindow)).Scan(&n, &oldest); err != nil {
			return err
		}
		if n >= RevokeLimit {
			return httpx.RateLimited(max(oldest.Add(RevokeWindow).Sub(now), time.Second))
		}
		cur, err := h.scanSummary(tx.QueryRow(ctx, `select `+summaryCols+` from cp.usage_reports where id = $1 for update`, id))
		if err != nil {
			return err
		}
		if cur.RevokedAt != nil {
			// revoking twice changes nothing and writes no audit row
			out = cur
			return nil
		}
		out, err = h.scanSummary(tx.QueryRow(ctx, `update cp.usage_reports set revoked_at = now(), revoked_by = $2
			where id = $1 returning `+summaryCols, id, a.User.ID))
		if err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: ActionRevoke, Target: Target(id),
			Detail: map[string]any{"tenant_id": a.TenantID.String()},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// PublicReport is the answer of GET /api/public/reports/{report_id}: the
// signed file, when it was generated, and the platform's keys, so the
// page checks the signer against them.
type PublicReport struct {
	ID        string          `json:"id"`
	URL       string          `json:"url"`
	CreatedAt httpx.Timestamp `json:"created_at"`
	File      File            `json:"file"`
	Keys      Keys            `json:"keys"`
}

// errRevoked ends the public read of a revoked link before it counts a
// view or writes an audit row.
var errRevoked = errors.New("reports: the link is revoked")

// public answers one report by id to anyone. an unknown id and a removed
// project answer the same 404, so the route says nothing about which ids
// exist; a revoked link answers 410 report_revoked with the bare error
// envelope, so a reader holding it learns that it was revoked and nothing
// about the project. both cases run the same single read. the read, the
// view count and the audit row are one cp_system transaction that names
// this one id and nothing else.
func (h *Routes) public(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	now := h.c.Now()
	if ok, wait := h.limiter.take(now); !ok {
		return httpx.RateLimited(wait)
	}
	id, err := reportID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out PublicReport
	err = h.c.DB.WithSystem(ctx, func(tx pgx.Tx) error {
		var tenant uuid.UUID
		var body, sig, signer string
		var created time.Time
		var revoked bool
		err := tx.QueryRow(ctx, `select u.tenant_id, u.body, u.signature, u.signer, u.created_at, u.revoked_at is not null
			from cp.usage_reports u join cp.tenants t on t.id = u.tenant_id
			where u.id = $1 and t.status <> 'removed'
			for update of u`, id).Scan(&tenant, &body, &sig, &signer, &created, &revoked)
		if err != nil {
			return err
		}
		if revoked {
			return errRevoked
		}
		var n int
		var oldest time.Time
		if err := tx.QueryRow(ctx, `select count(*), coalesce(min(at), now()) from cp.audit_log
			where target = $1 and action = $2 and at > $3`, Target(id), ActionView, now.Add(-ViewWindow)).Scan(&n, &oldest); err != nil {
			return err
		}
		if n >= ViewLimit {
			return httpx.RateLimited(max(oldest.Add(ViewWindow).Sub(now), time.Second))
		}
		if _, err := tx.Exec(ctx, `update cp.usage_reports set view_count = view_count + 1, last_viewed_at = now()
			where id = $1`, id); err != nil {
			return err
		}
		out = PublicReport{ID: id, URL: h.link(id), CreatedAt: httpx.Timestamp(created),
			File: File{Report: json.RawMessage(body), Signature: Signature{Scheme: SignatureScheme, Signer: signer, Value: sig}},
			Keys: h.Keys()}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: PublicActor, Action: ActionView, Target: Target(id), Detail: map[string]any{"tenant_id": tenant.String()},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound()
	}
	if errors.Is(err, errRevoked) {
		return httpx.E(http.StatusGone, httpx.CodeReportRevoked, "this report link was revoked")
	}
	if err != nil {
		return err
	}
	writeWithReport(w, http.StatusOK, &out, &out.File)
	return nil
}

func (h *Routes) keys(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) > 0 {
		return httpx.BadQuery("this route takes no query parameters")
	}
	httpx.WriteJSON(w, http.StatusOK, h.Keys())
	return nil
}

// bucket is one token bucket for the public route of this instance.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.last.IsZero() {
		if el := now.Sub(b.last); el > 0 {
			b.tokens = min(b.burst, b.tokens+el.Seconds()*b.rate)
		}
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
}
