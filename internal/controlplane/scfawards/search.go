package scfawards

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// the award search of the enrollment form (control-plane-api 4.2.7): a
// signed-in person types a project name or website and picks the project's
// award, and the form fills the cohort, the website and the stellar ids. a
// result holds what the form fills and nothing else.

// search limits
const (
	// MaxResults is the most projects one search returns
	MaxResults = 10
	// SearchLimit is the searches one person makes per minute
	SearchLimit  = 30
	searchWindow = time.Minute
	minQuery     = 2
	maxQuery     = 100
)

// Result is one project of a search: its most recent award, and the rounds
// of its earlier awards.
type Result struct {
	ID         uuid.UUID `json:"id"`
	Project    string    `json:"project"`
	Round      string    `json:"round"`
	Category   string    `json:"category"`
	Program    string    `json:"program"`
	AwardDate  *string   `json:"award_date"`
	Website    *string   `json:"website"`
	StellarIDs []string  `json:"stellar_ids"`
	Rounds     []string  `json:"rounds"`
}

// SearchAnswer is the answer of GET /api/scf/awards/search.
type SearchAnswer struct {
	Items []Result `json:"items"`
}

// Search serves the award search.
type Search struct {
	db  *db.DB
	now func() time.Time

	mu     sync.Mutex
	counts map[uuid.UUID]*searchCount
}

type searchCount struct {
	start time.Time
	n     int
}

// NewSearch needs the database with the cp_system pool and a clock.
func NewSearch(d *db.DB, now func() time.Time) (*Search, error) {
	if d == nil || now == nil {
		return nil, errors.New("scfawards: the search needs a database and a clock")
	}
	return &Search{db: d, now: now, counts: map[uuid.UUID]*searchCount{}}, nil
}

// Routes registers the path under the session router mounted at /api.
func (s *Search) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/scf/awards/search", httpx.HandlerFunc(s.search))
}

// allow counts one search of user in a fixed one-minute window.
func (s *Search) allow(user uuid.UUID, now time.Time) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.counts {
		if now.Sub(c.start) >= searchWindow {
			delete(s.counts, id)
		}
	}
	c := s.counts[user]
	if c == nil {
		c = &searchCount{start: now}
		s.counts[user] = c
	}
	c.n++
	return c.n <= SearchLimit, max(searchWindow-now.Sub(c.start), time.Second)
}

func (s *Search) search(w http.ResponseWriter, r *http.Request) error {
	u, err := session.MustUser(r.Context())
	if err != nil {
		return err
	}
	q := r.URL.Query()
	for k := range q {
		if k != "q" {
			return httpx.BadQuery("unknown query parameter " + k)
		}
	}
	if len(q["q"]) != 1 {
		return httpx.BadQuery("q is required, once")
	}
	term := strings.Join(strings.Fields(q.Get("q")), " ")
	if n := utf8.RuneCountInString(term); n < minQuery || n > maxQuery || strings.ContainsRune(term, 0) {
		return httpx.BadQuery(fmt.Sprintf("q must be %d to %d characters", minQuery, maxQuery))
	}
	if ok, wait := s.allow(u.ID, s.now()); !ok {
		return httpx.RateLimited(wait)
	}
	var awards []AwardRow
	err = s.db.WithSystem(r.Context(), func(tx pgx.Tx) error {
		var err error
		awards, err = LoadAwards(r.Context(), tx)
		return err
	})
	if err != nil {
		return fmt.Errorf("scfawards: search: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, SearchAnswer{Items: Find(awards, term)})
	return nil
}

// projectKey groups the awards of one project: the website host, the
// same evidence the matching uses, or the name when the award has no host
// of its own.
func projectKey(a AwardRow) string {
	if a.WebsiteHost != nil && !SharedHost(*a.WebsiteHost) {
		return "host:" + *a.WebsiteHost
	}
	return "name:" + strings.ToLower(a.Project)
}

// Find returns the projects whose name or website host holds term, case
// insensitive: one result per project from its most recent award, names
// that start with term first, then by name, at most MaxResults.
func Find(awards []AwardRow, term string) []Result {
	term = strings.ToLower(term)
	groups := map[string][]AwardRow{}
	var keys []string
	for _, a := range awards {
		k := projectKey(a)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], a)
	}
	type hit struct {
		r      Result
		prefix bool
	}
	var hits []hit
	for _, k := range keys {
		g := groups[k]
		found, prefix := false, false
		for _, a := range g {
			name := strings.ToLower(a.Project)
			if strings.Contains(name, term) || a.WebsiteHost != nil && strings.Contains(*a.WebsiteHost, term) {
				found = true
			}
			prefix = prefix || strings.HasPrefix(name, term)
		}
		if !found {
			continue
		}
		best := g[0]
		for _, a := range g[1:] {
			if newer(a, best) {
				best = a
			}
		}
		res := Result{ID: best.ID, Project: best.Project, Round: best.Round, Category: best.Category, Program: best.Program,
			AwardDate: dateString(best.AwardDate), StellarIDs: []string{}, Rounds: []string{}}
		if best.WebsiteHost != nil {
			site := "https://" + *best.WebsiteHost
			res.Website = &site
		}
		ids, rounds := map[string]bool{}, map[string]bool{}
		for _, a := range g {
			for _, id := range a.Strkeys {
				ids[id] = true
			}
			if a.ID != best.ID && a.Round != best.Round {
				rounds[a.Round] = true
			}
		}
		for id := range ids {
			res.StellarIDs = append(res.StellarIDs, id)
		}
		sort.Strings(res.StellarIDs)
		for r := range rounds {
			res.Rounds = append(res.Rounds, r)
		}
		sort.Slice(res.Rounds, func(i, j int) bool { return RoundNumber(res.Rounds[i]) > RoundNumber(res.Rounds[j]) })
		hits = append(hits, hit{r: res, prefix: prefix})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].prefix != hits[j].prefix {
			return hits[i].prefix
		}
		return strings.ToLower(hits[i].r.Project) < strings.ToLower(hits[j].r.Project)
	})
	out := make([]Result, 0, min(len(hits), MaxResults))
	for _, h := range hits {
		if len(out) == MaxResults {
			break
		}
		out = append(out, h.r)
	}
	return out
}
