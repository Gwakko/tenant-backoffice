//go:build integration

package isolation_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

const rlsDenied = "42501"

// Test-ID: T-ISO-12
func TestNoActorSeesNothing(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)

	// Control: the owner sees everything, so "0 rows" below is RLS and not an empty database.
	var total int
	require.NoError(t, db.Owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM nodes) + (SELECT count(*) FROM users) + (SELECT count(*) FROM role_assignments)`).Scan(&total))
	require.Equal(t, len(w.allNodes)+len(w.allUsers)+w.roleRows, total)

	// Sessions that carry no usable actor.
	sessions := map[string]string{
		"actor never set":    "",
		"unknown actor uuid": uuid.NewString(),
	}
	for name, actor := range sessions {
		t.Run(name, func(t *testing.T) {
			before := snapshot(ctx, t, db)
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				for _, table := range []string{"nodes", "users", "role_assignments"} {
					assert.Zero(t, count(ctx, t, tx, "SELECT 1 FROM "+table), table)
				}
				assert.Zero(t, count(ctx, t, tx, `SELECT n.id FROM nodes n JOIN role_assignments ra ON ra.node_id = n.id`))
				assert.Zero(t, count(ctx, t, tx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM nodes) OR EXISTS (SELECT 1 FROM users)`))
			})
			// UPDATE and DELETE touch nothing.
			for _, stmt := range []string{
				`UPDATE nodes SET name = 'x'`, `DELETE FROM nodes`,
				`UPDATE role_assignments SET role = 'viewer'`, `DELETE FROM role_assignments`,
				`UPDATE users SET status = 'inactive'`,
			} {
				inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
					tag, err := tx.Exec(ctx, stmt)
					require.NoError(t, err, stmt)
					assert.Zero(t, tag.RowsAffected(), stmt)
				})
			}
			// INSERT is rejected (each in its own transaction: the error aborts it).
			inserts := map[string][]any{
				`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
				 VALUES ($1::uuid, 'platform', NULL, replace($1::uuid::text, '-', '')::ltree, 'p', 'p', now(), now())`: {uuid.New()},
				`INSERT INTO role_assignments (user_id, node_id, role, granted_at) VALUES ($1, $2, 'viewer', now())`:   {w.orphan, w.a},
				`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, 'new@example.test', 'h', now())`: {uuid.New()},
			}
			for stmt, args := range inserts {
				inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
					_, err := tx.Exec(ctx, stmt, args...)
					require.Error(t, err, stmt)
					assert.Equal(t, rlsDenied, sqlState(err), stmt)
				})
			}
			assert.Equal(t, before, snapshot(ctx, t, db), "data changed")
		})
	}

	t.Run("empty string actor", func(t *testing.T) {
		// A cleared setting (as after a finished transaction) means no actor.
		tx, err := db.App.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', '', true)`)
		require.NoError(t, err)
		assert.Zero(t, count(ctx, t, tx, `SELECT * FROM nodes`))
	})

	t.Run("no transaction at all", func(t *testing.T) {
		var n int
		require.NoError(t, db.App.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n))
		assert.Zero(t, n)
		require.NoError(t, db.App.QueryRow(ctx, `SELECT count(*) FROM role_assignments`).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("garbage actor fails closed with an error", func(t *testing.T) {
		inTx(ctx, t, db.App, "not-a-uuid", func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `SELECT count(*) FROM nodes`)
			require.Error(t, err)
			assert.Equal(t, "22P02", sqlState(err))
		})
	})

	t.Run("functions", func(t *testing.T) {
		inTx(ctx, t, db.App, "", func(tx pgx.Tx) {
			var scopes []string
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_actor_scopes()::text[]`).Scan(&scopes))
			assert.Empty(t, scopes)
			var id *uuid.UUID
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_actor_id()`).Scan(&id))
			assert.Nil(t, id)
			var active *bool
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_node_effective_active($1)`, w.a1).Scan(&active))
			assert.Nil(t, active)
			var found *uuid.UUID
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_user_id_by_email($1)`, w.emails[w.adminB]).Scan(&found))
			assert.Nil(t, found, "tb_user_id_by_email must answer only for an actor")
		})
	})

	t.Run("inactive actor", func(t *testing.T) {
		inTx(ctx, t, db.App, w.inactive.String(), func(tx pgx.Tx) {
			assert.Zero(t, count(ctx, t, tx, `SELECT * FROM nodes`))
			assert.Zero(t, count(ctx, t, tx, `SELECT * FROM role_assignments`))
			assert.Equal(t, []uuid.UUID{w.inactive}, ids(ctx, t, tx, `SELECT id FROM users`), "only the own row")
		})
	})
}

