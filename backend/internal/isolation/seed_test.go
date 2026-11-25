//go:build integration

package isolation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// rlsWorld is a minimal two-branch fixture written straight to the tables as
// tb_owner: root > partner A / partner B, each with one company and two units.
type rlsWorld struct {
	root               uuid.UUID
	a, a1, a1u1, a1u2  uuid.UUID
	b, b1, b1u1, b1u2  uuid.UUID
	adminA, viewerA1u1 uuid.UUID
	adminB, onlyB      uuid.UUID
	multi, inactive    uuid.UUID
	orphan, ownerRoot  uuid.UUID
	emails             map[uuid.UUID]string
	allNodes, allUsers []uuid.UUID
	nodesA, nodesB     []uuid.UUID
	pathOf             map[uuid.UUID]string
	nameOf             map[uuid.UUID]string
	roleRows           int
}

// querier is satisfied by both a pool and a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func label(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }

func newWorld(ctx context.Context, t *testing.T, db *testkit.DB) *rlsWorld {
	t.Helper()
	w := &rlsWorld{emails: map[uuid.UUID]string{}, pathOf: map[uuid.UUID]string{}, nameOf: map[uuid.UUID]string{}}
	now := time.Now().UTC()

	node := func(typ string, parent *uuid.UUID, name string) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		path := label(id)
		if parent != nil {
			path = w.pathOf[*parent] + "." + label(id)
		}
		_, err := db.Owner.Exec(ctx,
			`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
			 VALUES ($1, $2, $3, $4::ltree, $5, lower($5), $6, $6)`, id, typ, parent, path, name, now)
		require.NoError(t, err)
		w.pathOf[id] = path
		w.nameOf[id] = name
		w.allNodes = append(w.allNodes, id)
		return id
	}
	w.root = node("platform", nil, "root")
	w.a = node("partner", &w.root, "A")
	w.a1 = node("company", &w.a, "A1")
	w.a1u1 = node("unit", &w.a1, "A1u1")
	w.a1u2 = node("unit", &w.a1, "A1u2")
	w.b = node("partner", &w.root, "B")
	w.b1 = node("company", &w.b, "B1")
	w.b1u1 = node("unit", &w.b1, "B1u1")
	w.b1u2 = node("unit", &w.b1, "B1u2")
	w.nodesA = []uuid.UUID{w.a, w.a1, w.a1u1, w.a1u2}
	w.nodesB = []uuid.UUID{w.b, w.b1, w.b1u1, w.b1u2}

	user := func(name, status string) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		email := name + "@example.test"
		_, err := db.Owner.Exec(ctx,
			`INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, $2, $3, $4, $5)`,
			id, email, "hash-of-"+name, status, now)
		require.NoError(t, err)
		w.emails[id] = email
		w.allUsers = append(w.allUsers, id)
		return id
	}
	w.ownerRoot = user("owner-root", "active")
	w.adminA = user("admin-a", "active")
	w.viewerA1u1 = user("viewer-a1u1", "active")
	w.adminB = user("admin-b", "active")
	w.onlyB = user("only-b", "active")
	w.multi = user("multi", "active")
	w.inactive = user("inactive-a", "inactive")
	w.orphan = user("orphan", "active")

	grant := func(u, n uuid.UUID, role string) {
		_, err := db.Owner.Exec(ctx,
			`INSERT INTO role_assignments (user_id, node_id, role, granted_at) VALUES ($1, $2, $3, $4)`,
			u, n, role, now)
		require.NoError(t, err)
		w.roleRows++
	}
	grant(w.ownerRoot, w.root, "owner")
	grant(w.adminA, w.a, "admin")
	grant(w.viewerA1u1, w.a1u1, "viewer")
	grant(w.adminB, w.b, "admin")
	grant(w.onlyB, w.b1u2, "viewer")
	grant(w.multi, w.a1, "admin")
	grant(w.multi, w.b1, "viewer")
	grant(w.inactive, w.a, "admin")
	return w
}

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

// snapshot is a digest of every row of the tables under test, read as tb_owner
// (exempt from RLS). Equal digests before and after a statement prove that
// nothing changed.
func snapshot(ctx context.Context, t *testing.T, db *testkit.DB) string {
	t.Helper()
	var s string
	err := db.Owner.QueryRow(ctx, `
SELECT (SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM nodes x) || '|' ||
       (SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM users x) || '|' ||
       (SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM role_assignments x) || '|' ||
       (SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM refresh_tokens x)`).Scan(&s)
	require.NoError(t, err)
	return s
}
