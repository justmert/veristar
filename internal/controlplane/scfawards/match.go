package scfawards

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// match states of an award
const (
	StateMatched     = "matched"
	StateAmbiguous   = "ambiguous"
	StateNotEnrolled = "not_enrolled"
)

// evidence kinds: what tied an award to a project
const (
	EvidenceSiteDomain        = "site_domain"
	EvidenceSiteOrigin        = "site_origin"
	EvidenceEnrollmentWebsite = "enrollment_website"
	EvidenceOnchainID         = "onchain_id"
)

// Evidence is one exact fact that ties an award to a project.
type Evidence struct {
	Kind string `json:"kind"`
	// Value is the matching host or strkey
	Value string `json:"value"`
	// SiteID names the site of a site_domain or site_origin match
	SiteID *uuid.UUID `json:"site_id"`
	// Verified is true for a site whose domain the project proved and for
	// every on-chain id (only verified ids count); false for an enrollment
	// website and for a site not verified
	Verified bool `json:"verified"`
}

// Tenant is an enrolled project and its evidence.
type Tenant struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	Status    string
	CreatedAt time.Time
	// hosts maps a normalised host to what names it
	hosts map[string][]Evidence
	// ids are the verified on-chain ids
	ids map[string]bool
}

// AwardRow is a stored award.
type AwardRow struct {
	ID              uuid.UUID
	Project         string
	Round           string
	Category        string
	Program         string
	AwardDate       *time.Time
	AmountCents     int64
	TranchePercent  *int
	TrancheStatus   *string
	WebsiteHost     *string
	Strkeys         []string
	FirstImportedAt time.Time
	ImportedAt      time.Time
}

// Candidate is a project an award points to, with the evidence.
type Candidate struct {
	TenantID uuid.UUID
	Evidence []Evidence
}

// Match is the match of one award.
type Match struct {
	State string
	// Candidates are the projects the award points to: one for a match,
	// several (or one shared with other awards) when ambiguous
	Candidates []Candidate
	// SharedWith are the other awards pointing to the same project, which
	// make a single candidate ambiguous
	SharedWith []uuid.UUID
}

// Tenant returns the matched project, or uuid.Nil unless State is matched.
func (m Match) Tenant() uuid.UUID {
	if m.State != StateMatched {
		return uuid.Nil
	}
	return m.Candidates[0].TenantID
}

// enrolledStatuses are the statuses of projects an award can match: every
// enrolled project an operator still works with
var enrolledStatuses = []string{"pending", "active", "suspended"}

// LoadTenants reads every enrolled project that is not internal, with its
// evidence: the domains and origin hosts of its sites that are not
// removed, its enrollment website and its verified on-chain ids. it reads
// cp.operator_sites, so it runs under cp_system and cp_operator alike.
func LoadTenants(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]*Tenant, error) {
	out := map[uuid.UUID]*Tenant{}
	rows, err := tx.Query(ctx, `select id, slug, name, status, created_at, coalesce(enrollment_answers ->> 'website', '')
		from cp.tenants where not is_internal and status = any($1::text[])`, enrolledStatuses)
	if err != nil {
		return nil, fmt.Errorf("scfawards: tenants: %w", err)
	}
	for rows.Next() {
		t := &Tenant{hosts: map[string][]Evidence{}, ids: map[string]bool{}}
		var website string
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.CreatedAt, &website); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scfawards: tenants: %w", err)
		}
		if h, ok := NormalizeHost(website); ok {
			t.add(h, Evidence{Kind: EvidenceEnrollmentWebsite, Value: h})
		}
		out[t.ID] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfawards: tenants: %w", err)
	}
	rows, err = tx.Query(ctx, `select s.id, s.tenant_id, s.domain, s.origins,
		       s.verified_at is not null and s.last_verify_error is null
		from cp.operator_sites s where s.status <> 'removed' order by s.created_at, s.id`)
	if err != nil {
		return nil, fmt.Errorf("scfawards: sites: %w", err)
	}
	for rows.Next() {
		var id, tenant uuid.UUID
		var domain string
		var origins []string
		var verified bool
		if err := rows.Scan(&id, &tenant, &domain, &origins, &verified); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scfawards: sites: %w", err)
		}
		t := out[tenant]
		if t == nil {
			continue
		}
		site := id
		if h, ok := NormalizeHost(domain); ok {
			t.add(h, Evidence{Kind: EvidenceSiteDomain, Value: h, SiteID: &site, Verified: verified})
		}
		for _, o := range origins {
			if h, ok := NormalizeHost(o); ok {
				t.add(h, Evidence{Kind: EvidenceSiteOrigin, Value: h, SiteID: &site, Verified: verified})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfawards: sites: %w", err)
	}
	rows, err = tx.Query(ctx, `select tenant_id, strkey from cp.chain_ids where verified_at is not null`)
	if err != nil {
		return nil, fmt.Errorf("scfawards: chain ids: %w", err)
	}
	for rows.Next() {
		var tenant uuid.UUID
		var k string
		if err := rows.Scan(&tenant, &k); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scfawards: chain ids: %w", err)
		}
		if t := out[tenant]; t != nil {
			t.ids[k] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfawards: chain ids: %w", err)
	}
	return out, nil
}

