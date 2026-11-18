package scfawards

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

func TestFind(t *testing.T) {
	d := func(s string) *time.Time { v, _ := time.Parse(time.DateOnly, s); return &v }
	g := account()
	old, recent := award("Lumen Pay", "lumenpay.example", g), award("LumenPay", "lumenpay.example")
	old.Round, old.AwardDate = "SCF #31", d("2025-01-10")
	recent.Round, recent.AwardDate, recent.Category = "SCF #44", d("2026-06-01"), "Financial Protocols"
	other := award("Pay Lumens", "paylumens.example")
	hub := award("Hubbed", "github.com")
	awards := []AwardRow{old, recent, other, hub, award("Unrelated", "unrelated.example")}

	got := Find(awards, "LUMEN")
	require.Len(t, got, 2)
	r := got[0]
	require.Equal(t, recent.ID, r.ID, "one result per project, from its most recent award")
	require.Equal(t, "LumenPay", r.Project)
	require.Equal(t, "SCF #44", r.Round)
	require.Equal(t, "Financial Protocols", r.Category)
	require.Equal(t, "2026-06-01", *r.AwardDate)
	require.Equal(t, "https://lumenpay.example", *r.Website)
	require.Equal(t, []string{g}, r.StellarIDs, "ids of every award of the project")
	require.Equal(t, []string{"SCF #31"}, r.Rounds)
	require.Equal(t, "Pay Lumens", got[1].Project, "a name that starts with the term comes first")

	require.Len(t, Find(awards, "paylumens.ex"), 1, "the website host is searched")
	require.Len(t, Find(awards, "github"), 1)
	var many []AwardRow
	for i := 0; i < 15; i++ {
		many = append(many, award("Many "+string(rune('a'+i)), ""))
	}
	require.Len(t, Find(many, "many"), MaxResults)
}

// the search route: a session, a query of 2 to 100 characters, the per
// person limit, and results holding exactly the fields the form fills
func TestSearchRoute(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	r1, r2 := good("Lumenpay"), good("Lumenpay")
	r2.round, r2.date = "SCF #38", "1/5/2025"
	r1.traction = "Treasury " + account()
	_, err := Import(ctx, f.d, encode(t, '\t', nil, r1, r2, good("Other")), ActorPlatformctl, time.Now())
	require.NoError(t, err)

	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s, err := NewSearch(f.d, func() time.Time { return now })
	require.NoError(t, err)
	user := uuid.Must(uuid.NewV7())
	signedIn := chi.NewRouter()
	signedIn.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(session.WithUser(r.Context(), session.User{ID: user})))
		})
	})
	signedIn.Route("/api", s.Routes)
	anonymous := chi.NewRouter()
	anonymous.Route("/api", s.Routes)
	get := func(h http.Handler, q string) (int, []byte) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/scf/awards/search"+q, nil))
		return w.Code, w.Body.Bytes()
	}

	code, _ := get(anonymous, "?q=lumen")
	require.Equal(t, http.StatusUnauthorized, code)
	for _, q := range []string{"", "?q=l", "?q=%20l%20", "?q=a&q=b", "?q=lumen&x=1"} {
		code, _ := get(signedIn, q)
		require.Equal(t, http.StatusBadRequest, code, q)
	}

	code, body := get(signedIn, "?q=lumen")
	require.Equal(t, http.StatusOK, code, string(body))
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Len(t, raw.Items, 1)
	var keys []string
	for k := range raw.Items[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"award_date", "category", "id", "program", "project", "round", "rounds", "stellar_ids", "website"}, keys,
		"no amount, tranche, status or count")
	require.Equal(t, "SCF #45", raw.Items[0]["round"])
	require.Equal(t, []any{"SCF #38"}, raw.Items[0]["rounds"])
	require.Equal(t, "https://lumenpay.example", raw.Items[0]["website"])
	require.Len(t, raw.Items[0]["stellar_ids"], 1)

	// the limit is per person and per minute; the earlier searches of
	// this test count, invalid queries do not
	for i := 2; i <= SearchLimit; i++ {
		code, _ := get(signedIn, "?q=other")
		require.Equal(t, http.StatusOK, code, i)
	}
	w := httptest.NewRecorder()
	signedIn.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/scf/awards/search?q=other", nil))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	now = now.Add(time.Minute)
	code, _ = get(signedIn, "?q=other")
	require.Equal(t, http.StatusOK, code, "a new minute")
}

// an enrollment that names an award proposes the award's cohort as the
// member's own; an unknown award is 422 and enrolls nothing
func TestEnrollWithAward(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	r := good("Lumenpay")
	r.category = "Financial Protocols"
	_, err := Import(ctx, f.d, encode(t, '\t', nil, r), ActorPlatformctl, time.Now())
	require.NoError(t, err)
	awardID := uuid.MustParse(f.query(t, `select id::text from cp.scf_awards`))

	user := uuid.Must(uuid.NewV7())
	f.exec(t, `insert into cp.users (id, github_id, github_login) values ($1, 7101, 'enroller')`, user)
	scope, err := tenants.NewScope(f.d, "t1", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error { return nil }),
		slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	hooks, err := NewHooks(f.d, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	h := chi.NewRouter()
	h.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(session.WithUser(r.Context(), session.User{ID: user})))
		})
	})
	h.Route("/api", tenants.NewRoutes(scope).OnEnroll(hooks).Routes)
	enroll := func(slug string, extra map[string]any) (int, map[string]any) {
		body := map[string]any{"name": slug, "slug": slug, "accepted_terms_version": "t1",
			"enrollment": map[string]any{"website": "https://lumenpay.example", "stellar_usage": "payments"}}
		for k, v := range extra {
			body[k] = v
		}
		b, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/tenants", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}

	code, m := enroll("unknown-award", map[string]any{"scf_award_id": uuid.NewString()})
	require.Equal(t, http.StatusUnprocessableEntity, code, m)
	require.Contains(t, m["error"].(map[string]any)["fields"], "scf_award_id")
	require.Equal(t, "unknown_scf_award", m["error"].(map[string]any)["fields"].(map[string]any)["scf_award_id"])
	code, _ = enroll("both", map[string]any{"scf_award_id": awardID.String(), "cohort": map[string]any{"round": "SCF #1"}})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	code, _ = enroll("bad-id", map[string]any{"scf_award_id": "nope"})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Equal(t, "0", f.query(t, `select count(*)::text from cp.tenants`))

	code, m = enroll("lumenpay", map[string]any{"scf_award_id": awardID.String()})
	require.Equal(t, http.StatusCreated, code, m)
	id := uuid.MustParse(m["id"].(string))
	c := f.cohort(t, id)
	require.Nil(t, c.Confirmed)
	require.Equal(t, SourceMember, *c.ProposalSource, "the member's own proposal")
	require.Equal(t, Cohort{Program: strp("Build Award"), Round: strp("SCF #45"), Category: strp("Financial Protocols"),
		AwardDate: strp("2026-09-01")}, *c.Proposal)
	require.Equal(t, user.String(), f.query(t, `select proposed_by::text from cp.tenant_cohorts where tenant_id = $1`, id))
	// the matching in the same transaction found the member's proposal
	// and wrote nothing of its own
	require.Empty(t, f.audits(t, ActionCohortPropose))
	require.Equal(t, awardID.String(), f.query(t, `select detail ->> 'scf_award_id' from cp.audit_log where action = 'tenant.create'`))
}
