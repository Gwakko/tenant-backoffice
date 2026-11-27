//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	ua "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/audit"
	um "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	un "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
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

// argonHasher produces hashes that look like real argon2id output, so a leak
// into the journal is recognisable by the usual markers.
type argonHasher struct{}

func (argonHasher) Hash(p string) (user.PasswordHash, error) {
	return user.PasswordHash("$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$" + p + "hashbytes"), nil
}

func (argonHasher) Verify(user.PasswordHash, string) (bool, error) { return false, nil }

// auditCases is the node and member use cases and UC-AU1 wired to the real
// adapters, with the real AuditWriter (not a recorder).
type auditCases struct {
	node   *un.Deps
	create *un.CreateNode
	rename *un.RenameNode
	move   *un.MoveNode
	del    *un.DeleteNode
	deact  *un.DeactivateNode
	act    *un.ActivateNode
	add    *um.AddMember
	change *um.ChangeMemberRole
	remove *um.RemoveMember
	list   *ua.ListAudit
}

func (w *world) auditCases() *auditCases {
	log := w.runnerLog()
	aw := postgres.NewAuditWriter(log, realClock{}, v7IDs{})
	nd := un.Deps{Runner: w.runner, Checker: w.checker, Nodes: w.repo, Lock: w.lock, Audit: aw, Clock: realClock{}, IDs: v7IDs{}}
	md := um.Deps{
		Runner: w.runner, Checker: w.checker, Nodes: w.repo, Members: postgres.NewMemberRepo(log),
		Users: postgres.NewUserRepo(log), Hasher: argonHasher{}, Audit: aw, Clock: realClock{}, IDs: v7IDs{},
	}
	return &auditCases{
		node: &nd, create: un.NewCreateNode(nd), rename: un.NewRenameNode(nd), move: un.NewMoveNode(nd),
		del: un.NewDeleteNode(nd), deact: un.NewDeactivateNode(nd), act: un.NewActivateNode(nd),
		add: um.NewAddMember(md), change: um.NewChangeMemberRole(md), remove: um.NewRemoveMember(md),
		list: ua.NewListAudit(ua.Deps{Runner: w.runner, Checker: w.checker, Nodes: w.repo, Audit: postgres.NewAuditRepo(log)}),
	}
}

type auditRow struct {
	Action, TargetType, TargetID, RequestID string
	NodeID, ActorID                         uuid.UUID
	Before, After                           *string
	Text                                    string
}

// auditRows reads the whole journal as the owner, oldest first.
func (w *world) auditRows() []auditRow {
	w.t.Helper()
	rows, err := w.db.Owner.Query(context.Background(),
		`SELECT action, target_type, target_id::text, request_id, node_id, actor_id,
		        before::text, after::text, a::text
		   FROM audit_log a ORDER BY occurred_at, id`)
	require.NoError(w.t, err)
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		require.NoError(w.t, rows.Scan(&r.Action, &r.TargetType, &r.TargetID, &r.RequestID, &r.NodeID, &r.ActorID, &r.Before, &r.After, &r.Text))
		out = append(out, r)
	}
	require.NoError(w.t, rows.Err())
	return out
}

func (w *world) auditActions() []string {
	var out []string
	for _, r := range w.auditRows() {
		out = append(out, r.Action)
	}
	return out
}

func (w *world) auditCount() int { return len(w.auditRows()) }

