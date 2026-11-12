package operator

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// ActionOperatorSync is the audit action of a changed is_operator flag.
const ActionOperatorSync = "user.operator_sync"

// ActorStartup is the actor of the startup sync.
const ActorStartup = "system:startup"

// SyncFlags sets cp.users.is_operator from the loaded OPERATOR_GITHUB_IDS for
// every user in one cp_system transaction and audits each changed user
// (contract 5.1). sign-in keeps doing the same for the person signing in;
// this covers people who do not sign in again after an id was removed. it
// returns how many users changed.
func SyncFlags(ctx context.Context, d *db.DB, ids map[int64]bool) (int, error) {
	list := make([]int64, 0, len(ids))
	for id, ok := range ids {
		if ok {
			list = append(list, id)
		}
	}
	var n int
	err := d.WithSystem(ctx, func(tx pgx.Tx) error {
		rows, err := systemq.New(tx).OperatorSyncFlags(ctx, list)
		if err != nil {
			return err
		}
		n = len(rows)
		for _, r := range rows {
			if err := audit.InSystem(ctx, tx, audit.Entry{
				Actor: ActorStartup, Action: ActionOperatorSync, Target: audit.UserTarget(r.ID),
				Detail: map[string]any{"is_operator": audit.Change(!r.IsOperator, r.IsOperator)},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("operator: sync is_operator: %w", err)
	}
	return n, nil
}
