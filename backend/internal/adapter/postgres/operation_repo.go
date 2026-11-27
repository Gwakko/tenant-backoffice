package postgres

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const operationColumns = `id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, comment, actor_id, created_at`

// OperationRepo implements port.OperationRepo.
type OperationRepo struct{ log *slog.Logger }

var _ port.OperationRepo = (*OperationRepo)(nil)

// NewOperationRepo builds the repository; log receives rejected statements.
func NewOperationRepo(log *slog.Logger) *OperationRepo { return &OperationRepo{log: log} }

// Insert implements port.OperationRepo.
func (r *OperationRepo) Insert(ctx context.Context, tx port.Tx, op balance.Operation) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	var transfer, counterparty *uuid.UUID
	if op.TransferID != nil {
		v := uuid.UUID(*op.TransferID)
		transfer = &v
	}
	if op.CounterpartyNodeID != nil {
		v := uuid.UUID(*op.CounterpartyNodeID)
		counterparty = &v
	}
	_, err = t.Exec(ctx,
		`INSERT INTO operations (`+operationColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.UUID(op.ID), uuid.UUID(op.NodeID), string(op.Kind), op.Amount.Minor(), op.BalanceAfter.Minor(),
		transfer, counterparty, op.Comment.String(), uuid.UUID(op.ActorID), op.CreatedAt.UTC())
	if err != nil {
		return mapError(r.log, "insert operation", "node", err)
	}
	return nil
}

// Get implements port.OperationRepo.
func (r *OperationRepo) Get(ctx context.Context, tx port.Tx, id balance.OperationID) (balance.Operation, error) {
	t, err := unwrap(tx)
	if err != nil {
		return balance.Operation{}, err
	}
	op, err := scanOperation(t.QueryRow(ctx, `SELECT `+operationColumns+` FROM operations WHERE id = $1`, uuid.UUID(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return balance.Operation{}, apperr.NotFoundError("operation")
	}
	if err != nil {
		return balance.Operation{}, mapError(r.log, "get operation", "operation", err)
	}
	return op, nil
}

// GetTransfer implements port.OperationRepo.
func (r *OperationRepo) GetTransfer(ctx context.Context, tx port.Tx, id balance.TransferID) (balance.Operation, balance.Operation, error) {
	t, err := unwrap(tx)
	if err != nil {
		return balance.Operation{}, balance.Operation{}, err
	}
	rows, err := t.Query(ctx, `SELECT `+operationColumns+` FROM operations WHERE transfer_id = $1`, uuid.UUID(id))
	if err != nil {
		return balance.Operation{}, balance.Operation{}, mapError(r.log, "get transfer", "operation", err)
	}
	defer rows.Close()
	var out, in *balance.Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return balance.Operation{}, balance.Operation{}, mapError(r.log, "get transfer", "operation", err)
		}
		switch op.Kind {
		case balance.TransferOut:
			out = &op
		case balance.TransferIn:
			in = &op
		case balance.Credit, balance.Debit:
		}
	}
	if err := rows.Err(); err != nil {
		return balance.Operation{}, balance.Operation{}, mapError(r.log, "get transfer", "operation", err)
	}
	if out == nil || in == nil {
		// Either leg hidden by row-level security hides the whole transfer.
		return balance.Operation{}, balance.Operation{}, apperr.NotFoundError("operation")
	}
	return *out, *in, nil
}

func scanOperation(row pgx.Row) (balance.Operation, error) {
	var (
		id, nodeID, actor     uuid.UUID
		transfer, counterpart *uuid.UUID
		kind, comment         string
		amount, after         int64
		created               time.Time
	)
	if err := row.Scan(&id, &nodeID, &kind, &amount, &after, &transfer, &counterpart, &comment, &actor, &created); err != nil {
		return balance.Operation{}, err
	}
	k, err := balance.ParseKind(kind)
	if err != nil {
		return balance.Operation{}, err
	}
	c, err := balance.ParseComment(comment)
	if err != nil {
		return balance.Operation{}, err
	}
	op := balance.Operation{
		ID: balance.OperationID(id), NodeID: node.ID(nodeID), Kind: k,
		Amount: money.Amount(amount), BalanceAfter: money.Amount(after),
		Comment: c, ActorID: user.ID(actor), CreatedAt: created.UTC(),
	}
	if transfer != nil {
		v := balance.TransferID(*transfer)
		op.TransferID = &v
	}
	if counterpart != nil {
		v := node.ID(*counterpart)
		op.CounterpartyNodeID = &v
	}
	return op, nil
}
