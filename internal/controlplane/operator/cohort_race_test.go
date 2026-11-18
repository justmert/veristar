package operator

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// a proposal the project changes while an operator's confirm runs: the
// confirm copies the proposal it read and audits, never a newer one the
// audit row does not name.
func TestCohortConfirmAuditsWhatItConfirms(t *testing.T) {
	f := newCohortFixture(t)
	beta := f.tenants["beta"]
	superExec(t, f.pg, `insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_by, proposed_at)
		values ($1, 'First', $2, now())`, beta, f.op)

	// the project's new proposal, written and not yet committed
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	tx, err := c.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `update cp.tenant_cohorts set proposed_program = 'Second', proposed_at = now()
		where tenant_id = $1`, beta)
	require.NoError(t, err)

	type result struct {
		code int
		body []byte
	}
	done := make(chan result, 1)
	go func() {
		code, body := f.do(t, http.MethodPost, "/api/operator/tenants/"+beta.String()+"/cohort/confirm", nil)
		done <- result{code, body}
	}()
	// wait until the confirm waits for the project's row lock
	mon, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = mon.Close(context.Background()) }()
	require.Eventually(t, func() bool {
		var n int
		err := mon.QueryRow(t.Context(), `select count(*) from pg_stat_activity
			where wait_event_type = 'Lock' and pid <> $1`, tx.Conn().PgConn().PID()).Scan(&n)
		return err == nil && n > 0
	}, 20*time.Second, 20*time.Millisecond)
	require.NoError(t, tx.Commit(t.Context()))

	r := <-done
	require.Equal(t, http.StatusOK, r.code, string(r.body))
	rec := decode[CohortRecord](t, r.body)
	rows := f.audit(t, ActionCohortConfirm)
	require.Len(t, rows, 1)
	change := rows[0]["detail"].(map[string]any)["cohort"].(map[string]any)
	audited := change["new"].(map[string]any)["program"]
	require.Equal(t, *rec.Confirmed.Program, audited, "the audit row names the confirmed program")
	require.Equal(t, "Second", *rec.Confirmed.Program)
}
