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
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// claimAttempts bounds the insert, read and delete rounds of one claim: a
// round repeats only when the row it met vanished (its transaction rolled back)
// or was an expired one.
const claimAttempts = 5

// IdempotencyRepo implements port.IdempotencyRepo.
type IdempotencyRepo struct{ log *slog.Logger }

var _ port.IdempotencyRepo = (*IdempotencyRepo)(nil)

// NewIdempotencyRepo builds the repository; log receives rejected statements.
func NewIdempotencyRepo(log *slog.Logger) *IdempotencyRepo { return &IdempotencyRepo{log: log} }

// Claim implements port.IdempotencyRepo. INSERT ... ON CONFLICT DO NOTHING
// waits on the unique index while another transaction holds the same key; the
// SELECT that follows is a new statement and sees the committed row.
func (r *IdempotencyRepo) Claim(ctx context.Context, tx port.Tx, rec port.IdempotencyRecord, now time.Time) (bool, port.IdempotencyRecord, error) {
	t, err := unwrap(tx)
	if err != nil {
		return false, port.IdempotencyRecord{}, err
	}
	for range claimAttempts {
		tag, err := t.Exec(ctx,
			`INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, transfer_id, created_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (user_id, key) DO NOTHING`,
			uuid.UUID(rec.UserID), rec.Key, rec.RequestHash, opPtr(rec.OperationID), trPtr(rec.TransferID),
			rec.CreatedAt.UTC(), rec.ExpiresAt.UTC())
		if err != nil {
			return false, port.IdempotencyRecord{}, mapError(r.log, "claim idempotency key", "node", err)
		}
		if tag.RowsAffected() == 1 {
			return true, port.IdempotencyRecord{}, nil
		}
		existing, err := r.load(ctx, t, rec.UserID, rec.Key)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the other transaction rolled back; claim again
		}
		if err != nil {
			return false, port.IdempotencyRecord{}, mapError(r.log, "read idempotency key", "node", err)
		}
		if existing.ExpiresAt.After(now) {
			return false, existing, nil
		}
		if _, err := t.Exec(ctx,
			`DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2 AND expires_at <= $3`,
			uuid.UUID(rec.UserID), rec.Key, now.UTC()); err != nil {
			return false, port.IdempotencyRecord{}, mapError(r.log, "delete expired idempotency key", "node", err)
		}
	}
	return false, port.IdempotencyRecord{}, fmt.Errorf("postgres: idempotency key of %s did not settle in %d attempts", rec.UserID, claimAttempts)
}

func (r *IdempotencyRepo) load(ctx context.Context, t pgx.Tx, u user.ID, key string) (port.IdempotencyRecord, error) {
	var op, tr *uuid.UUID
	rec := port.IdempotencyRecord{UserID: u, Key: key}
	err := t.QueryRow(ctx,
		`SELECT request_hash, operation_id, transfer_id, created_at, expires_at
		   FROM idempotency_keys WHERE user_id = $1 AND key = $2`, uuid.UUID(u), key).
		Scan(&rec.RequestHash, &op, &tr, &rec.CreatedAt, &rec.ExpiresAt)
	if err != nil {
		return port.IdempotencyRecord{}, err
	}
	rec.CreatedAt, rec.ExpiresAt = rec.CreatedAt.UTC(), rec.ExpiresAt.UTC()
	if op != nil {
		v := balance.OperationID(*op)
		rec.OperationID = &v
	}
	if tr != nil {
		v := balance.TransferID(*tr)
		rec.TransferID = &v
	}
	return rec, nil
}

func opPtr(id *balance.OperationID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := uuid.UUID(*id)
	return &v
}

func trPtr(id *balance.TransferID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := uuid.UUID(*id)
	return &v
}
