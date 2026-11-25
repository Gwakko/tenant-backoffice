//go:build integration

package isolation_test

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	nodeuc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// recAudit records audit entries until the audit table exists (M3.3).
type recAudit struct {
	mu      sync.Mutex
	entries []port.AuditEntry
}

func (a *recAudit) Write(_ context.Context, _ port.Tx, e port.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recAudit) all() []port.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]port.AuditEntry(nil), a.entries...)
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

type v7IDs struct{}

func (v7IDs) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// testHasher stands in for argon2id: these tests care about who may create and
// see what, not about how a hash is made.
type testHasher struct{}

func (testHasher) Hash(p string) (user.PasswordHash, error) {
	return user.PasswordHash("test-hash:" + p), nil
}

func (testHasher) Verify(h user.PasswordHash, p string) (bool, error) {
	return string(h) == "test-hash:"+p, nil
}

// app is the node and member use cases wired to the real adapters (TxRunner,
// Checker, repositories) on one pool.
type app struct {
	audit  *recAudit
	logs   *logSink
	pool   *pgxpool.Pool
	runner *postgres.TxRunner
	nodes  *postgres.NodeRepo
	member *postgres.MemberRepo
	check  *postgres.Checker

	create   *nodeuc.CreateNode
	rename   *nodeuc.RenameNode
	move     *nodeuc.MoveNode
	del      *nodeuc.DeleteNode
	get      *nodeuc.GetNode
	list     *nodeuc.ListChildren
	deact    *nodeuc.DeactivateNode
	act      *nodeuc.ActivateNode
	roots    *nodeuc.ListAccessRoots
	targets  *nodeuc.ListMoveTargets
	perms    *nodeuc.GetPermissions
	add      *member.AddMember
	change   *member.ChangeMemberRole
	remove   *member.RemoveMember
	listMems *member.ListMembers
}

func newApp(pool *pgxpool.Pool) *app {
	logs := &logSink{}
	log := slogTo(logs)
	a := &app{
		audit: &recAudit{}, logs: logs, pool: pool,
		runner: postgres.NewTxRunner(pool, log), nodes: postgres.NewNodeRepo(log),
		member: postgres.NewMemberRepo(log), check: postgres.NewChecker(log),
	}
	nd := nodeuc.Deps{
		Runner: a.runner, Checker: a.check, Nodes: a.nodes, Lock: postgres.NewTreeLock(),
		Audit: a.audit, Clock: fixedClock{}, IDs: v7IDs{},
	}
	a.create, a.rename, a.move, a.del = nodeuc.NewCreateNode(nd), nodeuc.NewRenameNode(nd), nodeuc.NewMoveNode(nd), nodeuc.NewDeleteNode(nd)
	a.get, a.list, a.deact, a.act = nodeuc.NewGetNode(nd), nodeuc.NewListChildren(nd), nodeuc.NewDeactivateNode(nd), nodeuc.NewActivateNode(nd)
	a.roots, a.targets, a.perms = nodeuc.NewListAccessRoots(nd), nodeuc.NewListMoveTargets(nd), nodeuc.NewGetPermissions(nd)
	md := member.Deps{
		Runner: a.runner, Checker: a.check, Nodes: a.nodes, Members: a.member, Users: postgres.NewUserRepo(log),
		Hasher: testHasher{}, Audit: a.audit, Clock: fixedClock{}, IDs: v7IDs{},
	}
	a.add, a.change, a.remove, a.listMems = member.NewAddMember(md), member.NewChangeMemberRole(md), member.NewRemoveMember(md), member.NewListMembers(md)
	return a
}

func actorOf(id uuid.UUID) user.ActorID { return user.ActorID(id) }

// op is one use case call against a node id; it returns the output (to be
// empty on failure) and the error.
type op struct {
	name string
	run  func(ctx context.Context, a *app, actor, target uuid.UUID) (any, error)
}

// readOps are the node and member reads addressed by a node id.
func readOps() []op {
	return []op{
		{"get", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(actor), NodeID: nid(id)})
		}},
		{"permissions", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.perms.Execute(ctx, nodeuc.GetPermissionsInput{Actor: actorOf(actor), NodeID: nid(id)})
		}},
		{"children", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(actor), NodeID: nid(id), Limit: 50})
		}},
		{"children with query", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(actor), NodeID: nid(id), Limit: 50, Query: "b", Status: "active"})
		}},
		{"move targets", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(actor), NodeID: nid(id), Limit: 50})
		}},
		{"members", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(actor), NodeID: nid(id), Limit: 50})
		}},
		{"members inherited", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(actor), NodeID: nid(id), Limit: 50, Inherited: true})
		}},
	}
}

