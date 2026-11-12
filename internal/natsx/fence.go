package natsx

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/db/fenceq"
)

// ErrFenced means a newer lock holder has already written: the caller is a
// former leader and its transaction must roll back.
var ErrFenced = errors.New("natsx: fenced by a newer lock holder")

// CheckFence records f as the latest fencing token of lock name in
// cp.leader_fence, inside tx. it accepts a token whose epoch is newer than
// the stored one, or equal with a revision at least as high, and fails with
// ErrFenced otherwise. the update takes the row lock, so two holders' writing
// transactions are serialised and the older one cannot commit after the
// newer one. call it in every transaction a singleton worker commits.
//
// the row for name must exist: the migrations seed one per singleton with
// epoch 0, and the worker roles may update it but not insert it.
func CheckFence(ctx context.Context, tx pgx.Tx, name string, f Fence) error {
	if f.Epoch <= 0 || f.Revision == 0 || f.Revision > math.MaxInt64 {
		return fmt.Errorf("natsx: fence %s: %+v is not the token of a held lock", name, f)
	}
	q := fenceq.New(tx)
	n, err := q.AdvanceFence(ctx, fenceq.AdvanceFenceParams{Epoch: f.Epoch, Revision: int64(f.Revision), Name: name})
	if err != nil {
		return fmt.Errorf("natsx: fence %s: %w", name, err)
	}
	if n == 1 {
		return nil
	}
	stored, err := q.GetFence(ctx, name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("natsx: fence %s: no row in cp.leader_fence, the migration must seed it", name)
	case err != nil:
		return fmt.Errorf("natsx: fence %s: %w", name, err)
	}
	return fmt.Errorf("%w: %s holds epoch %d revision %d, stored epoch %d revision %d",
		ErrFenced, name, f.Epoch, f.Revision, stored.Epoch, stored.Revision)
}
