package main

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
)

// a rotation leaves exactly one active token, the new one, revokes the old
// one in the same step and records both in the audit log as the probe actor
func TestProbeTenantRotateOTLPToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	log := slog.New(slog.DiscardHandler)
	require.NoError(t, runDBWith(ctx, []string{"migrate"}, config.FromMap(pg.Env), log))
	require.NoError(t, runDBWith(ctx, []string{"roles"}, config.FromMap(pg.Env), log))
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "platformctl-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)

	_, _, err = rotateProbeOTLPToken(ctx, d)
	require.ErrorContains(t, err, "no internal probe tenant")

	require.NoError(t, d.WithSystem(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into cp.users (github_id, github_login, is_operator) values (9300001, 'probe-op', true)`)
		return err
	}))
	pt, err := createProbeTenant(ctx, d, 9300001, "probe.example.test", "https://probe.example.test")
	require.NoError(t, err)

	type row struct {
		id     uuid.UUID
		hash   []byte
		status string
	}
	tokens := func() []row {
		var out []row
		require.NoError(t, d.WithSystem(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `select id, hash, status from cp.otlp_tokens where tenant_id = $1 order by created_at, id`, pt.TenantID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.id, &r.hash, &r.status); err != nil {
					return err
				}
				out = append(out, r)
			}
			return rows.Err()
		}))
		return out
	}
	before := tokens()
	require.Len(t, before, 1)

	for round := 1; round <= 2; round++ {
		plain, revoked, err := rotateProbeOTLPToken(ctx, d)
		require.NoError(t, err)
		require.Equal(t, 1, revoked)
		require.NotEqual(t, pt.OTLPToken, plain)
		want := sha256.Sum256([]byte(plain))

		all := tokens()
		require.Len(t, all, round+1)
		active := 0
		for _, r := range all {
			if r.status == otlptokens.StatusActive {
				active++
				require.Equal(t, want[:], r.hash, "the active token is the one printed")
			}
		}
		require.Equal(t, 1, active)
		pt.OTLPToken = plain
	}

	var creates, revokes int
	require.NoError(t, d.WithSystem(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select count(*) from cp.audit_log where action = $1 and actor = $2`, audit.OTLPTokenCreate, probeActor).Scan(&creates); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from cp.audit_log where action = $1 and actor = $2 and target = $3`,
			audit.OTLPTokenRevoke, probeActor, audit.OTLPTokenTarget(before[0].id)).Scan(&revokes)
	}))
	require.Equal(t, 3, creates, "one at creation, one per rotation")
	require.Equal(t, 1, revokes, "the first token is revoked once")
}
