package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/db/dbtest"
)

const scfExport = "Project,Round,Category,Award Type,Award Amount,Award Date,Website,Contact Person\n" +
	"Example Pay,SCF #44 ,Applications,Build,\"$75,000.00\",3/2/2026,https://www.examplepay.example,Pat Placeholder\n" +
	"Broken,SCF #44,Applications,Build,$75000,13/45/2026,https://broken.example,Pat Placeholder\n"

// a dry run reads standard input and touches no database; an import
// prints the counts and the rejected rows; scf match runs the matching
// again
func TestSCFCommand(t *testing.T) {
	var out bytes.Buffer
	err := runSCF(t.Context(), []string{"import", "-dry-run", "-"}, config.FromMap(nil), strings.NewReader(scfExport), &out)
	require.NoError(t, err)
	require.Equal(t, "dry_run=true\nheader=true\ndelimiter=comma\nrecords=2\naccepted=1\nrejected=1\n"+
		"rejected record=2 line=3 field=award_date reason=invalid\n", out.String())
	require.NotContains(t, out.String(), "Placeholder")

	err = runSCF(t.Context(), []string{"import"}, config.FromMap(nil), strings.NewReader(""), &out)
	require.ErrorContains(t, err, "usage")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	log := slog.New(slog.DiscardHandler)
	require.NoError(t, runDBWith(ctx, []string{"migrate"}, config.FromMap(pg.Env), log))
	require.NoError(t, runDBWith(ctx, []string{"roles"}, config.FromMap(pg.Env), log))
	env := pg.Config("platform")

	out.Reset()
	require.NoError(t, runSCF(ctx, []string{"import", "-"}, env, strings.NewReader(scfExport), &out))
	s := out.String()
	for _, want := range []string{"\nrecords=2\n", "\ninserted=1\n", "\nrejected=1\n", "\nmatched=0\n", "\nnot_enrolled=1\n",
		"\nproposals_written=0\n", "rejected record=2 line=3 field=award_date reason=invalid\n"} {
		require.Contains(t, s, want)
	}
	out.Reset()
	require.NoError(t, runSCF(ctx, []string{"import", "-"}, env, strings.NewReader(scfExport), &out))
	require.Contains(t, out.String(), "\nunchanged=1\n")
	out.Reset()
	require.NoError(t, runSCF(ctx, []string{"match"}, env, nil, &out))
	require.Equal(t, "matched=0\nambiguous=0\nnot_enrolled=1\nproposals_written=0\n", out.String())
}
