package scfawards

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/require"
)

// the fixtures are synthetic rows in the shape of the scf build award
// export: 35 columns, the personal columns filled with made-up values the
// tests then look for in everything the parser returns.

var today = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

const (
	personName = "Pat Placeholder"
	linkedin   = "https://www.linkedin.com/in/pat-placeholder-test"
	xHandle    = "https://x.com/pat_placeholder_test"
	docsend    = "https://docsend.com/view/placeholder000"
	docLink    = "https://docs.google.com/document/d/placeholder000"
	traction   = "Live product with early usage.\n\nThree pilots signed."
	describe   = "Overview\n\nA \"borderless\" payments app built on Stellar."
)

// row is one export row; fields not set stay empty.
type row struct {
	project, round, category, awardType, date, amount, pct, status, website string
	description, traction                                                    string
}

func (r row) cells() []string {
	c := make([]string, 35)
	c[0], c[1], c[2], c[3], c[4] = r.project, r.round, "Proposal of "+r.project, r.project, r.category
	c[5] = "https://communityfund.stellar.org/dashboard/submissions/recPLACEHOLDER"
	c[6], c[7], c[8], c[9], c[10], c[11] = r.pct, r.amount, "$10,000.00", "12,345.67", r.date, r.awardType
	c[13], c[14], c[16], c[18], c[21] = r.description, docLink, r.website, "http://demo.example.org", r.traction
	c[22] = "Deliverable # 1 - Integration\nBudget: $10,000"
	c[25], c[26], c[27], c[28], c[30] = personName, "photo.jpg (https://airtable.example/photo.jpg)", linkedin, xHandle, docsend
	c[32], c[33], c[34] = r.status, "2026", "Q3 '26"
	return c
}

func good(project string) row {
	return row{project: project, round: "SCF #45", category: "Developer Tooling", awardType: "Build",
		date: "9/1/2026", amount: "$100,000.00", pct: "33", status: "Pre-Launch #1 - MVP",
		website: "https://www." + strings.ToLower(project) + ".example", description: describe, traction: traction}
}

func encode(t *testing.T, comma rune, header []string, rows ...row) []byte {
	t.Helper()
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = comma
	if header != nil {
		require.NoError(t, w.Write(header))
	}
	for _, r := range rows {
		require.NoError(t, w.Write(r.cells()))
	}
	w.Flush()
	require.NoError(t, w.Error())
	return []byte(b.String())
}

// exportHeader names the columns as a header row would.
func exportHeader() []string {
	h := make([]string, 35)
	for i := range h {
		h[i] = "Column " + string(rune('A'+i%26))
	}
	h[0], h[1], h[3], h[4], h[6], h[7], h[10], h[11], h[16], h[25], h[32] = "Name", "Round", "Project", "Category",
		"Tranche %", "Award Amount", "Award Date", "Award Type", "Website", "Contact Person", "Status"
	return h
}

func account() string { return keypair.MustRandom().Address() }

func contract(t *testing.T) string {
	t.Helper()
	var b [32]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	s, err := strkey.Encode(strkey.VersionByteContract, b[:])
	require.NoError(t, err)
	return s
}

// noPersonalData fails when anything the parser returns holds a value of
// a personal or free-text column.
func noPersonalData(t *testing.T, p Parsed) {
	t.Helper()
	b, err := json.Marshal(p)
	require.NoError(t, err)
	for _, v := range []string{personName, "linkedin", "pat_placeholder", "docsend", "docs.google", "borderless",
		"pilots", "Deliverable", "Proposal of", "photo.jpg", "communityfund", "demo.example", "12,345", "12345"} {
		require.NotContains(t, string(b), v)
	}
}

func TestParseHeaderlessTab(t *testing.T) {
	r := good("Lumenpay")
	r.round = "SCF #34 "
	data := encode(t, '\t', nil, r, good("Anchorly"))
	p, err := Parse(data, today)
	require.NoError(t, err)
	require.False(t, p.HasHeader)
	require.Equal(t, "tab", p.Delimiter)
	require.Equal(t, 2, p.Records)
	require.Empty(t, p.Rejected)
	require.Len(t, p.Awards, 2)
	a := p.Awards[0]
	require.Equal(t, "Lumenpay", a.Project)
	require.Equal(t, "SCF #34", a.Round, "the trailing space of the export is dropped")
	require.Equal(t, "Developer Tooling", a.Category)
	require.Equal(t, ProgramBuildAward, a.Program)
	require.Equal(t, "2026-09-01", a.AwardDate.Format(time.DateOnly))
	require.Equal(t, int64(10_000_000), a.AmountCents)
	require.Equal(t, 33, *a.TranchePercent)
	require.Equal(t, "Pre-Launch #1 - MVP", *a.TrancheStatus)
	require.Equal(t, "lumenpay.example", *a.WebsiteHost)
	require.Empty(t, a.Strkeys)
	require.Equal(t, 1, a.Record)
	// the second record starts after the first one's multi-line fields
	require.Greater(t, p.Awards[1].Line, 5)
	noPersonalData(t, p)
}

