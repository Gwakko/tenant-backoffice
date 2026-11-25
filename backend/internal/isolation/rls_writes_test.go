//go:build integration

package isolation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// Test-ID: T-ISO-20
//
// The nodes trigger deliberately does not check the path on an UPDATE that keeps
// parent_id, so a direct "SET path = <foreign path>" is stopped by the policy
// WITH CHECK alone. These cases fail if nodes_update.WITH CHECK is weakened.
func TestNodeWritesCannotEscapeBranch(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	actor := w.adminA.String()
	fresh := func(parent uuid.UUID) string { return w.pathOf[parent] + "." + label(uuid.New()) }

	t.Run("control: a move inside the own branch works", func(t *testing.T) {
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `UPDATE nodes SET name = 'renamed' WHERE id = $1`, w.a1u1)
			require.NoError(t, err)
			assert.EqualValues(t, 1, tag.RowsAffected())
		})
	})

	pathCases := map[string]string{
		"path inside branch B (fresh child of B1)":        fresh(w.b1),
		"path equal to an existing node of branch B":      w.pathOf[w.b1u1],
		"path of branch B itself":                         w.pathOf[w.b],
		"path of an ancestor above the access root":       w.pathOf[w.root],
		"fresh child of the ancestor above access root":   fresh(w.root),
		"unrelated fresh root path (outside every scope)": label(uuid.New()),
	}
	for name, newPath := range pathCases {
		t.Run("direct path update: "+name, func(t *testing.T) {
			before := snapshot(ctx, t, db)
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, `UPDATE nodes SET path = $1::ltree WHERE id = $2`, newPath, w.a1u1)
				require.Error(t, err)
				assert.Equal(t, rlsDenied, sqlState(err), "got: %v", err)
			})
			assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
		})
	}

	t.Run("re-parent into branch B", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `UPDATE nodes SET parent_id = $1 WHERE id = $2`, w.b1, w.a1u1)
			require.Error(t, err)
			assert.Contains(t, []string{"23514", rlsDenied}, sqlState(err), "got: %v", err)
		})
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})

	t.Run("re-parent and path together into branch B", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `UPDATE nodes SET parent_id = $1, path = $2::ltree WHERE id = $3`, w.b1, fresh(w.b1), w.a1u1)
			require.Error(t, err)
			assert.Contains(t, []string{"23514", rlsDenied}, sqlState(err), "got: %v", err)
		})
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})

	t.Run("delete nodes of branch B and above the access root", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		for _, id := range append(append([]uuid.UUID{}, w.nodesB...), w.root) {
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
				require.NoError(t, err)
				assert.Zero(t, tag.RowsAffected(), w.nameOf[id])
			})
		}
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})
}

