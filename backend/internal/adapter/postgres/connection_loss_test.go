//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// Test-ID: T-I-27
// EC-83: the connection dies between the INSERT of the operation and COMMIT:
// the caller gets db_unavailable, nothing is stored, and a retry with the same
// key creates exactly one operation.
func TestTerminateBackendMidTransaction(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	adminURL, ok := config.TestDBAdminURL()
	require.True(t, ok)

	kills := 0
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
		kills++
		_, err = admin.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
		return err
	})
	_, err := c.creditAs("adminA", "A1u1", "100", "k1")
	require.Error(t, err)
	assert.Equal(t, apperr.UnavailableError(), err, "got %v", err)
	assert.Equal(t, 1, kills)
	assert.Zero(t, w.countRows("operations"))
	assert.Zero(t, w.countRows("idempotency_keys"))
	assert.EqualValues(t, 0, w.balanceOf("A1u1"))

	w.runner.BeforeCommit(nil)
	out, err := c.creditAs("adminA", "A1u1", "100", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed, "the key of the lost attempt was never stored")
	again, err := c.creditAs("adminA", "A1u1", "100", "k1")
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.Equal(t, 1, w.countRows("operations"))
	assert.EqualValues(t, 100, w.balanceOf("A1u1"))
	w.requireLedgerInvariant("after the lost connection")
}
