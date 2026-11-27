package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	daudit "github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

type fakeRunner struct{ readOnly *bool }

func (r fakeRunner) InTx(ctx context.Context, _ user.ActorID, o port.TxOptions, fn func(context.Context, port.Tx) error) error {
	*r.readOnly = o.ReadOnly
	return fn(ctx, fakeTx{})
}

// fakeChecker gives each actor one role on one node; any other node is hidden.
type fakeChecker struct {
	roles map[user.ActorID]role.Role
	node  dnode.ID
}

func (c fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, a user.ActorID, id dnode.ID) (role.Role, bool, error) {
	r, ok := c.roles[a]
	return r, ok && id == c.node, nil
}

// fakeRepo serves a fixed, already ordered journal and records the call.
type fakeRepo struct {
	records []port.AuditRecord
	calls   int
	filter  port.AuditFilter
	limit   int
	after   *port.AuditCursor
}

func (r *fakeRepo) List(_ context.Context, _ port.Tx, f port.AuditFilter, after *port.AuditCursor, limit int) ([]port.AuditRecord, error) {
	r.calls++
	r.filter, r.after, r.limit = f, after, limit
	start := 0
	if after != nil {
		for i, x := range r.records {
			if x.Entry.ID() == after.ID {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(r.records))
	return r.records[start:end], nil
}

// fakeNodes answers only the subtree question of the nodeId filter.
type fakeNodes struct {
	port.NodeRepo
	inside bool
}

func (n *fakeNodes) IsInSubtree(context.Context, port.Tx, dnode.ID, dnode.ID) (bool, error) {
	return n.inside, nil
}

type env struct {
	nodes  *fakeNodes
	uc     *uc.ListAudit
	repo   *fakeRepo
	scope  dnode.ID
	actors map[string]user.ActorID
	txRO   *bool
}

func newEnv(t *testing.T, n int) *env {
	t.Helper()
	scope := dnode.ID(uuid.New())
	e := &env{scope: scope, actors: map[string]user.ActorID{}, txRO: new(bool), repo: &fakeRepo{}, nodes: &fakeNodes{inside: true}}
	roles := map[user.ActorID]role.Role{}
	for name, r := range map[string]role.Role{"viewer": role.Viewer, "analyst": role.Analyst, "admin": role.Admin, "owner": role.Owner} {
		e.actors[name] = user.ActorID(uuid.New())
		roles[e.actors[name]] = r
	}
	e.actors["nobody"] = user.ActorID(uuid.New())
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := range n {
		en, err := daudit.NewEntry(daudit.Params{
			ID: daudit.ID(uuid.New()), OccurredAt: at.Add(-time.Duration(i) * time.Second), ActorID: e.actors["owner"],
			NodeID: scope, Action: daudit.NodeRenamed, TargetID: uuid.NewString(),
			Before: daudit.Fields{{Key: "name", Value: "a"}}, After: daudit.Fields{{Key: "name", Value: "b"}},
		})
		require.NoError(t, err)
		e.repo.records = append(e.repo.records, port.AuditRecord{Entry: en, NodeName: "N"})
	}
	e.uc = uc.NewListAudit(uc.Deps{Runner: fakeRunner{e.txRO}, Checker: fakeChecker{roles: roles, node: scope}, Nodes: e.nodes, Audit: e.repo})
	return e
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	assert.Equal(t, code, got)
}

func (e *env) list(who string, mod func(*uc.ListAuditInput)) (uc.ListAuditOutput, error) {
	in := uc.ListAuditInput{Actor: e.actors[who], ScopeNodeID: e.scope, Limit: 2}
	if mod != nil {
		mod(&in)
	}
	return e.uc.Execute(context.Background(), in)
}

func TestListAuditAuthorization(t *testing.T) {
	e := newEnv(t, 1)
	// audit.read: analyst, admin and owner; not viewer.
	_, err := e.list("viewer", nil)
	requireCode(t, err, apperr.Forbidden)
	for _, who := range []string{"analyst", "admin", "owner"} {
		_, err := e.list(who, nil)
		require.NoError(t, err, who)
	}
	// An actor who cannot see the scope gets not_found and the store is not asked.
	calls := e.repo.calls
	_, err = e.list("nobody", nil)
	requireCode(t, err, apperr.NotFound)
	_, err = e.list("owner", func(in *uc.ListAuditInput) { in.ScopeNodeID = dnode.ID(uuid.New()) })
	requireCode(t, err, apperr.NotFound)
	assert.Equal(t, calls, e.repo.calls)
	assert.True(t, *e.txRO, "the read runs in a read-only transaction")
}

func TestListAuditValidation(t *testing.T) {
	e := newEnv(t, 1)
	bad := "node.exploded"
	later, earlier := time.Unix(200, 0), time.Unix(100, 0)
	cases := map[string]func(*uc.ListAuditInput){
		"no scope":      func(in *uc.ListAuditInput) { in.ScopeNodeID = dnode.ID{} },
		"unknown":       func(in *uc.ListAuditInput) { in.Action = &bad },
		"from after to": func(in *uc.ListAuditInput) { in.From, in.To = &later, &earlier },
		"from equals to": func(in *uc.ListAuditInput) {
			in.From, in.To = &earlier, &earlier
		},
		"limit zero":   func(in *uc.ListAuditInput) { in.Limit = 0 },
		"limit big":    func(in *uc.ListAuditInput) { in.Limit = 201 },
		"bad cursor":   func(in *uc.ListAuditInput) { in.Cursor = "%%%" },
		"cursor shape": func(in *uc.ListAuditInput) { in.Cursor = "bm9wZQ" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.list("owner", mod)
			requireCode(t, err, apperr.ValidationFailed)
		})
	}
	assert.Zero(t, e.repo.calls, "the store is not reached with an invalid input")
}

// Pages follow each other without gaps or repeats; the filters reach the store.
func TestListAuditPaging(t *testing.T) {
	e := newEnv(t, 5)
	var seen []daudit.ID
	cursor := ""
	for page := 0; page < 3; page++ {
		out, err := e.list("owner", func(in *uc.ListAuditInput) { in.Cursor = cursor })
		require.NoError(t, err)
		for _, it := range out.Items {
			seen = append(seen, it.Entry.ID())
		}
		assert.Equal(t, 3, e.repo.limit, "one extra row decides whether there is a next page")
		cursor = out.NextCursor
		if page < 2 {
			require.NotEmpty(t, cursor)
		}
	}
	assert.Empty(t, cursor, "the last page has no cursor")
	require.Len(t, seen, 5)
	for i, r := range e.repo.records {
		assert.Equal(t, r.Entry.ID(), seen[i])
	}

	act := "node.renamed"
	who := user.ID(uuid.New())
	nid := dnode.ID(uuid.New())
	from, to := time.Unix(100, 0), time.Unix(200, 0)
	_, err := e.list("owner", func(in *uc.ListAuditInput) {
		in.Action, in.ActorID, in.NodeID, in.From, in.To = &act, &who, &nid, &from, &to
	})
	require.NoError(t, err)
	assert.Equal(t, daudit.NodeRenamed, *e.repo.filter.Action)
	assert.Equal(t, who, *e.repo.filter.ActorID)
	assert.Equal(t, nid, *e.repo.filter.NodeID)
	assert.Equal(t, e.scope, e.repo.filter.ScopeNodeID)
	assert.Equal(t, from, *e.repo.filter.From)
	assert.Equal(t, to, *e.repo.filter.To)

	out, err := e.list("owner", func(in *uc.ListAuditInput) { in.Limit = 10 })
	require.NoError(t, err)
	assert.Len(t, out.Items, 5)
	assert.Empty(t, out.NextCursor)
}

// EC-52: a nodeId outside the subtree of the scope is not_found and the store
// is not asked.
func TestListAuditNodeFilterOutsideScope(t *testing.T) {
	e := newEnv(t, 2)
	e.nodes.inside = false
	nid := dnode.ID(uuid.New())
	calls := e.repo.calls
	_, err := e.list("owner", func(in *uc.ListAuditInput) { in.NodeID = &nid })
	requireCode(t, err, apperr.NotFound)
	assert.Equal(t, calls, e.repo.calls)
}
