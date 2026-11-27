//go:build integration

package isolation

import (
	"context"
	"crypto/sha256"
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
// inactive), Orphan (no role).
//
// Every branch also owns data of the M3 tables, so that a leak through any of
// them is visible: balances of every node (units: 1100 and 500), a credit on
// each of the two units and a transfer between them (the same shape in A and
// B), settings overrides (the root, A, A1, B, B1), idempotency keys (one key
// text, KeyShared, is used by AdminA and by AdminB for different operations;
// KeyOnlyA and KeyOnlyB by one admin each) and audit entries on A1, A1u1, B1,
// B1u1 and the root.
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

	// Ledger and audit data (written as tb_owner).
	OpsA, OpsB     []uuid.UUID // operation ids of the branch (credits and transfer legs)
	CreditA        uuid.UUID   // credit on A1u1, the operation behind AdminA's keys
	CreditB        uuid.UUID   // credit on B1u1, the operation behind AdminB's keys
	TransferA      uuid.UUID   // transfer A1u2 -> A1u1
	TransferB      uuid.UUID   // transfer B1u2 -> B1u1
	AuditA, AuditB []uuid.UUID // audit entries on nodes of the branch
	AuditRoot      uuid.UUID   // audit entry on the root (visible to neither branch admin)
	BalanceOf      map[uuid.UUID]int64
	SettingsRows   int
	KeyShared      string
	KeyOnlyA       string
	KeyOnlyB       string

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
	w.seedLedger(ctx, t)
	return w
}

// seedLedger writes balances, operations, settings, idempotency keys and audit
// entries into both branches, as tb_owner.
func (w *World) seedLedger(ctx context.Context, t *testing.T) {
	t.Helper()
	w.BalanceOf = map[uuid.UUID]int64{}
	for _, n := range w.AllNodes {
		w.AddBalance(ctx, t, n, 0)
	}
	w.BalanceOf[w.A1u1], w.BalanceOf[w.A1u2] = 1100, 500
	w.BalanceOf[w.B1u1], w.BalanceOf[w.B1u2] = 1100, 500
	for n, amount := range w.BalanceOf {
		_, err := w.db.Owner.Exec(ctx, `UPDATE balances SET amount = $2, version = 3 WHERE node_id = $1`, n, amount)
		require.NoError(t, err)
	}

	// Credit 1000 on the first unit, credit 600 on the second, then a transfer
	// of 100 from the second to the first: 1100 and 500, as the balances say.
	w.CreditA, w.TransferA, w.OpsA = w.seedOperations(ctx, t, w.AdminA, w.A1u1, w.A1u2)
	w.CreditB, w.TransferB, w.OpsB = w.seedOperations(ctx, t, w.AdminB, w.B1u1, w.B1u2)

	w.AddSetting(ctx, t, w.Root, "display_timezone", `"UTC"`)
	w.AddSetting(ctx, t, w.A, "operations_enabled", `true`)
	w.AddSetting(ctx, t, w.A1, "max_operation_amount", `"5000"`)
	w.AddSetting(ctx, t, w.B, "operations_enabled", `true`)
	w.AddSetting(ctx, t, w.B1, "max_operation_amount", `"7000"`)

	w.KeyShared, w.KeyOnlyA, w.KeyOnlyB = "shared-key", "only-a-key", "only-b-key"
	w.AddIdempotencyKey(ctx, t, w.AdminA, w.KeyShared, &w.CreditA, nil)
	w.AddIdempotencyKey(ctx, t, w.AdminA, w.KeyOnlyA, &w.CreditA, nil)
	w.AddIdempotencyKey(ctx, t, w.AdminB, w.KeyShared, &w.CreditB, nil)
	w.AddIdempotencyKey(ctx, t, w.AdminB, w.KeyOnlyB, nil, &w.TransferB)

	w.AuditRoot = w.AddAudit(ctx, t, w.OwnerRoot, w.Root, "node.created")
	w.AuditA = []uuid.UUID{
		w.AddAudit(ctx, t, w.AdminA, w.A1, "node.created"),
		w.AddAudit(ctx, t, w.AdminA, w.A1u1, "node.renamed"),
	}
	w.AuditB = []uuid.UUID{
		w.AddAudit(ctx, t, w.AdminB, w.B1, "node.created"),
		w.AddAudit(ctx, t, w.AdminB, w.B1u1, "node.renamed"),
	}
}

func (w *World) seedOperations(ctx context.Context, t *testing.T, actor, first, second uuid.UUID) (credit, transfer uuid.UUID, ops []uuid.UUID) {
	t.Helper()
	transfer = uuid.Must(uuid.NewV7())
	credit = w.AddOperation(ctx, t, actor, first, "credit", 1000, 1000, nil, nil)
	c2 := w.AddOperation(ctx, t, actor, second, "credit", 600, 600, nil, nil)
	out := w.AddOperation(ctx, t, actor, second, "transfer_out", 100, 500, &transfer, &first)
	in := w.AddOperation(ctx, t, actor, first, "transfer_in", 100, 1100, &transfer, &second)
	return credit, transfer, []uuid.UUID{credit, c2, out, in}
}

// AddBalance inserts the balance row of a node.
func (w *World) AddBalance(ctx context.Context, t *testing.T, node uuid.UUID, amount int64) {
	t.Helper()
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO balances (node_id, amount, version, updated_at) VALUES ($1, $2, 0, $3)`, node, amount, time.Now().UTC())
	require.NoError(t, err)
	w.BalanceOf[node] = amount
}

// AddOperation inserts a ledger row and returns its id.
func (w *World) AddOperation(ctx context.Context, t *testing.T, actor, node uuid.UUID, kind string, amount, after int64, transfer, counterparty *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, actor_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, node, kind, amount, after, transfer, counterparty, actor, time.Now().UTC())
	require.NoError(t, err)
	return id
}

// AddSetting inserts a settings override; value is JSON text.
func (w *World) AddSetting(ctx context.Context, t *testing.T, node uuid.UUID, key, value string) {
	t.Helper()
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, $2, $3::jsonb, $4, $5)`,
		node, key, value, time.Now().UTC(), w.OwnerRoot)
	require.NoError(t, err)
	w.SettingsRows++
}

// AddIdempotencyKey stores a key of a user for an operation or a transfer.
func (w *World) AddIdempotencyKey(ctx context.Context, t *testing.T, user uuid.UUID, key string, operation, transfer *uuid.UUID) {
	t.Helper()
	now := time.Now().UTC()
	hash := sha256.Sum256([]byte(user.String() + key))
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO idempotency_keys (user_id, key, request_hash, operation_id, transfer_id, created_at, expires_at)
		 VALUES ($1, $2, $7, $3, $4, $5, $6)`,
		user, key, operation, transfer, now, now.Add(24*time.Hour), hash[:])
	require.NoError(t, err)
}

// AddAudit appends an audit entry about a node and returns its id.
func (w *World) AddAudit(ctx context.Context, t *testing.T, actor, node uuid.UUID, action string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, before, after, request_id)
		 VALUES ($1, $2, $3, $4, $5, 'node', $4, NULL, '{"name":"x"}'::jsonb, 'req-seed')`,
		id, time.Now().UTC(), actor, node, action)
	require.NoError(t, err)
	return id
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
