//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appTx runs fn in a transaction as tb_app with the actor set; always rolled back.
func appTx(ctx context.Context, t *testing.T, w *world, actor uuid.UUID, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := w.db.App.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, actor.String())
	require.NoError(t, err)
	fn(tx)
}

// Test-ID: T-I-38
//
// The audit journal (and the ledger) is append-only: tb_app has no UPDATE,
// DELETE or TRUNCATE (42501), and the owner, who is exempt from RLS, is stopped
// by a trigger (55000). The row is the same afterwards.
func TestAuditImmutable(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	admin := uuid.UUID(w.users["adminA"])
	nodeID := uuid.UUID(w.nodes["A1"].ID)
	auditID, opID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, after, request_id)
		 VALUES ($1, $2, $3, $4, 'node.created', 'node', $4, '{"name":"A1"}'::jsonb, 'req-1')`, auditID, now, admin, nodeID)
	require.NoError(t, err)
	_, err = w.db.Owner.Exec(ctx,
		`INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at)
		 VALUES ($1, $2, 'credit', 10, 10, $3, $4)`, opID, nodeID, admin, now)
	require.NoError(t, err)

	rowText := func(table string) string {
		var s string
		require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM `+table+` x`).Scan(&s))
		return s
	}
	before := map[string]string{"audit_log": rowText("audit_log"), "operations": rowText("operations")}
	require.NotEmpty(t, before["audit_log"])

	for _, table := range []string{"audit_log", "operations"} {
		stmts := []string{`UPDATE ` + table + ` SET id = id`, `DELETE FROM ` + table, `TRUNCATE ` + table}
		for _, stmt := range stmts {
			t.Run("tb_app: "+stmt, func(t *testing.T) {
				appTx(ctx, t, w, admin, func(tx pgx.Tx) {
					_, err := tx.Exec(ctx, stmt)
					require.Error(t, err)
					assert.Equal(t, "42501", sqlState(err), "got: %v", err)
				})
			})
			t.Run("owner: "+stmt, func(t *testing.T) {
				_, err := w.db.Owner.Exec(ctx, stmt)
				require.Error(t, err)
				assert.Equal(t, "55000", sqlState(err), "got: %v", err)
				assert.Contains(t, err.Error(), table+" is append-only")
			})
		}
		// A narrower statement does not slip through either: a real column, a WHERE.
		t.Run("owner: update of a column with a WHERE on "+table, func(t *testing.T) {
			col := map[string]string{"audit_log": "request_id = 'x'", "operations": "comment = 'x'"}[table]
			_, err := w.db.Owner.Exec(ctx, `UPDATE `+table+` SET `+col+` WHERE id = $1`, map[string]uuid.UUID{"audit_log": auditID, "operations": opID}[table])
			require.Error(t, err)
			assert.Equal(t, "55000", sqlState(err), "got: %v", err)
		})
	}

	t.Run("tb_app cannot switch the triggers off for the session", func(t *testing.T) {
		appTx(ctx, t, w, admin, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `SET session_replication_role = replica`)
			require.Error(t, err)
			assert.Equal(t, "42501", sqlState(err), "got: %v", err)
		})
		appTx(ctx, t, w, admin, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER audit_log_immutable`)
			require.Error(t, err)
			assert.Equal(t, "42501", sqlState(err), "got: %v", err) // not the owner of the table
		})
	})

	t.Run("control: tb_app can append and read the entry back", func(t *testing.T) {
		appTx(ctx, t, w, admin, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx,
				`INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, request_id)
				 VALUES ($1, $2, $3, $4, 'node.renamed', 'node', $4, 'req-2')`, uuid.Must(uuid.NewV7()), now, admin, nodeID)
			require.NoError(t, err)
			var n int
			require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE node_id = $1`, nodeID).Scan(&n))
			assert.Equal(t, 2, n)
		})
	})

	assert.Equal(t, before["audit_log"], rowText("audit_log"), "audit_log changed")
	assert.Equal(t, before["operations"], rowText("operations"), "operations changed")
}