// Test-ID: T-ISO-11
func TestRLSWithoutUseCaseChecks(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)

	cases := []struct {
		name         string
		actor        uuid.UUID
		own, foreign []uuid.UUID
	}{
		{"admin of A", w.adminA, w.nodesA, w.nodesB},
		{"admin of B", w.adminB, w.nodesB, w.nodesA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actor := c.actor.String()
			before := snapshot(ctx, t, db)

			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				// The actor sees exactly its own branch, never the root above it.
				assert.ElementsMatch(t, c.own, ids(ctx, t, tx, `SELECT id FROM nodes`))
				for _, f := range append(append([]uuid.UUID{}, c.foreign...), w.root) {
					assert.Zero(t, count(ctx, t, tx, `SELECT id FROM nodes WHERE id = $1`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT id FROM nodes WHERE id IN (SELECT $1::uuid)`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT ra.user_id FROM role_assignments ra WHERE ra.node_id = $1`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM role_assignments ra JOIN nodes n ON n.id = ra.node_id WHERE n.id = $1`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT * FROM nodes WHERE id = $1 UNION ALL SELECT * FROM nodes WHERE id = $1`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM nodes WHERE id = $1)`, f))
					assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM nodes WHERE path <@ (SELECT path FROM nodes WHERE id = $1)`, f))
				}
				// Subtree and name probes across the branch boundary.
				assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM nodes WHERE name = $1`, w.nameOf[c.foreign[0]]))
				assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM nodes WHERE path <@ $1::ltree`, w.pathOf[c.foreign[0]]))
				// Users: only people with an assignment in the own branch, plus the actor.
				for _, u := range []uuid.UUID{w.onlyB, w.ownerRoot, w.orphan} {
					if u == w.onlyB && c.actor == w.adminB {
						continue
					}
					assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM users WHERE id = $1`, u), w.emails[u])
				}
				// A multi-branch user is visible, but only the own-branch assignment is.
				assert.Equal(t, 1, count(ctx, t, tx, `SELECT 1 FROM users WHERE id = $1`, w.multi))
				assert.Equal(t, 1, count(ctx, t, tx, `SELECT 1 FROM role_assignments WHERE user_id = $1`, w.multi))
				// The password hash is not readable through SQL at all.
				_, err := tx.Exec(ctx, `SELECT password_hash FROM users`)
				assert.Equal(t, rlsDenied, sqlState(err))
			})

			// Functions give no way around the policies.
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				var scopes []string
				require.NoError(t, tx.QueryRow(ctx, `SELECT tb_actor_scopes()::text[]`).Scan(&scopes))
				require.Len(t, scopes, 1)
				for _, f := range c.foreign {
					var active *bool
					require.NoError(t, tx.QueryRow(ctx, `SELECT tb_node_effective_active($1)`, f).Scan(&active))
					assert.Nil(t, active, "effective activity of a foreign node must be NULL")
				}
				var active *bool
				require.NoError(t, tx.QueryRow(ctx, `SELECT tb_node_effective_active($1)`, c.own[1]).Scan(&active))
				require.NotNil(t, active)
				assert.True(t, *active)
			})

			// UPDATE and DELETE of foreign rows affect nothing.
			for _, f := range c.foreign {
				for _, stmt := range []string{
					`UPDATE nodes SET name = 'hijacked', name_key = 'hijacked' WHERE id = $1`,
					`DELETE FROM nodes WHERE id = $1`,
					`UPDATE role_assignments SET role = 'owner' WHERE node_id = $1`,
					`DELETE FROM role_assignments WHERE node_id = $1`,
					`UPDATE users SET status = 'inactive' WHERE id IN (SELECT user_id FROM role_assignments WHERE node_id = $1)`,
				} {
					inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
						tag, err := tx.Exec(ctx, stmt, f)
						require.NoError(t, err, stmt)
						assert.Zero(t, tag.RowsAffected(), stmt)
					})
				}
			}

			// Writes into the foreign branch are rejected.
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx,
					`INSERT INTO role_assignments (user_id, node_id, role, granted_at) VALUES ($1, $2, 'viewer', now())`,
					w.orphan, c.foreign[1])
				require.Error(t, err)
				assert.Equal(t, rlsDenied, sqlState(err))
			})
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				id := uuid.Must(uuid.NewV7())
				_, err := tx.Exec(ctx,
					`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
					 VALUES ($1, 'unit', $2, $3::ltree, 'x', 'x', now(), now())`,
					id, c.foreign[1], w.pathOf[c.foreign[1]]+"."+label(id))
				require.Error(t, err, "a node under a foreign parent must not be created")
				// The parent trigger reads the parent under RLS, so it fires first (check_violation).
				assert.Contains(t, []string{rlsDenied, "23514"}, sqlState(err))
			})
			// Moving an own node into the foreign branch fails the WITH CHECK.
			inTx(ctx, t, db.App, actor, func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, `UPDATE nodes SET path = $2::ltree || subpath(path, -1) WHERE id = $1`,
					c.own[2], w.pathOf[c.foreign[1]])
				require.Error(t, err)
			})
			assert.Equal(t, before, snapshot(ctx, t, db), "foreign data changed")
		})
	}

	t.Run("role on a unit does not reveal ancestors", func(t *testing.T) {
		inTx(ctx, t, db.App, w.viewerA1u1.String(), func(tx pgx.Tx) {
			assert.Equal(t, []uuid.UUID{w.a1u1}, ids(ctx, t, tx, `SELECT id FROM nodes`))
			assert.Zero(t, count(ctx, t, tx, `SELECT 1 FROM nodes WHERE id = ANY($1)`, []uuid.UUID{w.a1, w.a, w.root, w.a1u2}))
		})
	})

	t.Run("multi-branch user sees both of its subtrees and nothing else", func(t *testing.T) {
		inTx(ctx, t, db.App, w.multi.String(), func(tx pgx.Tx) {
			got := ids(ctx, t, tx, `SELECT id FROM nodes`)
			want := []uuid.UUID{w.a1, w.a1u1, w.a1u2, w.b1, w.b1u1, w.b1u2}
			sortIDs(got)
			sortIDs(want)
			assert.Equal(t, want, got)
		})
	})

	t.Run("effective activity sees inactive ancestors above the access root", func(t *testing.T) {
		_, err := db.Owner.Exec(ctx, `UPDATE nodes SET status = 'inactive' WHERE id = $1`, w.root)
		require.NoError(t, err)
		inTx(ctx, t, db.App, w.adminA.String(), func(tx pgx.Tx) {
			var active *bool
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_node_effective_active($1)`, w.a1).Scan(&active))
			require.NotNil(t, active)
			assert.False(t, *active)
			assert.Equal(t, 1, count(ctx, t, tx, `SELECT 1 FROM nodes WHERE id = $1 AND status = 'active'`, w.a1))
		})
		_, err = db.Owner.Exec(ctx, `UPDATE nodes SET status = 'active' WHERE id = $1`, w.root)
		require.NoError(t, err)
	})

	t.Run("insert user without RETURNING works, with RETURNING is rejected", func(t *testing.T) {
		id := uuid.Must(uuid.NewV7())
		inTx(ctx, t, db.App, w.adminA.String(), func(tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, 'fresh@example.test', 'h', now())`, id)
			require.NoError(t, err)
			assert.EqualValues(t, 1, tag.RowsAffected())
			var found *uuid.UUID
			require.NoError(t, tx.QueryRow(ctx, `SELECT tb_user_id_by_email('Fresh@Example.test')`).Scan(&found))
			require.NotNil(t, found)
			assert.Equal(t, id, *found)
		})
		inTx(ctx, t, db.App, w.adminA.String(), func(tx pgx.Tx) {
			_, err := tx.Exec(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, 'fresh2@example.test', 'h', now()) RETURNING id`, uuid.New())
			require.Error(t, err)
			assert.Equal(t, rlsDenied, sqlState(err))
		})
	})

	t.Run("authentication functions need no actor", func(t *testing.T) {
		inTx(ctx, t, db.App, "", func(tx pgx.Tx) {
			var id uuid.UUID
			var hash, status string
			var tv int64
			require.NoError(t, tx.QueryRow(ctx, `SELECT id, password_hash, status, token_version FROM tb_auth_user_by_email($1)`, w.emails[w.adminB]).Scan(&id, &hash, &status, &tv))
			assert.Equal(t, w.adminB, id)
			assert.Equal(t, "active", status)
			require.NoError(t, tx.QueryRow(ctx, `SELECT id, status, token_version FROM tb_auth_user_by_id($1)`, w.inactive).Scan(&id, &status, &tv))
			assert.Equal(t, "inactive", status)
			// One user per call, never a list; unknown input gives no row.
			assert.Zero(t, count(ctx, t, tx, `SELECT * FROM tb_auth_user_by_email('nobody@example.test')`))
			assert.Zero(t, count(ctx, t, tx, `SELECT * FROM tb_auth_user_by_email('%@example.test')`))
		})
	})
}

