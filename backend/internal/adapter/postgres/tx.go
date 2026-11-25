package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// rollbackTimeout bounds the rollback that follows a cancelled context.
const rollbackTimeout = 5 * time.Second

// pgTx is the transaction handed to use cases. Repositories of this package
// unwrap it; any other implementation of port.Tx is rejected.
type pgTx struct{ pgx.Tx }

// TxMarker implements port.Tx.
func (pgTx) TxMarker() {}

// unwrap returns the pgx transaction behind tx.
func unwrap(tx port.Tx) (pgx.Tx, error) {
	t, ok := tx.(pgTx)
	if !ok {
		return nil, fmt.Errorf("postgres: transaction of type %T was not created by this package", tx)
	}
	return t.Tx, nil
}

// TxRunner implements port.TxRunner on a pgx pool.
//
// Every transaction starts by setting the actor with a transaction-local
// set_config, which PostgreSQL discards at COMMIT and ROLLBACK. A connection
// therefore goes back to the pool without any actor, and row-level security
// shows nothing to whoever uses it without a new InTx.
type TxRunner struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// beforeCommit runs after fn succeeded and before COMMIT. It exists for
	// tests only and is reachable only through export_test.go.
	beforeCommit func(ctx context.Context, tx pgx.Tx) error
}

var _ port.TxRunner = (*TxRunner)(nil)

// NewTxRunner builds a runner on pool; log receives rejected writes and
// failed rollbacks.
func NewTxRunner(pool *pgxpool.Pool, log *slog.Logger) *TxRunner {
	return &TxRunner{pool: pool, log: log}
}

// InTx implements port.TxRunner.
func (r *TxRunner) InTx(ctx context.Context, actor user.ActorID, opts port.TxOptions, fn func(ctx context.Context, tx port.Tx) error) error {
	if actor.IsZero() {
		return errors.New("postgres: InTx needs a non-zero actor")
	}
	tx, err := r.pool.BeginTx(ctx, pgxOptions(opts))
	if err != nil {
		return mapError(r.log, "begin", "node", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// An error return, a panic or a failed commit: roll back on a context
		// that outlives a cancelled request. A panic keeps propagating.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		if rbErr := tx.Rollback(rctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			r.log.Error("rollback failed", "error", rbErr)
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, actor.String()); err != nil {
		return mapError(r.log, "set actor", "node", err)
	}
	if err := fn(ctx, pgTx{tx}); err != nil {
		return mapError(r.log, "transaction", "node", err)
	}
	if r.beforeCommit != nil {
		if err := r.beforeCommit(ctx, tx); err != nil {
			return mapError(r.log, "before commit", "node", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError(r.log, "commit", "node", err)
	}
	committed = true
	return nil
}

func pgxOptions(o port.TxOptions) pgx.TxOptions {
	out := pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}
	if o.Isolation == port.RepeatableRead {
		out.IsoLevel = pgx.RepeatableRead
	}
	if o.ReadOnly {
		out.AccessMode = pgx.ReadOnly
	}
	return out
}