func TestParseCommaWithHeader(t *testing.T) {
	data := encode(t, ',', exportHeader(), good("Lumenpay"), good("Anchorly"))
	p, err := Parse(data, today)
	require.NoError(t, err)
	require.True(t, p.HasHeader)
	require.Equal(t, "comma", p.Delimiter)
	require.Equal(t, 2, p.Records)
	require.Len(t, p.Awards, 2)
	require.Equal(t, "Anchorly", p.Awards[1].Project)
	require.Equal(t, 2, p.Awards[1].Record)
	require.Equal(t, 33, *p.Awards[1].TranchePercent, "the Tranche % column")
	require.Equal(t, "Pre-Launch #1 - MVP", *p.Awards[1].TrancheStatus)
	require.Equal(t, "2026-09-01", p.Awards[1].AwardDate.Format(time.DateOnly))
	noPersonalData(t, p)

	// a header maps columns by name, wherever they are
	h := []string{"Website", "Award Amount", "Award Type", "Category", "Round", "Project", "Notes"}
	p, err = Parse([]byte(strings.Join(h, "\t")+"\nhttps://Example.ORG.\t$5,000\tBuild\tFinancial Protocols\tSCF #40\tSwapline\tsee https://linkedin.com/in/x\n"), today)
	require.NoError(t, err)
	require.True(t, p.HasHeader)
	require.Len(t, p.Awards, 1)
	a := p.Awards[0]
	require.Equal(t, "Swapline", a.Project)
	require.Equal(t, "example.org", *a.WebsiteHost)
	require.Equal(t, int64(500_000), a.AmountCents)
	require.Nil(t, a.AwardDate)
	require.Nil(t, a.TranchePercent)

	// a header without a required column cannot be read
	_, err = Parse([]byte("Project\tRound\tCategory\nX\tSCF #1\tApplications\n"), today)
	require.ErrorContains(t, err, "award_type, award_amount")
}

func TestParseRejectsWithReasons(t *testing.T) {
	mk := func(f func(*row)) row { r := good("P"); f(&r); return r }
	cases := []struct {
		r             row
		field, reason string
	}{
		{mk(func(r *row) { r.project = "  " }), "project", ReasonMissing},
		{mk(func(r *row) { r.round = "" }), "round", ReasonMissing},
		{mk(func(r *row) { r.round = "Round 45" }), "round", ReasonInvalid},
		{mk(func(r *row) { r.category = "Gaming" }), "category", ReasonUnknown},
		{mk(func(r *row) { r.awardType = "Activation" }), "award_type", ReasonUnknown},
		{mk(func(r *row) { r.date = "31/12/2025" }), "award_date", ReasonInvalid},
		{mk(func(r *row) { r.date = "soon" }), "award_date", ReasonInvalid},
		{mk(func(r *row) { r.date = "1/2/2017" }), "award_date", ReasonOutOfRange},
		{mk(func(r *row) { r.date = "1/2/2027" }), "award_date", ReasonOutOfRange},
		{mk(func(r *row) { r.amount = "" }), "award_amount", ReasonMissing},
		{mk(func(r *row) { r.amount = "$1.000,00" }), "award_amount", ReasonInvalid},
		{mk(func(r *row) { r.amount = "-5" }), "award_amount", ReasonInvalid},
		{mk(func(r *row) { r.amount = "about 50k" }), "award_amount", ReasonInvalid},
		{mk(func(r *row) { r.amount = "$10,00" }), "award_amount", ReasonInvalid},
		{mk(func(r *row) { r.pct = "half" }), "tranche_percent", ReasonInvalid},
		{mk(func(r *row) { r.pct = "150" }), "tranche_percent", ReasonOutOfRange},
		{mk(func(r *row) { r.website = "http://10.0.0.1/app" }), "website", ReasonInvalid},
		{mk(func(r *row) { r.website = "localhost" }), "website", ReasonInvalid},
		{mk(func(r *row) { r.website = "https://user:pw@example.org" }), "website", ReasonInvalid},
	}
	for _, c := range cases {
		p, err := Parse(encode(t, '\t', nil, good("First"), c.r), today)
		require.NoError(t, err)
		require.Len(t, p.Awards, 1, "%s %s", c.field, c.reason)
		require.Equal(t, []Rejection{{Record: 2, Line: p.Rejected[0].Line, Field: c.field, Reason: c.reason}}, p.Rejected)
		require.Equal(t, 2, p.Records)
		noPersonalData(t, p)
	}

	// a short row, a duplicate project and round (any case), and a blank
	// row, which is not a record
	short := strings.Join(make([]string, 5), "\t")
	dup := good("first")
	data := string(encode(t, '\t', nil, good("First"), dup)) + "\t\t\n" + "x\tSCF #1\t" + short + "\n"
	p, err := Parse([]byte(data), today)
	require.NoError(t, err)
	require.Equal(t, 3, p.Records)
	require.Len(t, p.Awards, 1)
	require.Equal(t, ReasonDuplicate, p.Rejected[0].Reason)
	require.Equal(t, ReasonTooFewColumns, p.Rejected[1].Reason)
}