// Test-ID: T-ISO-21
//
// tb_app can update only status and token_version of users. The password hash
// changes through tb_set_own_password, which touches the acting user only.
func TestUserPasswordWritePath(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	actor := w.adminA.String()

	hashes := func() map[uuid.UUID]string {
		out := map[uuid.UUID]string{}
		for _, id := range w.allUsers {
			var h string
			require.NoError(t, db.Owner.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, id).Scan(&h))
			out[id] = h
		}
		return out
	}

	t.Run("direct UPDATE of password_hash is denied", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		// Own row, a visible user of the actor's branch, an invisible user of another branch.
		for _, id := range []uuid.UUID{w.adminA, w.viewerA1u1, w.multi, w.adminB} {
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, `UPDATE users SET password_hash = 'pwned' WHERE id = $1`, id)
				require.Error(t, err)
				assert.Equal(t, rlsDenied, sqlState(err), "got: %v", err)
			})
		}
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `UPDATE users SET password_hash = 'pwned'`)
			require.Error(t, err)
			assert.Equal(t, rlsDenied, sqlState(err))
		})
		assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
	})

	t.Run("status and token_version stay updatable on visible users", func(t *testing.T) {
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `UPDATE users SET status = 'inactive', token_version = token_version + 1 WHERE id = $1`, w.viewerA1u1)
			require.NoError(t, err)
			assert.EqualValues(t, 1, tag.RowsAffected())
			tag, err = tx.Exec(ctx, `UPDATE users SET status = 'inactive' WHERE id = $1`, w.adminB)
			require.NoError(t, err)
			assert.Zero(t, tag.RowsAffected(), "foreign user is invisible")
		})
	})

	t.Run("columns other than status and token_version are not updatable", func(t *testing.T) {
		for _, col := range []string{"id", "email", "password_hash", "created_at"} {
			var priv bool
			require.NoError(t, db.Owner.QueryRow(ctx, `SELECT has_column_privilege('tb_app', 'public.users', $1, 'UPDATE')`, col).Scan(&priv))
			assert.False(t, priv, col)
		}
		for _, col := range []string{"status", "token_version"} {
			var priv bool
			require.NoError(t, db.Owner.QueryRow(ctx, `SELECT has_column_privilege('tb_app', 'public.users', $1, 'UPDATE')`, col).Scan(&priv))
			assert.True(t, priv, col)
		}
	})

	t.Run("tb_set_own_password changes only the acting user", func(t *testing.T) {
		before := hashes()
		// inTx always rolls back; this one must commit to prove persistence.
		tx, err := db.App.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, actor)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `SELECT tb_set_own_password('new-hash')`)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
		after := hashes()
		for _, id := range w.allUsers {
			if id == w.adminA {
				assert.Equal(t, "new-hash", after[id])
			} else {
				assert.Equal(t, before[id], after[id], "other user %s must keep the hash", w.emails[id])
			}
		}
	})

	t.Run("tb_set_own_password without a usable actor changes nothing", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		hBefore := hashes()
		for _, a := range []string{"", uuid.NewString()} {
			inTx(ctx, t, db.App, a, func(tx pgx.Tx) {
				var n int
				require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT tb_set_own_password('x')) q`).Scan(&n))
				assert.Equal(t, 1, n, "void result, no data returned")
			})
		}
		assert.Equal(t, before, snapshot(ctx, t, db))
		assert.Equal(t, hBefore, hashes())
	})

	t.Run("SELECT * on users is still denied", func(t *testing.T) {
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `SELECT * FROM users`)
			require.Error(t, err)
			assert.Equal(t, rlsDenied, sqlState(err))
		})
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `SELECT password_hash FROM users`)
			require.Error(t, err)
			assert.Equal(t, rlsDenied, sqlState(err))
		})
	})

	t.Run("INSERT of a user with an initial hash still works", func(t *testing.T) {
		inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, 'fresh@example.test', 'h', 'active', now())`, uuid.New())
			require.NoError(t, err)
			assert.EqualValues(t, 1, tag.RowsAffected())
		})
	})
}

// Test-ID: T-ISO-22
//
// Token refresh needs no credential: tb_auth_user_by_id must not expose the
// password hash, while tb_auth_user_by_email (login) still does.
func TestAuthByIDHasNoPasswordHash(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)

	columns := func(tx pgx.Tx, query string, arg any) []string {
		rows, err := tx.Query(ctx, query, arg)
		require.NoError(t, err)
		defer rows.Close()
		var names []string
		for _, f := range rows.FieldDescriptions() {
			names = append(names, f.Name)
		}
		require.True(t, rows.Next(), "one row expected")
		require.NoError(t, rows.Err())
		return names
	}
	inTx(ctx, t, db.App, "", func(tx pgx.Tx) {
		assert.Equal(t, []string{"id", "status", "token_version"},
			columns(tx, `SELECT * FROM tb_auth_user_by_id($1)`, w.adminA))
	})
	inTx(ctx, t, db.App, "", func(tx pgx.Tx) {
		assert.Contains(t, columns(tx, `SELECT * FROM tb_auth_user_by_email($1)`, w.emails[w.adminA]), "password_hash")
	})
	inTx(ctx, t, db.App, "", func(tx pgx.Tx) {
		_, err := tx.Exec(ctx, `SELECT password_hash FROM tb_auth_user_by_id($1)`, w.adminA)
		require.Error(t, err)
		assert.Equal(t, "42703", sqlState(err))
	})
}
