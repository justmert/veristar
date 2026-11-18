package db_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// the enrollment answers add their ids to cp.chain_ids only up to the 20
// the console holds a project to (migration 00048): a resubmission with
// new ids used to add them on top of the ids already held
func TestEnrollmentIDsStopAtTheCap(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	tx, err := c.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = randomContract()
		}
		return out
	}
	answers := func(list []string) string {
		b, err := json.Marshal(map[string]any{"stellar_ids": list})
		require.NoError(t, err)
		return string(b)
	}
	count := func(tenant string) int {
		var n int
		require.NoError(t, tx.QueryRow(ctx, `select count(*) from cp.chain_ids where tenant_id = $1`, tenant).Scan(&n))
		return n
	}

	first := ids(15)
	var tenant string
	require.NoError(t, tx.QueryRow(ctx, `insert into cp.tenants (slug, name, enrollment_answers)
		values ('cap-test', 'cap', $1::jsonb) returning id`, answers(first)).Scan(&tenant))
	require.Equal(t, 15, count(tenant))

	// a resubmission names 15 new ids: only the first 5 fit
	second := ids(15)
	_, err = tx.Exec(ctx, `update cp.tenants set enrollment_answers = $2::jsonb where id = $1`, tenant, answers(second))
	require.NoError(t, err)
	require.Equal(t, 20, count(tenant))
	var added []string
	rows, err := tx.Query(ctx, `select strkey from cp.chain_ids where tenant_id = $1 and strkey = any($2)`, tenant, second)
	require.NoError(t, err)
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		added = append(added, s)
	}
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, second[:5], added, "the ids are taken in the order the answers list them")

	// a full project gains nothing from new answers
	_, err = tx.Exec(ctx, `update cp.tenants set enrollment_answers = $2::jsonb where id = $1`, tenant, answers(ids(3)))
	require.NoError(t, err)
	require.Equal(t, 20, count(tenant))

	// a removed id makes room again, and an id named twice counts once
	_, err = tx.Exec(ctx, `delete from cp.chain_ids where tenant_id = $1 and strkey = $2`, tenant, first[0])
	require.NoError(t, err)
	again := ids(1)
	_, err = tx.Exec(ctx, `update cp.tenants set enrollment_answers = $2::jsonb where id = $1`, tenant,
		answers([]string{again[0], again[0], randomContract()}))
	require.NoError(t, err)
	require.Equal(t, 20, count(tenant))
	var n int
	require.NoError(t, tx.QueryRow(ctx, `select count(*) from cp.chain_ids where tenant_id = $1 and strkey = $2`, tenant, again[0]).Scan(&n))
	require.Equal(t, 1, n)
}