// add records evidence for a host once per kind and site.
func (t *Tenant) add(h string, e Evidence) {
	for _, x := range t.hosts[h] {
		if x.Kind == e.Kind && (x.SiteID == nil) == (e.SiteID == nil) && (x.SiteID == nil || *x.SiteID == *e.SiteID) {
			return
		}
	}
	t.hosts[h] = append(t.hosts[h], e)
}

// NewTenant builds a project with evidence, for tests and callers that
// read the evidence themselves.
func NewTenant(id uuid.UUID, slug string) *Tenant {
	return &Tenant{ID: id, Slug: slug, Name: slug, Status: "active", hosts: map[string][]Evidence{}, ids: map[string]bool{}}
}

// AddHost adds a host evidence of the given kind; raw is normalised as
// every host is.
func (t *Tenant) AddHost(kind, raw string, site *uuid.UUID, verified bool) {
	if h, ok := NormalizeHost(raw); ok {
		t.add(h, Evidence{Kind: kind, Value: h, SiteID: site, Verified: verified})
	}
}

// AddVerifiedID adds a verified on-chain id.
func (t *Tenant) AddVerifiedID(k string) { t.ids[k] = true }

// awardColumns are the columns LoadAwards and scanAward read
const awardColumns = `id, project, round, category, program, award_date, (award_amount * 100)::bigint,
	tranche_percent, tranche_status, website_host, strkeys, first_imported_at, imported_at`

func scanAward(row pgx.Row) (AwardRow, error) {
	var a AwardRow
	var pct *int16
	err := row.Scan(&a.ID, &a.Project, &a.Round, &a.Category, &a.Program, &a.AwardDate, &a.AmountCents,
		&pct, &a.TrancheStatus, &a.WebsiteHost, &a.Strkeys, &a.FirstImportedAt, &a.ImportedAt)
	if pct != nil {
		v := int(*pct)
		a.TranchePercent = &v
	}
	return a, err
}

// LoadAwards reads every award, in round order (newest first) and then by
// project.
func LoadAwards(ctx context.Context, tx pgx.Tx) ([]AwardRow, error) {
	rows, err := tx.Query(ctx, `select `+awardColumns+` from cp.scf_awards
		order by substr(round, 6)::int desc, lower(project), id`)
	if err != nil {
		return nil, fmt.Errorf("scfawards: awards: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AwardRow, error) { return scanAward(r) })
	if err != nil {
		return nil, fmt.Errorf("scfawards: awards: %w", err)
	}
	return out, nil
}

// MatchAll matches every award to the projects on exact evidence only: the
// award's website host equals a host of the project's sites (domain or an
// origin) or of its enrollment website, or one of the award's strkeys is a
// verified on-chain id of the project. names are never compared, and a
// host many organisations share is never evidence. an award that points
// to several projects, or to a project another award also points to, is
// ambiguous and matches nothing.
func MatchAll(awards []AwardRow, tenants map[uuid.UUID]*Tenant) map[uuid.UUID]Match {
	ids := make([]uuid.UUID, 0, len(tenants))
	for id := range tenants {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	cands := make(map[uuid.UUID][]Candidate, len(awards))
	byTenant := map[uuid.UUID][]uuid.UUID{}
	for _, a := range awards {
		for _, id := range ids {
			t := tenants[id]
			var ev []Evidence
			if a.WebsiteHost != nil && !SharedHost(*a.WebsiteHost) {
				ev = append(ev, t.hosts[*a.WebsiteHost]...)
			}
			for _, k := range a.Strkeys {
				if t.ids[k] {
					ev = append(ev, Evidence{Kind: EvidenceOnchainID, Value: k, Verified: true})
				}
			}
			if len(ev) > 0 {
				sortEvidence(ev)
				cands[a.ID] = append(cands[a.ID], Candidate{TenantID: id, Evidence: ev})
				byTenant[id] = append(byTenant[id], a.ID)
			}
		}
	}
	out := make(map[uuid.UUID]Match, len(awards))
	for _, a := range awards {
		c := cands[a.ID]
		m := Match{State: StateNotEnrolled, Candidates: c}
		switch {
		case len(c) > 1:
			m.State = StateAmbiguous
		case len(c) == 1:
			m.State = StateMatched
			for _, other := range byTenant[c[0].TenantID] {
				if other != a.ID {
					m.SharedWith = append(m.SharedWith, other)
				}
			}
			if len(m.SharedWith) > 0 {
				m.State = StateAmbiguous
			}
		}
		if m.Candidates == nil {
			m.Candidates = []Candidate{}
		}
		out[a.ID] = m
	}
	return out
}

// evidenceRank orders evidence from the strongest kind
var evidenceRank = map[string]int{EvidenceSiteDomain: 0, EvidenceSiteOrigin: 1, EvidenceOnchainID: 2, EvidenceEnrollmentWebsite: 3}

func sortEvidence(ev []Evidence) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ri, rj := evidenceRank[ev[i].Kind], evidenceRank[ev[j].Kind]; ri != rj {
			return ri < rj
		}
		if ev[i].Verified != ev[j].Verified {
			return ev[i].Verified
		}
		return ev[i].Value < ev[j].Value
	})
}
