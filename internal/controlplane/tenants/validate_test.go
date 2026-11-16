package tenants

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeriveSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Stellar DEX":                  "stellar-dex",
		"  Soroban -- Swap!! ":         "soroban-swap",
		"日本":                           "",
		"a" + strings.Repeat("-b", 40): strings.TrimRight(("a" + strings.Repeat("-b", 40))[:63], "-"),
	} {
		got := DeriveSlug(in)
		require.Equal(t, want, got, in)
		if got != "" {
			require.Regexp(t, slugRe, got)
		}
	}
}

func TestText(t *testing.T) {
	var f Fields
	require.Equal(t, "ok", Text(&f, "a", "  ok \n", 1, 10))
	Text(&f, "b", "x\x00y", 0, 10)
	Text(&f, "c", "line\nnext\ttab", 0, 20)
	Text(&f, "d", strings.Repeat("ü", 11), 0, 10)
	Text(&f, "e", "   ", 1, 10)
	require.Equal(t, map[string]string{"b": "invalid_format", "d": "too_long", "e": "required"}, f.codes)
}

func TestCheckCohort(t *testing.T) {
	today := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	s := func(v string) *string { return &v }
	var f Fields
	v := CheckCohort(&f, "cohort", &CohortInput{Program: s("  Build Award "), Round: s("SCF #38"), AwardDate: s("2026-10-01")}, today, false)
	require.NoError(t, f.Err())
	require.Equal(t, "Build Award", *v.Program, "values are trimmed")
	require.Nil(t, v.Category)
	require.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), *v.AwardDate)

	for _, c := range []struct {
		in    *CohortInput
		empty bool
		path  string
	}{
		{&CohortInput{}, false, "cohort"},
		{nil, true, "cohort"},
		{&CohortInput{Program: s("")}, false, "cohort.program"},
		{&CohortInput{Round: s(strings.Repeat("r", 81))}, false, "cohort.round"},
		{&CohortInput{Category: s("a\nb")}, false, "cohort.category"},
		{&CohortInput{AwardDate: s("2026-11-17")}, false, "cohort.award_date"},
		{&CohortInput{AwardDate: s("2017-12-31")}, false, "cohort.award_date"},
		{&CohortInput{AwardDate: s("16/11/2026")}, false, "cohort.award_date"},
	} {
		var f Fields
		CheckCohort(&f, "cohort", c.in, today, c.empty)
		err := f.Err()
		require.Error(t, err, c.path)
		require.Contains(t, err.Error(), c.path)
	}
	// an operator may clear a cohort with an object of nulls
	var g Fields
	require.True(t, CheckCohort(&g, "cohort", &CohortInput{}, today, true).Empty())
	require.NoError(t, g.Err())
}