// Test-ID: T-ISO-13
func TestPoolDoesNotLeakActor(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	pool := db.NewAppPool(t, 1)

	setting := func(q querier) (val string) {
		require.NoError(t, q.QueryRow(ctx, `SELECT current_setting('app.actor_id', true)`).Scan(&val))
		return val
	}
	pid := func(q querier) (n int) {
		require.NoError(t, q.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&n))
		return n
	}
	visible := func(tx pgx.Tx) []uuid.UUID { return ids(ctx, t, tx, `SELECT id FROM nodes`) }

	var pidA int
	// Transaction of actor A, committed.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, w.adminA.String())
	require.NoError(t, err)
	pidA = pid(tx)
	assert.ElementsMatch(t, w.nodesA, visible(tx))
	require.NoError(t, tx.Commit(ctx))

	// Between transactions, on the same connection: no actor, no rows.
	assert.Equal(t, pidA, pid(pool), "the single connection must be reused")
	assert.Empty(t, setting(pool))
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n))
	assert.Zero(t, n)

	// Transaction of actor B on the same connection sees B only.
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, w.adminB.String())
	require.NoError(t, err)
	assert.Equal(t, pidA, pid(tx))
	assert.ElementsMatch(t, w.nodesB, visible(tx))
	require.NoError(t, tx.Rollback(ctx))

	// A transaction without an actor after a rolled-back one sees nothing.
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	assert.Empty(t, setting(tx))
	assert.Empty(t, visible(tx))
	require.NoError(t, tx.Rollback(ctx))

	// A failed statement inside an actor transaction, then rollback: nothing leaks either.
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, w.adminA.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT 1/0`)
	require.Error(t, err)
	require.NoError(t, tx.Rollback(ctx))
	assert.Equal(t, pidA, pid(pool))
	assert.Empty(t, setting(pool))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n))
	assert.Zero(t, n)
}

// Test-ID: T-ISO-15
func TestRolesAndPoliciesConfigured(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)

	t.Run("application role", func(t *testing.T) {
		var super, bypass, createDB, createRole, login bool
		require.NoError(t, db.Owner.QueryRow(ctx,
			`SELECT rolsuper, rolbypassrls, rolcreatedb, rolcreaterole, rolcanlogin FROM pg_roles WHERE rolname = 'tb_app'`).
			Scan(&super, &bypass, &createDB, &createRole, &login))
		assert.False(t, super)
		assert.False(t, bypass)
		assert.False(t, createDB)
		assert.False(t, createRole)
		assert.True(t, login)

		var member int
		require.NoError(t, db.Owner.QueryRow(ctx,
			`SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = 'tb_app'`).Scan(&member))
		assert.Zero(t, member, "tb_app must not be a member of another role")

		var owned []string
		rows, err := db.Owner.Query(ctx, `
SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_roles r ON r.oid = c.relowner
 WHERE n.nspname = 'public' AND r.rolname = 'tb_app'`)
		require.NoError(t, err)
		owned, err = pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		assert.Empty(t, owned, "tb_app must not own relations")

		var cur string
		var curBypass bool
		require.NoError(t, db.App.QueryRow(ctx,
			`SELECT current_user, (SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user)`).Scan(&cur, &curBypass))
		assert.Equal(t, "tb_app", cur)
		assert.False(t, curBypass)
	})

	t.Run("row level security and policies", func(t *testing.T) {
		want := map[string][]string{
			"nodes":            {"DELETE", "INSERT", "SELECT", "UPDATE"},
			"role_assignments": {"DELETE", "INSERT", "SELECT", "UPDATE"},
			"users":            {"INSERT", "SELECT", "UPDATE"}, // no DELETE grant, so no policy
		}
		for table, cmds := range want {
			var enabled, forced bool
			var owner string
			require.NoError(t, db.Owner.QueryRow(ctx,
				`SELECT c.relrowsecurity, c.relforcerowsecurity, r.rolname
				   FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner
				  WHERE c.oid = ('public.' || $1)::regclass`, table).Scan(&enabled, &forced, &owner))
			assert.True(t, enabled, table+": RLS enabled")
			assert.False(t, forced, table+": the owner is exempt by design (SECURITY DEFINER functions rely on it)")
			assert.Equal(t, "tb_owner", owner, table)

			rows, err := db.Owner.Query(ctx, `SELECT cmd FROM pg_policies WHERE schemaname = 'public' AND tablename = $1 ORDER BY cmd`, table)
			require.NoError(t, err)
			got, err := pgx.CollectRows(rows, pgx.RowTo[string])
			require.NoError(t, err)
			assert.Equal(t, cmds, got, table)
		}
		var priv bool
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT has_table_privilege('tb_app', 'public.users', 'DELETE')`).Scan(&priv))
		assert.False(t, priv, "tb_app must not delete users")
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT has_column_privilege('tb_app', 'public.users', 'password_hash', 'SELECT')`).Scan(&priv))
		assert.False(t, priv, "tb_app must not read password hashes")
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT has_column_privilege('tb_app', 'public.refresh_tokens', 'token_hash', 'UPDATE')`).Scan(&priv))
		assert.False(t, priv, "refresh token hashes are immutable for tb_app")
		var rls bool
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT relrowsecurity FROM pg_class WHERE oid = 'public.refresh_tokens'::regclass`).Scan(&rls))
		assert.False(t, rls, "refresh_tokens has no tenant data and no RLS by design")
	})

	t.Run("SECURITY DEFINER functions", func(t *testing.T) {
		rows, err := db.Owner.Query(ctx, `
