package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// BalanceRepo implements port.BalanceRepo.
//
// A violation of the balances CHECK (amount >= 0) is never translated to
// not_found: it means the use case wrote a negative balance, which it must
// have refused with insufficient_funds beforehand. It surfaces as an internal
// error.
type BalanceRepo struct{ log *slog.Logger }

var _ port.BalanceRepo = (*BalanceRepo)(nil)

// NewBalanceRepo builds the repository; log receives rejected statements.
func NewBalanceRepo(log *slog.Logger) *BalanceRepo { return &BalanceRepo{log: log} }

// Get implements port.BalanceRepo.
func (r *BalanceRepo) Get(ctx context.Context, tx port.Tx, id node.ID) (balance.Balance, error) {
	return r.read(ctx, tx, id, "")
}

// LockForUpdate implements port.BalanceRepo. The row is created lazily; two
// transactions creating it at once serialise on the primary key, then on the
// row lock.
func (r *BalanceRepo) LockForUpdate(ctx context.Context, tx port.Tx, id node.ID, now time.Time) (balance.Balance, error) {
	t, err := unwrap(tx)
	if err != nil {
		return balance.Balance{}, err
	}
	if _, err := t.Exec(ctx,
		`INSERT INTO balances (node_id, amount, version, updated_at) VALUES ($1, 0, 0, $2)
		 ON CONFLICT (node_id) DO NOTHING`, uuid.UUID(id), now.UTC()); err != nil {
		return balance.Balance{}, mapError(r.log, "ensure balance", "node", err)
	}
	return r.read(ctx, tx, id, " FOR UPDATE")
}

func (r *BalanceRepo) read(ctx context.Context, tx port.Tx, id node.ID, suffix string) (balance.Balance, error) {
	t, err := unwrap(tx)
	if err != nil {
		return balance.Balance{}, err
	}
	var amount, version int64
	var updated time.Time
	err = t.QueryRow(ctx,
		`SELECT amount, version, updated_at FROM balances WHERE node_id = $1`+suffix, uuid.UUID(id)).
		Scan(&amount, &version, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		if suffix != "" {
			// The row was just ensured: not visible means a node outside the
			// actor's subtrees.
			return balance.Balance{}, mapError(r.log, "lock balance", "node", fmt.Errorf("balance of %s is not visible", id))
		}
		return balance.Balance{NodeID: id}, nil
	}
	if err != nil {
		return balance.Balance{}, mapError(r.log, "read balance", "node", err)
	}
	return balance.Balance{NodeID: id, Amount: money.Amount(amount), Version: version, UpdatedAt: updated.UTC()}, nil
}

// Save implements port.BalanceRepo. The row must be locked by this
// transaction and still hold the previous version.
func (r *BalanceRepo) Save(ctx context.Context, tx port.Tx, b balance.Balance) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx,
		`UPDATE balances SET amount = $2, version = $3, updated_at = $4
		  WHERE node_id = $1 AND version = $3 - 1`,
		uuid.UUID(b.NodeID), b.Amount.Minor(), b.Version, b.UpdatedAt.UTC())
	if err != nil {
		return mapError(r.log, "save balance", "node", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: save balance of %s: %d rows changed, the row was not locked at version %d",
			b.NodeID, tag.RowsAffected(), b.Version-1)
	}
	return nil
}
