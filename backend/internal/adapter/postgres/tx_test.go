//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

func TestInTxActorAndOptions(t *testing.T) {
	w := newWorld(t)

	t.Run("the actor is set inside and gone after", func(t *testing.T) {
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			// A read through the transaction sees the actor's rows.
			n, err := w.repo.CountChildren(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			assert.Equal(t, 2, n)
		})
		var after string
		require.NoError(t, w.db.App.QueryRow(context.Background(), `SELECT current_setting('app.actor_id', true)`).Scan(&after))
		assert.Empty(t, after)
	})

	t.Run("a zero actor is refused before any statement", func(t *testing.T) {
		err := w.runner.InTx(context.Background(), user.ActorID{}, port.TxOptions{}, func(context.Context, port.Tx) error {
			t.Fatal("fn must not run")
			return nil
		})
		assert.Error(t, err)
	})

	t.Run("a read-only transaction rejects writes", func(t *testing.T) {
		n := w.newChild("A1", node.TypeUnit, "ReadOnly")
		err := w.runner.InTx(context.Background(), w.users["adminA"], port.TxOptions{ReadOnly: true},
			func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
		require.Error(t, err)
		assert.Equal(t, "25006", sqlState(err))
	})

	t.Run("repeatable read is honoured", func(t *testing.T) {
		err := w.runner.InTx(context.Background(), w.users["adminA"], port.TxOptions{Isolation: port.RepeatableRead},
			func(ctx context.Context, tx port.Tx) error {
				_, err := w.repo.Get(ctx, tx, w.id("A1"))
				return err
			})
		require.NoError(t, err)
	})

	t.Run("a transaction not created by the runner is rejected", func(t *testing.T) {
		_, err := w.repo.Get(context.Background(), foreignTx{}, w.id("A1"))
		assert.Error(t, err)
		assert.Error(t, w.lock.Lock(context.Background(), foreignTx{}))
	})
}

type foreignTx struct{}

func (foreignTx) TxMarker() {}

// A lost connection ends in apperr.Unavailable, leaves no trace and does not
// poison the pool.
func TestConnectionLossIsUnavailable(t *testing.T) {
	w := newWorld(t)
	adminURL, ok := config.TestDBAdminURL()
	require.True(t, ok)
	n := w.newChild("A1", node.TypeUnit, "Doomed")

	w.runner.BeforeCommit(func(ctx context.Context, tx pgx.Tx) error {
		var pid int
		if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return err
		}
		admin, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			return err
		}
		defer func() { _ = admin.Close(ctx) }()
		_, err = admin.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
		return err
	})
	err := w.try("adminA", func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
	require.Error(t, err)
	assert.Equal(t, apperr.UnavailableError(), err, "got %v", err)

	var count int
	require.NoError(t, w.db.Owner.QueryRow(context.Background(),
		`SELECT count(*) FROM nodes WHERE id = $1`, uuid.UUID(n.ID)).Scan(&count))
	assert.Zero(t, count, "the insert must not survive the lost connection")

	w.runner.BeforeCommit(nil)
	w.do("adminA", func(ctx context.Context, tx port.Tx) {
		_, err := w.repo.Get(ctx, tx, w.id("A1"))
		require.NoError(t, err)
	})
}

func TestBeforeCommitErrorRollsBack(t *testing.T) {
	w := newWorld(t)
	n := w.newChild("A1", node.TypeUnit, "Never committed")
	boom := errors.New("boom")
	w.runner.BeforeCommit(func(context.Context, pgx.Tx) error { return boom })
	err := w.try("adminA", func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
	require.ErrorIs(t, err, boom)
	var count int
	require.NoError(t, w.db.Owner.QueryRow(context.Background(),
		`SELECT count(*) FROM nodes WHERE id = $1`, uuid.UUID(n.ID)).Scan(&count))
	assert.Zero(t, count)
}