SELECT p.proname, r.rolname, coalesce(p.proconfig, '{}'::text[]),
       has_function_privilege('tb_app', p.oid, 'EXECUTE'),
       EXISTS (SELECT 1 FROM aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a WHERE a.grantee = 0 AND a.privilege_type = 'EXECUTE')
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_roles r ON r.oid = p.proowner
 WHERE n.nspname = 'public' AND p.prosecdef
 ORDER BY p.proname`)
		require.NoError(t, err)
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name, owner string
			var config []string
			var appExec, publicExec bool
			require.NoError(t, rows.Scan(&name, &owner, &config, &appExec, &publicExec))
			names = append(names, name)
			assert.Equal(t, "tb_owner", owner, name)
			assert.Equal(t, []string{"search_path=pg_catalog, public, pg_temp"}, config, name+": fixed search_path, pg_temp last")
			assert.True(t, appExec, name+": tb_app can execute")
			assert.False(t, publicExec, name+": PUBLIC must not execute")
		}
		require.NoError(t, rows.Err())
		// A new SECURITY DEFINER function must be reviewed and added here on purpose.
		assert.Equal(t, []string{
			"tb_actor_scopes", "tb_auth_user_by_email", "tb_auth_user_by_id",
			"tb_node_effective_active", "tb_set_own_password", "tb_user_id_by_email",
		}, names)

		for _, f := range []string{"tb_actor_id()"} {
			var publicExec bool
			require.NoError(t, db.Owner.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a WHERE a.grantee = 0)
  FROM pg_proc p WHERE p.oid = ('public.' || $1)::regprocedure`, f).Scan(&publicExec))
			assert.False(t, publicExec, f)
		}
	})
}

func sortIDs(s []uuid.UUID) {
	sort.Slice(s, func(i, j int) bool { return s[i].String() < s[j].String() })
}
