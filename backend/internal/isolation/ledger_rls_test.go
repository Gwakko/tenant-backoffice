//go:build integration

package isolation_test

import (
	"context"
	"crypto/sha256"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// The tests in this file go straight at the tables of migrations 0004...0007
// with raw SQL under tb_app (no use case, no repository): they pin the RLS
// policies and the grants of the settings, ledger, idempotency and audit tables.
// They have no spec id yet; see the report of M3.1 for the proposed T-ISO ids.

const (
	checkViolation = "23514"
	triggerRaised  = "55000"
)

// sorted returns the ids as sorted strings, for set comparisons.
func sorted(ids ...[]uuid.UUID) []string {
	var out []string
	for _, part := range ids {
		for _, id := range part {
			out = append(out, id.String())
		}
	}
	sort.Strings(out)
	return out
}

// denied runs stmt as actor in its own transaction and requires SQLSTATE 42501.
func denied(ctx context.Context, t *testing.T, db *testkit.DB, actor, stmt string, args ...any) {
	t.Helper()
	inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
		_, err := tx.Exec(ctx, stmt, args...)
		require.Error(t, err, stmt)
		assert.Equal(t, rlsDenied, sqlState(err), "%s: got %v", stmt, err)
	})
}

// affects runs stmt as actor and requires exactly n affected rows.
func affects(ctx context.Context, t *testing.T, db *testkit.DB, actor string, n int64, stmt string, args ...any) {
	t.Helper()
	inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
		tag, err := tx.Exec(ctx, stmt, args...)
		require.NoError(t, err, stmt)
		assert.Equal(t, n, tag.RowsAffected(), stmt)
	})
}

// visibleIDs returns the uuid column of query as sorted strings, as actor.
func visibleIDs(ctx context.Context, t *testing.T, db *testkit.DB, actor, query string) []string {
	t.Helper()
	var out []string
	inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
		out = sorted(ids(ctx, t, tx, query))
	})
	return out
}

// Proposed id T-ISO-24 (no actor, unknown, inactive or role-less actor).
func TestLedgerTablesNeedAnActor(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	fresh := w.AddNode(ctx, t, "unit", &w.A1, "A1u3") // visible to AdminA, has no balance yet

	tables := []string{"node_settings", "balances", "operations", "idempotency_keys", "audit_log"}

	// Control: the owner sees rows in every table, so "0 rows" below is RLS.
	for _, table := range tables {
		var n int
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n))
		require.Positive(t, n, table)
	}

	sessions := map[string]string{
		"actor never set":              "",
		"unknown actor":                uuid.NewString(),
		"inactive user with a role":    w.Inactive.String(),
		"active user without any role": w.Orphan.String(),
	}
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte("x"))
	for name, actor := range sessions {
		t.Run(name, func(t *testing.T) {
			before := snapshot(ctx, t, db)
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				for _, table := range tables {
					assert.Zero(t, count(ctx, t, tx, "SELECT 1 FROM "+table), table)
				}
			})
			// Updates and deletes the grants allow touch no row.
			for _, stmt := range []string{
				`UPDATE node_settings SET updated_at = now()`, `DELETE FROM node_settings`,
				`UPDATE balances SET amount = amount + 1`,
				`DELETE FROM idempotency_keys`,
			} {
				affects(ctx, t, db, actor, 0, stmt)
			}
			// Inserts are rejected, each in its own transaction (the error aborts it).
			// The actor named inside the row is the one the session claims to be, so
			// only the missing or unusable actor can be the reason.
			rowActor := w.AdminA
			if actor != "" {
				if id, err := uuid.Parse(actor); err == nil {
					rowActor = id
				}
			}
			denied(ctx, t, db, actor, `INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, 'operations_enabled', 'true', $2, $3)`, w.A1u1, now, rowActor)
			denied(ctx, t, db, actor, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, fresh, now)
			denied(ctx, t, db, actor, `INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at) VALUES ($1, $2, 'credit', 1, 1, $3, $4)`, uuid.New(), w.A1u1, rowActor, now)
			denied(ctx, t, db, actor, `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at) VALUES ($1, 'k', $2, $3, $4, $5)`, rowActor, sum[:], uuid.New(), now, now.Add(time.Hour))
			denied(ctx, t, db, actor, `INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, request_id) VALUES ($1, $2, $3, $4, 'node.created', 'node', $4, 'r')`, uuid.New(), now, rowActor, w.A1u1)
			assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
		})
	}
}