func TestParseAmountsDatesAndQuotes(t *testing.T) {
	for in, want := range map[string]int64{
		"$100,000.00": 10_000_000, "100000": 10_000_000, "1,234,567.5": 123_456_750, "USD 75": 7_500, "75 USD": 7_500, "$0": 0,
	} {
		got, err := parseAmount(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	require.Equal(t, "123456.05", FormatCents(12_345_605))
	d, err := parseDate("2026-01-05")
	require.NoError(t, err)
	require.Equal(t, time.January, d.Month())

	// a quote inside an unquoted field is text, as the export writes it
	r := good("Quoted")
	data := strings.Replace(string(encode(t, '\t', nil, r)), "Proposal of Quoted", `the "best" proposal`, 1)
	p, err := Parse([]byte(data), today)
	require.NoError(t, err)
	require.Len(t, p.Awards, 1)

	// a byte order mark and windows line ends
	data = "\ufeff" + strings.ReplaceAll(string(encode(t, ',', exportHeader(), good("Bom"))), "\n", "\r\n")
	p, err = Parse([]byte(data), today)
	require.NoError(t, err)
	require.True(t, p.HasHeader)
	require.Len(t, p.Awards, 1)

	_, err = Parse([]byte{0xff, 0xfe, 0x00}, today)
	require.ErrorContains(t, err, "utf-8")
}

func TestStrkeysAndHosts(t *testing.T) {
	g, c := account(), contract(t)
	broken := g[:55] + map[bool]string{true: "A", false: "B"}[g[55] != 'A']
	text := "our account " + g + " and contract (" + c + "). bad checksum " + broken +
		" inside a run X" + account() + " muxed " + strings.Repeat("A", 3)
	require.Equal(t, sortedPair(g, c), ExtractStrkeys(text, "again "+g))

	r := good("Chainy")
	r.traction = "Contract " + c + " is live; treasury " + g + "."
	p, err := Parse(encode(t, '\t', nil, r), today)
	require.NoError(t, err)
	require.Equal(t, sortedPair(g, c), p.Awards[0].Strkeys)

	for in, want := range map[string]string{
		"https://WWW.Example.ORG./path?q=1": "example.org", "www.rampmedaddy.com": "rampmedaddy.com",
		"http://app.example.org:8443": "app.example.org", "https://bücher.example": "xn--bcher-kva.example",
		"https://www.www.example.org": "www.example.org",
	} {
		h, ok := NormalizeHost(in)
		require.True(t, ok, in)
		require.Equal(t, want, h, in)
	}
	for _, in := range []string{"", "ftp://example.org", "https://localhost:3000", "http://127.0.0.1", "not a url", "https://example"} {
		_, ok := NormalizeHost(in)
		require.False(t, ok, in)
	}
	r.round = "scf#7"
	p, err = Parse(encode(t, '\t', nil, r), today)
	require.NoError(t, err)
	require.Equal(t, "SCF #7", p.Awards[0].Round)
}

func sortedPair(a, b string) []string {
	if a < b {
		return []string{a, b}
	}
	return []string{b, a}
}
