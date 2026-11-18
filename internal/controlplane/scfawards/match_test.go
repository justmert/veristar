package scfawards

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func award(project, host string, ids ...string) AwardRow {
	a := AwardRow{ID: uuid.Must(uuid.NewV7()), Project: project, Round: "SCF #40", Category: "Applications",
		Program: ProgramBuildAward, Strkeys: ids}
	if host != "" {
		a.WebsiteHost = &host
	}
	return a
}

func TestMatchAll(t *testing.T) {
	site := uuid.Must(uuid.NewV7())
	verifiedID, unverifiedID := account(), account()

	alpha := NewTenant(uuid.Must(uuid.NewV7()), "alpha")
	alpha.AddHost(EvidenceSiteDomain, "WWW.Alpha.Example.", &site, true)
	beta := NewTenant(uuid.Must(uuid.NewV7()), "beta")
	beta.AddHost(EvidenceSiteOrigin, "https://app.beta.example:8443", &site, false)
	beta.AddHost(EvidenceSiteOrigin, "http://localhost:3000", &site, false)
	gamma := NewTenant(uuid.Must(uuid.NewV7()), "gamma")
	gamma.AddHost(EvidenceEnrollmentWebsite, "https://gamma.example/about", nil, false)
	gamma.AddVerifiedID(verifiedID)
	// two projects claim the same host
	d1, d2 := NewTenant(uuid.Must(uuid.NewV7()), "d1"), NewTenant(uuid.Must(uuid.NewV7()), "d2")
	d1.AddHost(EvidenceEnrollmentWebsite, "https://shared.example", nil, false)
	d1.AddHost(EvidenceSiteDomain, "d1-only.example", &site, true)
	d2.AddHost(EvidenceSiteDomain, "shared.example", &site, true)
	// a project named exactly as an award, without any evidence
	named := NewTenant(uuid.Must(uuid.NewV7()), "Nameonly")
	// a project on a code hosting site
	hub := NewTenant(uuid.Must(uuid.NewV7()), "hub")
	hub.AddHost(EvidenceEnrollmentWebsite, "https://github.com/hub-org", nil, false)
	// one project, two awards
	twice := NewTenant(uuid.Must(uuid.NewV7()), "twice")
	twice.AddHost(EvidenceSiteDomain, "twice.example", &site, true)

	tenants := map[uuid.UUID]*Tenant{}
	for _, x := range []*Tenant{alpha, beta, gamma, d1, d2, named, hub, twice} {
		tenants[x.ID] = x
	}
	aAlpha := award("Alpha", "alpha.example")
	aBeta := award("Beta", "app.beta.example")
	aGamma := award("Gamma by id", "", verifiedID)
	aUnverified := award("Unverified", "", unverifiedID)
	aShared := award("Shared", "shared.example")
	aName := award("Nameonly", "nameonly-elsewhere.example")
	aHub := award("Hub", "github.com")
	aTwice1, aTwice2 := award("Twice", "twice.example"), award("Twice", "twice.example")
	aTwice2.Round = "SCF #44"
	aSub := award("Sub", "beta.example")
	aD1 := award("D1 only", "d1-only.example")
	awards := []AwardRow{aAlpha, aBeta, aGamma, aUnverified, aShared, aName, aHub, aTwice1, aTwice2, aSub, aD1}

	m := MatchAll(awards, tenants)
	require.Equal(t, StateMatched, m[aAlpha.ID].State)
	require.Equal(t, alpha.ID, m[aAlpha.ID].Tenant())
	require.Equal(t, []Evidence{{Kind: EvidenceSiteDomain, Value: "alpha.example", SiteID: &site, Verified: true}},
		m[aAlpha.ID].Candidates[0].Evidence)
	require.Equal(t, beta.ID, m[aBeta.ID].Tenant(), "an origin host counts")
	require.Equal(t, StateNotEnrolled, m[aSub.ID].State, "a parent domain is not the same host")
	require.Equal(t, gamma.ID, m[aGamma.ID].Tenant())
	require.Equal(t, EvidenceOnchainID, m[aGamma.ID].Candidates[0].Evidence[0].Kind)
	require.Equal(t, StateNotEnrolled, m[aUnverified.ID].State, "only verified ids count")
	require.Equal(t, StateAmbiguous, m[aShared.ID].State)
	require.Len(t, m[aShared.ID].Candidates, 2)
	require.Equal(t, uuid.Nil, m[aShared.ID].Tenant())
	require.Equal(t, StateNotEnrolled, m[aName.ID].State, "names are never compared")
	require.Equal(t, StateNotEnrolled, m[aHub.ID].State, "a shared host is never evidence")
	require.True(t, m[aAlpha.ID].Primary, "a single award is its project's most recent")
	// one project funded in two rounds: both match, the later round is the
	// primary award, each lists the other
	require.Equal(t, StateMatched, m[aTwice1.ID].State)
	require.Equal(t, StateMatched, m[aTwice2.ID].State)
	require.Equal(t, twice.ID, m[aTwice1.ID].Tenant())
	require.False(t, m[aTwice1.ID].Primary)
	require.True(t, m[aTwice2.ID].Primary)
	require.Equal(t, aTwice2.ID, m[aTwice1.ID].PrimaryAward)
	require.Equal(t, []uuid.UUID{aTwice2.ID}, m[aTwice1.ID].OtherAwards)
	require.Equal(t, []uuid.UUID{aTwice1.ID}, m[aTwice2.ID].OtherAwards)
	// d1 is also a candidate of an award that names d2: its own award is
	// ambiguous too
	require.Equal(t, StateAmbiguous, m[aD1.ID].State)
	require.Equal(t, []uuid.UUID{aShared.ID}, m[aD1.ID].SharedWith)
	require.Equal(t, []uuid.UUID{aD1.ID}, m[aShared.ID].SharedWith)
}