// Test-ID: T-I-37
//
// EC-96: the entry lives and dies with the change. The runner fails the
// commit after the use case succeeded (so after the entry was written): the
// rename and its entry are both gone. Without the failure both are there.
func TestAuditRolledBackWithChange(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	c := w.auditCases()
	actor := w.users["adminA"]

	nameOf := func() string {
		var n string
		require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT name FROM nodes WHERE id = $1`, uuid.UUID(w.id("A1"))).Scan(&n))
		return n
	}

	boom := errors.New("commit refused")
	w.runner.BeforeCommit(func(context.Context, pgx.Tx) error { return boom })
	_, err := c.rename.Execute(ctx, un.RenameNodeInput{Actor: actor, RequestID: "r-1", NodeID: w.id("A1"), Name: "Renamed"})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, "A1", nameOf(), "the change must be rolled back")
	assert.Zero(t, w.auditCount(), "the entry must be rolled back with it")

	// The same for a change that writes several entries (a move writes three).
	_, err = c.move.Execute(ctx, un.MoveNodeInput{Actor: w.users["ownerRoot"], RequestID: "r-2", NodeID: w.id("A1u1"), NewParentID: w.id("B1")})
	require.ErrorIs(t, err, boom)
	assert.Zero(t, w.auditCount())

	// And for a use case that fails after its entry: a transaction function
	// that writes an entry and then returns an error leaves nothing.
	aw := postgres.NewAuditWriter(w.runnerLog(), realClock{}, v7IDs{})
	w.runner.BeforeCommit(nil)
	werr := w.try("adminA", func(ctx context.Context, tx port.Tx) error {
		require.NoError(t, aw.Write(ctx, tx, port.AuditEntry{
			Action: "node.renamed", NodeID: w.id("A1"), TargetID: uuid.UUID(w.id("A1")), ActorID: actor, RequestID: "r-3",
			Before: map[string]string{"name": "A1"}, After: map[string]string{"name": "X"},
		}))
		return boom
	})
	require.ErrorIs(t, werr, boom)
	assert.Zero(t, w.auditCount())

	// Control: without the failure the change and the entry are both there.
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: actor, RequestID: "r-4", NodeID: w.id("A1"), Name: "Renamed"})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", nameOf())
	rows := w.auditRows()
	require.Len(t, rows, 1)
	assert.Equal(t, "node.renamed", rows[0].Action)
	assert.Equal(t, "node", rows[0].TargetType)
	assert.Equal(t, uuid.UUID(w.id("A1")).String(), rows[0].TargetID)
	assert.Equal(t, uuid.UUID(w.id("A1")), rows[0].NodeID)
	assert.Equal(t, uuid.UUID(actor), rows[0].ActorID)
	assert.Equal(t, "r-4", rows[0].RequestID)
	assert.JSONEq(t, `{"name":"A1"}`, *rows[0].Before)
	assert.JSONEq(t, `{"name":"Renamed"}`, *rows[0].After)
}

// Test-ID: T-I-39
//
// EC-98: a move writes three entries; the old parent's does not carry the new
// place, the new parent's does not carry the old one; each branch sees only
// its own.
func TestMoveWritesThreeEntries(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	c := w.auditCases()
	moved, oldParent, newParent := w.id("A1u1"), w.id("A1"), w.id("B1")

	_, err := c.move.Execute(ctx, un.MoveNodeInput{Actor: w.users["ownerRoot"], RequestID: "mv-1", NodeID: moved, NewParentID: newParent})
	require.NoError(t, err)

	byAction := map[string]auditRow{}
	rows := w.auditRows()
	require.Len(t, rows, 3)
	for _, r := range rows {
		byAction[r.Action] = r
		assert.Equal(t, "mv-1", r.RequestID)
		assert.Equal(t, uuid.UUID(w.users["ownerRoot"]), r.ActorID)
		assert.Equal(t, uuid.UUID(moved).String(), r.TargetID)
	}
	mv, out, in := byAction["node.moved"], byAction["node.child_moved_out"], byAction["node.child_moved_in"]
	require.NotEmpty(t, mv.Action)
	require.NotEmpty(t, out.Action)
	require.NotEmpty(t, in.Action)

	assert.Equal(t, uuid.UUID(moved), mv.NodeID)
	assert.JSONEq(t, `{"parent_id":"`+uuid.UUID(oldParent).String()+`"}`, *mv.Before)
	assert.JSONEq(t, `{"parent_id":"`+uuid.UUID(newParent).String()+`"}`, *mv.After)

	assert.Equal(t, uuid.UUID(oldParent), out.NodeID)
	assert.JSONEq(t, `{"node_id":"`+uuid.UUID(moved).String()+`","name":"A1u1"}`, *out.Before)
	assert.Nil(t, out.After)
	assert.NotContains(t, out.Text, uuid.UUID(newParent).String(), "the old branch must not learn the new parent")
	assert.NotContains(t, out.Text, uuid.UUID(w.id("B")).String())

	assert.Equal(t, uuid.UUID(newParent), in.NodeID)
	assert.JSONEq(t, `{"node_id":"`+uuid.UUID(moved).String()+`","name":"A1u1"}`, *in.After)
	assert.Nil(t, in.Before)
	assert.NotContains(t, in.Text, uuid.UUID(oldParent).String(), "the new branch must not learn the old parent")

	list := func(who, scope string) []string {
		out, err := c.list.Execute(ctx, ua.ListAuditInput{Actor: w.users[who], ScopeNodeID: w.id(scope), Limit: 50})
		require.NoError(t, err, who)
		var actions []string
		for _, it := range out.Items {
			actions = append(actions, string(it.Entry.Action()))
		}
		return actions
	}
	assert.Equal(t, []string{"node.child_moved_out"}, list("adminA", "A"), "branch A sees only the departure")
	assert.ElementsMatch(t, []string{"node.moved", "node.child_moved_in"}, list("adminB", "B"), "branch B sees the arrival and the node")
	assert.ElementsMatch(t, []string{"node.moved", "node.child_moved_out", "node.child_moved_in"}, list("ownerRoot", "root"))

	// A move that changes nothing writes nothing (EC-101).
	_, err = c.move.Execute(ctx, un.MoveNodeInput{Actor: w.users["ownerRoot"], RequestID: "mv-2", NodeID: moved, NewParentID: newParent})
	require.NoError(t, err)
	assert.Equal(t, 3, w.auditCount())
}

// Test-ID: T-I-40
//
// EC-100: neither the password nor its hash reach the journal. The check is
// by content (argon2 marker, the dollar signs of a hash, the password itself,
// the words), not by the shape of the entry; and the writer itself refuses a
// secret-looking key.
func TestNoSecretsInAudit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	c := w.auditCases()
	const password = "Sup3r-Secret-pw!"
	admin, owner := w.users["adminA"], w.users["ownerRoot"]

	out, err := c.add.Execute(ctx, um.AddMemberInput{
		Actor: admin, RequestID: "m-1", NodeID: w.id("A1"), Email: "fresh@example.test", Role: "viewer", InitialPassword: password,
	})
	require.NoError(t, err)
	_, err = c.add.Execute(ctx, um.AddMemberInput{ // existing user, password ignored
		Actor: admin, RequestID: "m-2", NodeID: w.id("A1u2"), Email: "fresh@example.test", Role: "viewer", InitialPassword: password,
	})
	require.NoError(t, err)
	_, err = c.change.Execute(ctx, um.ChangeMemberRoleInput{Actor: admin, RequestID: "m-3", NodeID: w.id("A1"), UserID: out.Member.UserID, Role: "analyst"})
	require.NoError(t, err)
	require.NoError(t, c.remove.Execute(ctx, um.RemoveMemberInput{Actor: admin, RequestID: "m-4", NodeID: w.id("A1"), UserID: out.Member.UserID}))
	_, err = c.create.Execute(ctx, un.CreateNodeInput{Actor: owner, RequestID: "m-5", ParentID: w.id("A"), Type: "company", Name: "Fresh"})
	require.NoError(t, err)

	rows := w.auditRows()
	require.Equal(t, []string{"user.created", "member.granted", "member.granted", "member.changed", "member.revoked", "node.created"},
		w.auditActions())
	for _, r := range rows {
		lower := strings.ToLower(r.Text)
		for _, bad := range []string{"argon2", "$", password, strings.ToLower(password), "password", "hash", "token", "secret"} {
			assert.NotContains(t, lower, strings.ToLower(bad), "%s entry contains %q: %s", r.Action, bad, r.Text)
		}
	}

	// The writer refuses secret keys outright, whatever the value.
	aw := postgres.NewAuditWriter(w.runnerLog(), realClock{}, v7IDs{})
	for _, key := range []string{"password", "password_hash", "token", "refresh_token", "secret"} {
		err := w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			return aw.Write(ctx, tx, port.AuditEntry{
				Action: "member.granted", NodeID: w.id("A1"), TargetID: uuid.New(), ActorID: admin, RequestID: "x",
				After: map[string]string{key: "v"},
			})
		})
		requireApp(t, err, apperr.ValidationFailed)
	}
	assert.Equal(t, len(rows), w.auditCount(), "a refused entry leaves nothing")
}

// Test-ID: T-I-80
// Every changing use case of nodes and members writes its entry with the
// right action, node, target and snapshots; refusals and no-ops write none
// (EC-99, EC-101).
func TestAuditWrittenByEveryChange(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	c := w.auditCases()
	owner, admin := w.users["ownerRoot"], w.users["adminA"]
	last := func() auditRow { r := w.auditRows(); return r[len(r)-1] }

	created, err := c.create.Execute(ctx, un.CreateNodeInput{Actor: admin, RequestID: "q", ParentID: w.id("A"), Type: "company", Name: "Fresh"})
	require.NoError(t, err)
	fresh := created.Node.Node.ID
	r := last()
	assert.Equal(t, "node.created", r.Action)
	assert.Equal(t, uuid.UUID(fresh), r.NodeID)
	assert.Nil(t, r.Before)
	assert.JSONEq(t, `{"type":"company","name":"Fresh","parent_id":"`+uuid.UUID(w.id("A")).String()+`"}`, *r.After)

	_, err = c.deact.Execute(ctx, un.SetStatusInput{Actor: admin, RequestID: "q", NodeID: fresh})
	require.NoError(t, err)
	assert.Equal(t, "node.deactivated", last().Action)
	assert.JSONEq(t, `{"status":"active"}`, *last().Before)
	assert.JSONEq(t, `{"status":"inactive"}`, *last().After)
	_, err = c.act.Execute(ctx, un.SetStatusInput{Actor: admin, RequestID: "q", NodeID: fresh})
	require.NoError(t, err)
	assert.Equal(t, "node.activated", last().Action)

	// Refusals and no-ops write nothing.
	n := w.auditCount()
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: w.users["viewerA1u1"], RequestID: "q", NodeID: w.id("A1u1"), Name: "Nope"})
	requireApp(t, err, apperr.Forbidden)
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: w.users["adminB"], RequestID: "q", NodeID: fresh, Name: "Nope"})
	requireApp(t, err, apperr.NotFound)
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: admin, RequestID: "q", NodeID: fresh, Name: "A1"})
	requireApp(t, err, apperr.NameTaken)
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: admin, RequestID: "q", NodeID: fresh, Name: " "})
	requireApp(t, err, apperr.ValidationFailed)
	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: admin, RequestID: "q", NodeID: fresh, Name: "Fresh"})
	require.NoError(t, err)
	_, err = c.act.Execute(ctx, un.SetStatusInput{Actor: admin, RequestID: "q", NodeID: fresh})
	require.NoError(t, err)
	assert.Equal(t, n, w.auditCount())

	_, err = c.rename.Execute(ctx, un.RenameNodeInput{Actor: admin, RequestID: "q", NodeID: fresh, Name: "Fresh 2"})
	require.NoError(t, err)
	assert.Equal(t, "node.renamed", last().Action)

	// Delete: the entry is on the parent and survives the node.
	_, err = c.del.Execute(ctx, un.DeleteNodeInput{Actor: owner, RequestID: "q", NodeID: fresh})
	require.NoError(t, err)
	r = last()
	assert.Equal(t, "node.deleted", r.Action)
	assert.Equal(t, uuid.UUID(w.id("A")), r.NodeID)
	assert.Equal(t, uuid.UUID(fresh).String(), r.TargetID)
	assert.JSONEq(t, `{"node_id":"`+uuid.UUID(fresh).String()+`","name":"Fresh 2","type":"company"}`, *r.Before)
	list, err := c.list.Execute(ctx, ua.ListAuditInput{Actor: admin, ScopeNodeID: w.id("A"), Limit: 50,
		Action: ptr("node.deleted")})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)

	// Members: grant to a new user, change, revoke.
	added, err := c.add.Execute(ctx, um.AddMemberInput{Actor: admin, RequestID: "q", NodeID: w.id("A1"), Email: "new@example.test", Role: "viewer", InitialPassword: "pw-123456789"})
	require.NoError(t, err)
	uid := added.Member.UserID
	rows := w.auditRows()
	require.GreaterOrEqual(t, len(rows), 2)
	uc, gr := rows[len(rows)-2], rows[len(rows)-1]
	assert.Equal(t, "user.created", uc.Action)
	assert.Equal(t, "user", uc.TargetType)
	assert.Equal(t, uuid.UUID(uid).String(), uc.TargetID)
	assert.JSONEq(t, `{"email":"new@example.test"}`, *uc.After)
	assert.Equal(t, "member.granted", gr.Action)
	assert.Equal(t, "member", gr.TargetType)
	assert.JSONEq(t, `{"user_id":"`+uuid.UUID(uid).String()+`","role":"viewer"}`, *gr.After)

	_, err = c.change.Execute(ctx, um.ChangeMemberRoleInput{Actor: admin, RequestID: "q", NodeID: w.id("A1"), UserID: uid, Role: "analyst"})
	require.NoError(t, err)
	assert.Equal(t, "member.changed", last().Action)
	assert.JSONEq(t, `{"role":"viewer"}`, *last().Before)
	n = w.auditCount()
	_, err = c.change.Execute(ctx, um.ChangeMemberRoleInput{Actor: admin, RequestID: "q", NodeID: w.id("A1"), UserID: uid, Role: "analyst"})
	require.NoError(t, err)
	assert.Equal(t, n, w.auditCount(), "the same role again is a no-op")

	require.NoError(t, c.remove.Execute(ctx, um.RemoveMemberInput{Actor: admin, RequestID: "q", NodeID: w.id("A1"), UserID: uid}))
	assert.Equal(t, "member.revoked", last().Action)

	w.grant(ctx, "orphan", "A1", "owner")
	// An actor removing their own last assignment: the entry is written before
	// the delete, while the node is still visible to them.
	require.NoError(t, c.remove.Execute(ctx, um.RemoveMemberInput{Actor: w.users["orphan"], RequestID: "self", NodeID: w.id("A1"), UserID: w.users["orphan"]}), "self removal")
	r = last()
	assert.Equal(t, "member.revoked", r.Action)
	assert.Equal(t, "self", r.RequestID)
}

func ptr[T any](v T) *T { return &v }

// Test-ID: T-I-81
// UC-AU1 on the real database: newest first, equal times keep a stable order
// across pages, filters, the subtree scope, visibility and the right.
func TestListAuditReal(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	c := w.auditCases()
	owner, adminA := uuid.UUID(w.users["ownerRoot"]), uuid.UUID(w.users["adminA"])

	// Seed 7 entries on A1 with the same timestamp, one on A and one on B1.
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	ins := func(nodeKey string, actor uuid.UUID, action string, when time.Time) {
		_, err := w.db.Owner.Exec(ctx,
			`INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, after, request_id)
			 VALUES ($1, $2, $3, $4, $5, 'node', $4, '{"name":"x"}'::jsonb, 'seed')`,
			uuid.Must(uuid.NewV7()), when, actor, uuid.UUID(w.id(nodeKey)), action)
		require.NoError(t, err)
	}
	for range 7 {
		ins("A1", adminA, "node.renamed", at)
	}
	ins("A", owner, "node.created", at.Add(time.Minute))
	ins("B1", owner, "node.created", at.Add(2*time.Minute))

	page := func(who string, mod func(*ua.ListAuditInput)) ua.ListAuditOutput {
		in := ua.ListAuditInput{Actor: w.users[who], ScopeNodeID: w.id("root"), Limit: 3}
		if mod != nil {
			mod(&in)
		}
		out, err := c.list.Execute(ctx, in)
		require.NoError(t, err)
		return out
	}

	// Paging through all nine: every id once, order (time, id) descending.
	var ids []string
	var times []time.Time
	cursor := ""
	for {
		out := page("ownerRoot", func(in *ua.ListAuditInput) { in.Cursor = cursor })
		for _, it := range out.Items {
			ids = append(ids, uuid.UUID(it.Entry.ID()).String())
			times = append(times, it.Entry.OccurredAt())
		}
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	require.Len(t, ids, 9)
	seen := map[string]bool{}
	for i, id := range ids {
		assert.False(t, seen[id], "repeated across pages")
		seen[id] = true
		if i > 0 {
			assert.True(t, times[i-1].After(times[i]) || (times[i-1].Equal(times[i]) && ids[i-1] > id), "order at %d", i)
		}
	}
	assert.Equal(t, audit.NodeCreated, page("ownerRoot", nil).Items[0].Entry.Action())

	// Display data and filters.
	one := page("ownerRoot", func(in *ua.ListAuditInput) { in.Limit = 1; in.NodeID = ptr(w.id("B1")) })
	require.Len(t, one.Items, 1)
	assert.Equal(t, "B1", one.Items[0].NodeName)
	assert.Equal(t, "ownerroot@example.test", one.Items[0].ActorEmail)
	assert.Len(t, page("ownerRoot", func(in *ua.ListAuditInput) { in.Limit = 50; in.Action = ptr("node.created") }).Items, 2)
	actor := user.ID(w.users["adminA"])
	assert.Len(t, page("ownerRoot", func(in *ua.ListAuditInput) { in.Limit = 50; in.ActorID = &actor }).Items, 7)
	from, to := at.Add(30*time.Second), at.Add(2*time.Minute)
	got := page("ownerRoot", func(in *ua.ListAuditInput) { in.Limit = 50; in.From, in.To = &from, &to })
	require.Len(t, got.Items, 1, "from is inclusive, to exclusive")
	assert.Equal(t, "A", got.Items[0].NodeName)

	// Scope: a subtree only; the right on the scope node; branches stay apart.
	assert.Len(t, page("adminA", func(in *ua.ListAuditInput) { in.ScopeNodeID = w.id("A"); in.Limit = 50 }).Items, 8)
	assert.Len(t, page("adminA", func(in *ua.ListAuditInput) { in.ScopeNodeID = w.id("A1"); in.Limit = 50 }).Items, 7)
	_, outside := c.list.Execute(ctx, ua.ListAuditInput{
		Actor: w.users["adminA"], ScopeNodeID: w.id("A"), NodeID: ptr(w.id("B1")), Limit: 50,
	})
	require.Error(t, outside, "a node outside the scope is not found (EC-52)")
	code, _ := apperr.CodeOf(outside)
	assert.Equal(t, apperr.NotFound, code)
	_, err := c.list.Execute(ctx, ua.ListAuditInput{Actor: w.users["adminA"], ScopeNodeID: w.id("B"), Limit: 5})
	requireApp(t, err, apperr.NotFound)
	_, err = c.list.Execute(ctx, ua.ListAuditInput{Actor: w.users["adminA"], ScopeNodeID: w.id("root"), Limit: 5})
	requireApp(t, err, apperr.NotFound)
	_, err = c.list.Execute(ctx, ua.ListAuditInput{Actor: w.users["viewerA1u1"], ScopeNodeID: w.id("A1u1"), Limit: 5})
	requireApp(t, err, apperr.Forbidden)
	_, err = c.list.Execute(ctx, ua.ListAuditInput{Actor: w.users["orphan"], ScopeNodeID: w.id("A1"), Limit: 5})
	requireApp(t, err, apperr.NotFound)
	// The actor of an entry the reader cannot see as a user has no e-mail shown.
	b := page("adminB", func(in *ua.ListAuditInput) { in.ScopeNodeID = w.id("B"); in.Limit = 5 })
	require.Len(t, b.Items, 1)
	assert.Equal(t, "", b.Items[0].ActorEmail)
}