// Proposed id T-ISO-25 (branch A against branch B on every new table).
func TestLedgerTablesCrossBranch(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte("x"))
	a, b := w.AdminA.String(), w.AdminB.String()
	freshB := w.AddNode(ctx, t, "unit", &w.B1, "B1u3") // no balance row yet
	freshA := w.AddNode(ctx, t, "unit", &w.A1, "A1u3")

	t.Run("reads show the own branch only", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		type want struct{ settings, balances, ops, audit []uuid.UUID }
		// Settings are keyed by node: A has operations_enabled, A1 max_operation_amount;
		// the root's display_timezone lies above the access root of both admins.
		cases := map[string]struct {
			actor string
			want  want
		}{
			"admin of A": {a, want{[]uuid.UUID{w.A, w.A1}, w.NodesA, w.OpsA, w.AuditA}},
			"admin of B": {b, want{[]uuid.UUID{w.B, w.B1}, w.NodesB, w.OpsB, w.AuditB}},
		}
		for name, c := range cases {
			assert.Equal(t, sorted(c.want.settings), visibleIDs(ctx, t, db, c.actor, `SELECT node_id FROM node_settings`), name+": node_settings")
			assert.Equal(t, sorted(c.want.balances), visibleIDs(ctx, t, db, c.actor, `SELECT node_id FROM balances`), name+": balances")
			assert.Equal(t, sorted(c.want.ops), visibleIDs(ctx, t, db, c.actor, `SELECT id FROM operations`), name+": operations")
			assert.Equal(t, sorted(c.want.audit), visibleIDs(ctx, t, db, c.actor, `SELECT id FROM audit_log`), name+": audit_log")
		}
		// The user who is admin of A1 and viewer of B1 sees the union of both subtrees.
		multi := w.Multi.String()
		assert.Equal(t, sorted(w.OpsA, w.OpsB), visibleIDs(ctx, t, db, multi, `SELECT id FROM operations`), "multi: operations")
		assert.Equal(t, sorted(w.AuditA, w.AuditB), visibleIDs(ctx, t, db, multi, `SELECT id FROM audit_log`), "multi: audit_log")
		// The root's audit entry and settings are visible to the root owner only.
		assert.Contains(t, visibleIDs(ctx, t, db, w.OwnerRoot.String(), `SELECT id FROM audit_log`), w.AuditRoot.String())
		assert.NotContains(t, visibleIDs(ctx, t, db, a, `SELECT id FROM audit_log`), w.AuditRoot.String())
		assert.Equal(t, before, snapshot(ctx, t, db))
	})

	t.Run("updates and deletes of foreign rows touch nothing", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		// Own row: the control (rolled back by inTx).
		affects(ctx, t, db, a, 1, `UPDATE node_settings SET updated_at = now(), updated_by = $2 WHERE node_id = $1`, w.A1, w.AdminA)
		affects(ctx, t, db, a, 1, `UPDATE balances SET amount = 1, version = version + 1 WHERE node_id = $1`, w.A1u1)
		affects(ctx, t, db, a, 1, `DELETE FROM node_settings WHERE node_id = $1`, w.A1)
		// Foreign rows: filtered out by the policies, 0 rows, no error.
		affects(ctx, t, db, a, 0, `UPDATE node_settings SET updated_at = now() WHERE node_id = ANY($1)`, w.NodesB)
		affects(ctx, t, db, a, 0, `UPDATE balances SET amount = 7 WHERE node_id = ANY($1)`, w.NodesB)
		affects(ctx, t, db, a, 0, `DELETE FROM node_settings WHERE node_id = ANY($1)`, append(append([]uuid.UUID{}, w.NodesB...), w.Root))
		affects(ctx, t, db, a, 0, `UPDATE node_settings SET updated_at = now() WHERE node_id = $1`, w.Root)
		affects(ctx, t, db, a, 0, `DELETE FROM idempotency_keys WHERE user_id = $1`, w.AdminB)
		affects(ctx, t, db, b, 0, `DELETE FROM idempotency_keys WHERE user_id = $1`, w.AdminA)
		// Not even a row an own admin sees can be moved to a foreign node.
		denied(ctx, t, db, a, `UPDATE node_settings SET updated_by = $1 WHERE node_id = $2`, w.AdminB, w.A1)
		// operations and audit_log have no UPDATE and DELETE grant at all.
		for _, actor := range []string{a, b} {
			denied(ctx, t, db, actor, `UPDATE operations SET amount = 1`)
			denied(ctx, t, db, actor, `DELETE FROM operations`)
			denied(ctx, t, db, actor, `UPDATE audit_log SET action = 'x'`)
			denied(ctx, t, db, actor, `DELETE FROM audit_log`)
		}
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})

	t.Run("inserts for a foreign node are refused", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		// Controls: the same statements for an own node pass.
		affects(ctx, t, db, a, 1, `INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, 'display_timezone', '"Europe/Kyiv"', $2, $3)`, w.A1u1, now, w.AdminA)
		affects(ctx, t, db, a, 1, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, freshA, now)
		affects(ctx, t, db, a, 1, `INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at) VALUES ($1, $2, 'credit', 1, 1, $3, $4)`, uuid.New(), w.A1u1, w.AdminA, now)
		affects(ctx, t, db, a, 1, `INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, request_id) VALUES ($1, $2, $3, $4, 'node.created', 'node', $4, 'r')`, uuid.New(), now, w.AdminA, w.A1u1)
		affects(ctx, t, db, a, 1, `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at) VALUES ($1, 'fresh', $2, $3, $4, $5)`, w.AdminA, sum[:], uuid.New(), now, now.Add(time.Hour))

		// A foreign node, a node above the access root, a missing node: all the same 42501.
		for _, node := range []uuid.UUID{w.B1u1, w.B, w.Root, uuid.New()} {
			denied(ctx, t, db, a, `INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, 'display_timezone', '"UTC"', $2, $3)`, node, now, w.AdminA)
			denied(ctx, t, db, a, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, node, now)
			denied(ctx, t, db, a, `INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at) VALUES ($1, $2, 'credit', 1, 1, $3, $4)`, uuid.New(), node, w.AdminA, now)
			denied(ctx, t, db, a, `INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, request_id) VALUES ($1, $2, $3, $4, 'node.created', 'node', $4, 'r')`, uuid.New(), now, w.AdminA, node)
		}
		denied(ctx, t, db, a, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, freshB, now)

		// Spoofing the author: an operation, an audit entry or a setting written
		// for an own node but in the name of somebody else, and a key of another user.
		denied(ctx, t, db, a, `INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at) VALUES ($1, $2, 'credit', 1, 1, $3, $4)`, uuid.New(), w.A1u1, w.AdminB, now)
		denied(ctx, t, db, a, `INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, request_id) VALUES ($1, $2, $3, $4, 'node.created', 'node', $4, 'r')`, uuid.New(), now, w.AdminB, w.A1u1)
		denied(ctx, t, db, a, `INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, 'operations_enabled', 'false', $2, $3)`, w.A1u1, now, w.AdminB)
		denied(ctx, t, db, a, `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at) VALUES ($1, 'spoof', $2, $3, $4, $5)`, w.AdminB, sum[:], uuid.New(), now, now.Add(time.Hour))

		// A counterparty the actor cannot see is refused by the policy, not probed
		// through the foreign key: an existing foreign node, a missing node and a
		// node above the access root give the same answer.
		transfer := uuid.New()
		for _, cp := range []uuid.UUID{w.B1u1, w.Root, uuid.New()} {
			denied(ctx, t, db, a, `INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, actor_id, created_at) VALUES ($1, $2, 'transfer_out', 1, 1, $3, $4, $5, $6)`, uuid.New(), w.A1u1, transfer, cp, w.AdminA, now)
		}
		affects(ctx, t, db, a, 1, `INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, actor_id, created_at) VALUES ($1, $2, 'transfer_out', 1, 1, $3, $4, $5, $6)`, uuid.New(), w.A1u1, transfer, w.A1u2, w.AdminA, now)
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})

	t.Run("a balance goes with its node and an operation pins it", func(t *testing.T) {
		// freshA has no history: deleting it as tb_app removes its balance too
		// (the cascade runs with the owner's rights, tb_app has no DELETE on balances).
		inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, freshA, now)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, `DELETE FROM balances WHERE node_id = $1`, freshA)
			require.Error(t, err)
			assert.Equal(t, rlsDenied, sqlState(err))
		})
		inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `INSERT INTO balances (node_id, amount, updated_at) VALUES ($1, 0, $2)`, freshA, now)
			require.NoError(t, err)
			tag, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, freshA)
			require.NoError(t, err)
			assert.EqualValues(t, 1, tag.RowsAffected())
			assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM balances WHERE node_id = $1`, freshA))
		})
		// A node with ledger rows cannot be deleted (ON DELETE RESTRICT).
		inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, w.A1u1)
			require.Error(t, err)
			assert.Equal(t, "23001", sqlState(err), "got: %v", err)
		})
	})
}

// Proposed id T-ISO-26 (idempotency keys are scoped to the acting user).
func TestIdempotencyKeyScope(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	now := time.Now().UTC()
	a, b := w.AdminA.String(), w.AdminB.String()
	sum := sha256.Sum256([]byte("body"))

	lookup := func(actor, key string) (op *uuid.UUID, found bool) {
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			rows, err := tx.Query(ctx, `SELECT operation_id FROM idempotency_keys WHERE key = $1`, key)
			require.NoError(t, err)
			got, err := pgx.CollectRows(rows, pgx.RowTo[*uuid.UUID])
			require.NoError(t, err)
			require.LessOrEqual(t, len(got), 1, "a key text matches at most one of the actor's rows")
			if len(got) == 1 {
				op, found = got[0], true
			}
		})
		return op, found
	}

	t.Run("the same key text is two independent keys", func(t *testing.T) {
		opA, okA := lookup(a, w.KeyShared)
		opB, okB := lookup(b, w.KeyShared)
		require.True(t, okA)
		require.True(t, okB)
		assert.Equal(t, w.CreditA, *opA)
		assert.Equal(t, w.CreditB, *opB)
		// Another branch's key text is not even visible as existing.
		_, found := lookup(a, w.KeyOnlyB)
		assert.False(t, found, "A must not see B's key")
		_, found = lookup(b, w.KeyOnlyA)
		assert.False(t, found, "B must not see A's key")
		// The owner of both branches' parent does not see them either: keys are per user.
		_, found = lookup(w.OwnerRoot.String(), w.KeyShared)
		assert.False(t, found, "keys belong to the user, not to a subtree")
	})

	t.Run("a key of the other branch does not conflict and is not revealed", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
			// A uses the text of B's private key: no conflict, a new row of A.
			var op uuid.UUID
			err := tx.QueryRow(ctx, `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, key) DO NOTHING RETURNING operation_id`,
				w.AdminA, w.KeyOnlyB, sum[:], w.CreditA, now, now.Add(time.Hour)).Scan(&op)
			require.NoError(t, err)
			assert.Equal(t, w.CreditA, op)
			// A's own key conflicts: nothing is inserted.
			tag, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, key) DO NOTHING`,
				w.AdminA, w.KeyShared, sum[:], w.CreditA, now, now.Add(time.Hour))
			require.NoError(t, err)
			assert.Zero(t, tag.RowsAffected())
		})
		assert.Equal(t, before, snapshot(ctx, t, db), "rolled back, nothing may remain")
	})

	t.Run("a stored key is immutable", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		denied(ctx, t, db, a, `UPDATE idempotency_keys SET request_hash = $1 WHERE key = $2`, sum[:], w.KeyShared)
		denied(ctx, t, db, a, `UPDATE idempotency_keys SET expires_at = now() + interval '1 year'`)
		assert.Equal(t, before, snapshot(ctx, t, db))
	})

	t.Run("the table rejects malformed keys", func(t *testing.T) {
		ins := `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, transfer_id, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`
		op, tr := uuid.New(), uuid.New()
		bad := map[string][]any{
			"empty key":             {w.AdminA, "", sum[:], &op, nil, now, now.Add(time.Hour)},
			"key with a space":      {w.AdminA, "a b", sum[:], &op, nil, now, now.Add(time.Hour)},
			"key with a slash":      {w.AdminA, "a/b", sum[:], &op, nil, now, now.Add(time.Hour)},
			"129 characters":        {w.AdminA, repeat("k", 129), sum[:], &op, nil, now, now.Add(time.Hour)},
			"short hash":            {w.AdminA, "k1", sum[:8], &op, nil, now, now.Add(time.Hour)},
			"no result reference":   {w.AdminA, "k2", sum[:], nil, nil, now, now.Add(time.Hour)},
			"two result references": {w.AdminA, "k3", sum[:], &op, &tr, now, now.Add(time.Hour)},
			"already expired":       {w.AdminA, "k4", sum[:], &op, nil, now, now},
		}
		for name, args := range bad {
			inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, ins, args...)
				require.Error(t, err, name)
				assert.Equal(t, checkViolation, sqlState(err), "%s: %v", name, err)
			})
		}
		inTx(ctx, t, db.App, a, func(tx pgx.Tx) { // a transfer key and 128 characters are fine
			_, err := tx.Exec(ctx, ins, w.AdminA, repeat("k", 128), sum[:], nil, &tr, now, now.Add(time.Hour))
			require.NoError(t, err)
		})
	})

	t.Run("a parallel duplicate waits for the first and then conflicts", func(t *testing.T) {
		const key = "parallel-key"
		ins := `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, key) DO NOTHING`
		begin := func(actor uuid.UUID) pgx.Tx {
			tx, err := db.App.Begin(ctx)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, actor.String())
			require.NoError(t, err)
			return tx
		}
		first, second := begin(w.AdminA), begin(w.AdminA)
		defer func() { _ = first.Rollback(ctx); _ = second.Rollback(ctx) }()

		var firstPID, secondPID int32
		require.NoError(t, first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID))
		require.NoError(t, second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID))
		tag, err := first.Exec(ctx, ins, w.AdminA, key, sum[:], uuid.New(), now, now.Add(time.Hour))
		require.NoError(t, err)
		require.EqualValues(t, 1, tag.RowsAffected())

		var wg sync.WaitGroup
		var secondTag int64 = -1
		var secondErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			tg, e := second.Exec(ctx, ins, w.AdminA, key, sum[:], uuid.New(), now, now.Add(time.Hour))
			secondErr = e
			if e == nil {
				secondTag = tg.RowsAffected()
			}
		}()
		// The second insert must be blocked by the first one's backend.
		require.Eventually(t, func() bool {
			var blockers []int32
			require.NoError(t, db.Owner.QueryRow(ctx, `SELECT pg_blocking_pids($1)`, secondPID).Scan(&blockers))
			return len(blockers) == 1 && blockers[0] == firstPID
		}, 10*time.Second, 20*time.Millisecond, "the duplicate does not wait for the first transaction")
		require.NoError(t, first.Commit(ctx))
		wg.Wait()
		require.NoError(t, secondErr)
		assert.Zero(t, secondTag, "the waiter must end as a conflict, inserting nothing")
		require.NoError(t, second.Commit(ctx))

		var n int
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n))
		assert.Equal(t, 1, n)
	})

	t.Run("two users with one key text never wait for each other", func(t *testing.T) {
		const key = "concurrent-key"
		ins := `INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, key) DO NOTHING`
		ctxT, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var txs []pgx.Tx
		for _, u := range []uuid.UUID{w.AdminA, w.AdminB} {
			tx, err := db.App.Begin(ctxT)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			_, err = tx.Exec(ctxT, `SELECT set_config('app.actor_id', $1, true)`, u.String())
			require.NoError(t, err)
			tag, err := tx.Exec(ctxT, ins, u, key, sum[:], uuid.New(), now, now.Add(time.Hour))
			require.NoError(t, err, "the second user must not block")
			require.EqualValues(t, 1, tag.RowsAffected())
			txs = append(txs, tx)
		}
		for _, tx := range txs {
			require.NoError(t, tx.Commit(ctxT))
		}
		var n int
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n))
		assert.Equal(t, 2, n)
	})
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

