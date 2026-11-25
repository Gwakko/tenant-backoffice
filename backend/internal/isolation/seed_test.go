//go:build integration

package isolation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// inTx runs fn in a transaction on pool as tb_app. A non-empty actor is set the
// way the application does it (transaction-local). The transaction is always
// rolled back, so a test never relies on its own writes.
func inTx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, actor string, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	if actor != "" {
		_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, actor)
		require.NoError(t, err)
	}
	fn(tx)
}

func count(ctx context.Context, t *testing.T, tx pgx.Tx, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM ("+query+") q", args...).Scan(&n))
	return n
}

func ids(ctx context.Context, t *testing.T, tx pgx.Tx, query string, args ...any) []uuid.UUID {
	t.Helper()
	rows, err := tx.Query(ctx, query, args...)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)
	return out
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// snapshot digests every table of the database (see isolation.Snapshot).
func snapshot(ctx context.Context, t *testing.T, db *testkit.DB) string {
	t.Helper()
	return isolation.Snapshot(ctx, t, db)
}

// newWorld builds the shared fixture.
func newWorld(ctx context.Context, t *testing.T, db *testkit.DB) *isolation.World {
	t.Helper()
	return isolation.NewWorld(ctx, t, db)
}