// writeOps are the node mutations addressed by a node id. parent is a node the
// actor owns (the target of the "move the foreign node to my own parent" case).
func writeOps(parent uuid.UUID) []op {
	return []op{
		{"rename", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.rename.Execute(ctx, nodeuc.RenameNodeInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id), Name: "hijacked"})
		}},
		{"deactivate", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.deact.Execute(ctx, nodeuc.SetStatusInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id)})
		}},
		{"activate", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.act.Execute(ctx, nodeuc.SetStatusInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id)})
		}},
		{"delete", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.del.Execute(ctx, nodeuc.DeleteNodeInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id)})
		}},
		{"create child", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.create.Execute(ctx, nodeuc.CreateNodeInput{Actor: actorOf(actor), RequestID: "t", ParentID: nid(id), Type: "unit", Name: "intruder"})
		}},
		{"move the node away", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.move.Execute(ctx, nodeuc.MoveNodeInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id), NewParentID: nid(parent)})
		}},
	}
}

// memberOps are the member mutations addressed by a node id; existing is a
// real user, so the call is valid except for the node.
func memberOps(existing uuid.UUID, email string) []op {
	return []op{
		{"add new user", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.add.Execute(ctx, member.AddMemberInput{
				Actor: actorOf(actor), RequestID: "t", NodeID: nid(id),
				Email: "fresh-" + uuid.NewString() + "@example.test", Role: "viewer", InitialPassword: "correct horse battery",
			})
		}},
		{"add existing user", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.add.Execute(ctx, member.AddMemberInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id), Email: email, Role: "viewer"})
		}},
		{"change role", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return a.change.Execute(ctx, member.ChangeMemberRoleInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id), UserID: user.ID(existing), Role: "admin"})
		}},
		{"remove", func(ctx context.Context, a *app, actor, id uuid.UUID) (any, error) {
			return nil, a.remove.Execute(ctx, member.RemoveMemberInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(id), UserID: user.ID(existing)})
		}},
	}
}

func concat[T any](parts ...[]T) []T {
	var out []T
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// requireNotFound asserts the answer is the not_found of an unknown node.
func requireNotFound(t *testing.T, res any, err error, what string) {
	t.Helper()
	require.Error(t, err, what)
	assert.Equal(t, apperr.NotFoundError("node"), err, what)
	assert.True(t, isEmpty(res), "%s: a failed call must return an empty output, got %+v", what, res)
}

// requireIndistinguishable runs every op as actor against every target and
// against random ids (v4 and v7), and requires the answers to be the same
// apperr.Error value (code, message, fields), the not_found of an unknown node.
// Comparing the values, not the codes, is what catches a message or a field
// that tells a foreign node from a missing one.
func requireIndistinguishable(ctx context.Context, t *testing.T, a *app, actor uuid.UUID, ops []op, targets []uuid.UUID) {
	t.Helper()
	for _, o := range ops {
		res, base := o.run(ctx, a, actor, uuid.New())
		requireNotFound(t, res, base, o.name+" random v4")
		res, err := o.run(ctx, a, actor, uuid.Must(uuid.NewV7()))
		requireNotFound(t, res, err, o.name+" random v7")
		require.Equal(t, base, err, o.name)
		for _, id := range targets {
			res, err := o.run(ctx, a, actor, id)
			require.Equal(t, base, err, "%s %s: the answer for a foreign node differs from the one for a missing node", o.name, id)
			assert.True(t, isEmpty(res), "%s %s: output must be empty, got %+v", o.name, id, res)
		}
	}
}

// ids collects the node ids of a result for set checks.
func idSet(ids ...uuid.UUID) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// owned wraps the common setup of a test: a world on a fresh database and the
// application stack on the tb_app pool.
func setup(t *testing.T) (context.Context, *testkit.DB, *isolation.World, *app) {
	t.Helper()
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := isolation.NewWorld(ctx, t, db)
	return ctx, db, w, newApp(db.App)
}

func slogTo(w *logSink) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func isEmpty(v any) bool { return v == nil || reflect.ValueOf(v).IsZero() }

func memberListInput(actor, node uuid.UUID, inherited bool) member.ListMembersInput {
	return member.ListMembersInput{Actor: actorOf(actor), NodeID: nid(node), Limit: 50, Inherited: inherited}
}

func getInput(actor, id uuid.UUID) nodeuc.GetNodeInput {
	return nodeuc.GetNodeInput{Actor: actorOf(actor), NodeID: nid(id)}
}