// Proposed id T-ISO-27 (tb_settings_chain shows the chain, never a foreign node).
func TestSettingsChainFunction(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)

	type link struct {
		source *uuid.UUID
		depth  int
		key    string
		value  string
	}
	chain := func(actor string, node uuid.UUID) []link {
		var out []link
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			rows, err := tx.Query(ctx, `SELECT source_node_id, depth, key, value::text FROM tb_settings_chain($1)`, node)
			require.NoError(t, err)
			out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (link, error) {
				var l link
				return l, r.Scan(&l.source, &l.depth, &l.key, &l.value)
			})
			require.NoError(t, err)
		})
		return out
	}
	idp := func(id uuid.UUID) *uuid.UUID { return &id }

	t.Run("admin of A sees the chain; the root's id stays hidden", func(t *testing.T) {
		got := chain(w.AdminA.String(), w.A1u1)
		assert.Equal(t, []link{
			{nil, 1, "display_timezone", `"UTC"`}, // the root: value and depth, but no node id
			{idp(w.A), 2, "operations_enabled", `true`},
			{idp(w.A1), 3, "max_operation_amount", `"5000"`},
		}, got)
		inTx(ctx, t, db.App, w.AdminA.String(), func(tx pgx.Tx) {
			assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM node_settings WHERE node_id = $1`, w.Root), "direct read of the root's row stays hidden")
		})
	})
	t.Run("a deeper role hides more ids", func(t *testing.T) {
		got := chain(w.ViewerA1u1.String(), w.A1u1)
		require.Len(t, got, 3)
		for _, l := range got {
			assert.Nil(t, l.source, "no ancestor of the unit is visible to its viewer: %v", l)
		}
	})
	t.Run("the root owner sees every id", func(t *testing.T) {
		got := chain(w.OwnerRoot.String(), w.A1u1)
		require.Len(t, got, 3)
		assert.Equal(t, w.Root, *got[0].source)
	})
	t.Run("a foreign, missing or ancestor node and unusable actors give nothing", func(t *testing.T) {
		assert.Empty(t, chain(w.AdminA.String(), w.B1u1), "foreign node")
		assert.Empty(t, chain(w.AdminB.String(), w.A1), "foreign node, other direction")
		assert.Empty(t, chain(w.AdminA.String(), w.Root), "ancestor above the access root")
		assert.Empty(t, chain(w.AdminA.String(), uuid.New()), "missing node")
		assert.Empty(t, chain("", w.A1u1), "no actor")
		assert.Empty(t, chain(uuid.NewString(), w.A1u1), "unknown actor")
		assert.Empty(t, chain(w.Inactive.String(), w.A1u1), "inactive actor")
		assert.Empty(t, chain(w.Orphan.String(), w.A1u1), "actor without a role")
	})
	t.Run("the same chain is reached from B", func(t *testing.T) {
		got := chain(w.AdminB.String(), w.B1u2)
		assert.Equal(t, []link{
			{nil, 1, "display_timezone", `"UTC"`},
			{idp(w.B), 2, "operations_enabled", `true`},
			{idp(w.B1), 3, "max_operation_amount", `"7000"`},
		}, got)
	})
}
