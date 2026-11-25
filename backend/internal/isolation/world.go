//go:build integration

package isolation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// World is the shared fixture of the isolation tests, written straight to the
// tables as tb_owner (so it does not depend on the code under test):
//
//	Root (platform) > A (partner) > A1 (company) > A1u1, A1u2 (unit)
//	                > B (partner) > B1 (company) > B1u1, B1u2 (unit)
//
// Users: OwnerRoot (owner of the root), AdminA (admin of A), AnalystA (analyst
// of A1), ViewerA1u1 (viewer of A1u1), AdminB (admin of B), OnlyB (viewer of
// B1u2), Multi (admin of A1 and viewer of B1), Inactive (admin of A, status
// inactive), Orphan (no role). Operations and audit entries of both branches
// join it with the ledger and audit tables (M3).
type World struct {
	Root                    uuid.UUID
	A, A1, A1u1, A1u2       uuid.UUID
	B, B1, B1u1, B1u2       uuid.UUID
	OwnerRoot, AdminA       uuid.UUID
	AnalystA, ViewerA1u1    uuid.UUID
	AdminB, OnlyB           uuid.UUID
	Multi, Inactive, Orphan uuid.UUID

	Emails             map[uuid.UUID]string
	AllNodes, AllUsers []uuid.UUID
	NodesA, NodesB     []uuid.UUID
	PathOf             map[uuid.UUID]string
	NameOf             map[uuid.UUID]string
	RoleRows           int

	db *testkit.DB
}

// Label turns an id into an ltree label.
func Label(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }

// NewWorld builds the fixture in db.
func NewWorld(ctx context.Context, t *testing.T, db *testkit.DB) *World {
	t.Helper()
	w := &World{
		Emails: map[uuid.UUID]string{}, PathOf: map[uuid.UUID]string{}, NameOf: map[uuid.UUID]string{}, db: db,
	}
	w.Root = w.AddNode(ctx, t, "platform", nil, "root")
	w.A = w.AddNode(ctx, t, "partner", &w.Root, "A")
	w.A1 = w.AddNode(ctx, t, "company", &w.A, "A1")
	w.A1u1 = w.AddNode(ctx, t, "unit", &w.A1, "A1u1")
	w.A1u2 = w.AddNode(ctx, t, "unit", &w.A1, "A1u2")
	w.B = w.AddNode(ctx, t, "partner", &w.Root, "B")
	w.B1 = w.AddNode(ctx, t, "company", &w.B, "B1")
	w.B1u1 = w.AddNode(ctx, t, "unit", &w.B1, "B1u1")
	w.B1u2 = w.AddNode(ctx, t, "unit", &w.B1, "B1u2")
	w.NodesA = []uuid.UUID{w.A, w.A1, w.A1u1, w.A1u2}
	w.NodesB = []uuid.UUID{w.B, w.B1, w.B1u1, w.B1u2}

	w.OwnerRoot = w.addUser(ctx, t, "owner-root", "active")
	w.AdminA = w.addUser(ctx, t, "admin-a", "active")
	w.AnalystA = w.addUser(ctx, t, "analyst-a1", "active")
	w.ViewerA1u1 = w.addUser(ctx, t, "viewer-a1u1", "active")
	w.AdminB = w.addUser(ctx, t, "admin-b", "active")
	w.OnlyB = w.addUser(ctx, t, "only-b", "active")
	w.Multi = w.addUser(ctx, t, "multi", "active")
	w.Inactive = w.addUser(ctx, t, "inactive-a", "inactive")
	w.Orphan = w.addUser(ctx, t, "orphan", "active")

	w.Grant(ctx, t, w.OwnerRoot, w.Root, "owner")
	w.Grant(ctx, t, w.AdminA, w.A, "admin")
	w.Grant(ctx, t, w.AnalystA, w.A1, "analyst")
	w.Grant(ctx, t, w.ViewerA1u1, w.A1u1, "viewer")
	w.Grant(ctx, t, w.AdminB, w.B, "admin")
	w.Grant(ctx, t, w.OnlyB, w.B1u2, "viewer")
	w.Grant(ctx, t, w.Multi, w.A1, "admin")
	w.Grant(ctx, t, w.Multi, w.B1, "viewer")
	w.Grant(ctx, t, w.Inactive, w.A, "admin")
	return w
}

// AddNode inserts a node under parent (nil for a root) and returns its id.
func (w *World) AddNode(ctx context.Context, t *testing.T, typ string, parent *uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	path := Label(id)
	if parent != nil {
		path = w.PathOf[*parent] + "." + Label(id)
	}
	now := time.Now().UTC()
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
		 VALUES ($1, $2, $3, $4::ltree, $5, lower($5), $6, $6)`, id, typ, parent, path, name, now)
	require.NoError(t, err)
	w.PathOf[id] = path
	w.NameOf[id] = name
	w.AllNodes = append(w.AllNodes, id)
	return id
}

func (w *World) addUser(ctx context.Context, t *testing.T, name, status string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	email := name + "@example.test"
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, email, "hash-of-"+name, status, time.Now().UTC())
	require.NoError(t, err)
	w.Emails[id] = email
	w.AllUsers = append(w.AllUsers, id)
	return id
}

// Grant stores a role assignment.
func (w *World) Grant(ctx context.Context, t *testing.T, u, n uuid.UUID, role string) {
	t.Helper()
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO role_assignments (user_id, node_id, role, granted_at) VALUES ($1, $2, $3, $4)`,
		u, n, role, time.Now().UTC())
	require.NoError(t, err)
	w.RoleRows++
}

// Snapshot is a digest of every row of every table of the public schema (except
// the migration bookkeeping), read as tb_owner (exempt from RLS). Equal digests
// before and after an operation prove that nothing changed. New tables are
// included without changes here.
func Snapshot(ctx context.Context, t *testing.T, db *testkit.DB) string {
	t.Helper()
	rows, err := db.Owner.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version' ORDER BY tablename`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.NotEmpty(t, tables)

	var b strings.Builder
	for _, name := range tables {
		var s string
		require.NoError(t, db.Owner.QueryRow(ctx,
			`SELECT coalesce(string_agg(x::text, ',' ORDER BY x::text), '') FROM public.`+quoteIdent(name)+` x`).Scan(&s))
		b.WriteString(name + "=" + s + "|")
	}
	return b.String()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