func TestCohortState(t *testing.T) {
	a := award("A", "a.example")
	day := today
	a.AwardDate = &day
	other := uuid.Must(uuid.NewV7())
	c := CohortOf(a)
	differ := c
	differ.Category = strp("DeFi")
	noDate := c
	noDate.AwardDate = nil
	src := func(s string) *string { return &s }
	for _, x := range []struct {
		row  CohortRow
		want string
	}{
		{CohortRow{}, CohortNotProposed},
		{CohortRow{Confirmed: &c}, CohortConfirmed},
		{CohortRow{Confirmed: &differ, Proposal: &c, ProposalSource: src(SourceSCFImport)}, CohortConfirmedDiffers},
		{CohortRow{Confirmed: &noDate}, CohortConfirmedDiffers},
		{CohortRow{Proposal: &c, ProposalSource: src(SourceSCFImport), SCFAwardID: &a.ID}, CohortProposed},
		{CohortRow{Proposal: &c, ProposalSource: src(SourceSCFImport), SCFAwardID: &other}, CohortNotProposed},
		{CohortRow{Proposal: &differ, ProposalSource: src(SourceSCFImport), SCFAwardID: &a.ID}, CohortNotProposed},
		{CohortRow{Proposal: &c, ProposalSource: src(SourceMember)}, CohortMemberProposal},
		{CohortRow{Proposal: &differ, ProposalSource: src(SourceMember)}, CohortMemberProposalDiffers},
		{CohortRow{SCFAwardID: &a.ID}, CohortDismissed},
		{CohortRow{SCFAwardID: &other}, CohortNotProposed},
	} {
		require.Equal(t, x.want, CohortState(x.row, a))
	}
	// an award without a date says nothing about the date
	a.AwardDate = nil
	require.Equal(t, CohortConfirmed, CohortState(CohortRow{Confirmed: &c}, a))
}

func TestNewerAward(t *testing.T) {
	d := func(s string) *time.Time { v, _ := time.Parse(time.DateOnly, s); return &v }
	a, b := award("A", ""), award("A", "")
	a.Round, b.Round = "SCF #40", "SCF #38"
	require.True(t, newer(a, b), "the higher round without dates")
	b.AwardDate = d("2025-01-01")
	require.True(t, newer(b, a), "a dated award beats an undated one")
	a.AwardDate = d("2024-06-01")
	require.True(t, newer(b, a), "the later date beats the higher round")
	a.AwardDate = d("2025-01-01")
	require.True(t, newer(a, b), "same date: the higher round")
}
