package node_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

// events is the shared, ordered log of what the fakes were asked to do.
type events struct{ list []string }

func (e *events) add(s string) { e.list = append(e.list, s) }

func (e *events) count(prefix string) int {
	n := 0
	for _, s := range e.list {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

func (e *events) index(s string) int {
	for i, x := range e.list {
		if x == s {
			return i
		}
	}
	return -1
}

type fakeRunner struct{ ev *events }

func (r fakeRunner) InTx(ctx context.Context, actor user.ActorID, _ port.TxOptions, fn func(context.Context, port.Tx) error) error {
	if actor.IsZero() {
		return apperr.New(apperr.Forbidden, "no actor")
	}
	return fn(ctx, fakeTx{})
}

type fakeLock struct{ ev *events }

func (l fakeLock) Lock(context.Context, port.Tx) error { l.ev.add("lock"); return nil }

type fakeAudit struct{ entries []port.AuditEntry }

func (a *fakeAudit) Write(_ context.Context, _ port.Tx, e port.AuditEntry) error {
	a.entries = append(a.entries, e)
	return nil
}

func (a *fakeAudit) actions() []string {
	var out []string
	for _, e := range a.entries {
		out = append(out, e.Action)
	}
	return out
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type seqIDs struct{ n int }

func (s *seqIDs) New() uuid.UUID {
	s.n++
	return uuid.UUID{15: byte(100 + s.n)}
}

// fakeRepo is an in-memory port.NodeRepo. It does not model visibility: use
// cases must decide that through the checker before they reach it.
type fakeRepo struct {
	ev    *events
	nodes map[dnode.ID]dnode.Node
	// extra overrides facts the fake cannot derive.
	children map[dnode.ID]int
	stats    map[dnode.ID]dnode.DeleteStats
	roots    map[user.ActorID][]port.AccessRoot
}

var _ port.NodeRepo = (*fakeRepo)(nil)

func (r *fakeRepo) effective(n dnode.Node) dnode.Node {
	n.AncestorsActive = true
	for _, id := range n.Path[:len(n.Path)-1] {
		if r.nodes[id].Status != dnode.StatusActive {
			n.AncestorsActive = false
		}
	}
	return n
}

func (r *fakeRepo) Get(_ context.Context, _ port.Tx, id dnode.ID) (dnode.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return dnode.Node{}, apperr.NotFoundError("node")
	}
	return r.effective(n), nil
}

func (r *fakeRepo) GetForUpdate(ctx context.Context, tx port.Tx, id dnode.ID) (dnode.Node, error) {
	return r.Get(ctx, tx, id)
}

func (r *fakeRepo) Insert(_ context.Context, _ port.Tx, n dnode.Node) error {
	r.ev.add("insert")
	r.nodes[n.ID] = n
	return nil
}

func (r *fakeRepo) Update(_ context.Context, _ port.Tx, n dnode.Node) error {
	r.ev.add("update")
	r.nodes[n.ID] = n
	return nil
}

func (r *fakeRepo) Delete(_ context.Context, _ port.Tx, id dnode.ID) error {
	r.ev.add("delete")
	delete(r.nodes, id)
	return nil
}

func (r *fakeRepo) kids(parent dnode.ID) []dnode.Node {
	var out []dnode.Node
	for _, n := range r.nodes {
		if n.ParentID != nil && *n.ParentID == parent {
			out = append(out, r.effective(n))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Name.Key() != b.Name.Key() {
			return a.Name.Key() < b.Name.Key()
		}
		return a.ID.String() < b.ID.String()
	})
	return out
}

func after(n dnode.Node, c *port.ChildCursor) bool {
	if c == nil {
		return true
	}
	if n.Name.Key() != c.NameKey {
		return n.Name.Key() > c.NameKey
	}
	return n.ID.String() > c.ID.String()
}

func (r *fakeRepo) ListChildren(ctx context.Context, tx port.Tx, p dnode.ID, c *port.ChildCursor, limit int) ([]dnode.Node, error) {
	return r.FindChildren(ctx, tx, p, port.ChildFilter{}, c, limit)
}

func (r *fakeRepo) FindChildren(_ context.Context, _ port.Tx, p dnode.ID, f port.ChildFilter, c *port.ChildCursor, limit int) ([]dnode.Node, error) {
	var out []dnode.Node
	for _, n := range r.kids(p) {
		if !after(n, c) || (f.Status != "" && n.Status != f.Status) || !strings.Contains(n.Name.Key(), f.Query) {
			continue
		}
		out = append(out, n)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *fakeRepo) CountChildren(_ context.Context, _ port.Tx, p dnode.ID) (int, error) {
	if n, ok := r.children[p]; ok {
		return n, nil
	}
	return len(r.kids(p)), nil
}

func (r *fakeRepo) Subtree(_ context.Context, _ port.Tx, root dnode.ID) ([]dnode.Node, error) {
	var out []dnode.Node
	for _, n := range r.nodes {
		if n.Path.Contains(root) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (r *fakeRepo) IsInSubtree(_ context.Context, _ port.Tx, id, anc dnode.ID) (bool, error) {
	return r.nodes[id].Path.Contains(anc), nil
}

func (r *fakeRepo) Move(_ context.Context, _ port.Tx, plan dnode.MovePlan, expected int, now time.Time) error {
	r.ev.add("move")
	rewritten := 0
	for id, n := range r.nodes {
		if !n.Path.Contains(plan.NodeID) {
			continue
		}
		i := len(plan.OldPath) - 1
		n.Path = append(append(dnode.Path{}, plan.NewPath...), n.Path[i+1:]...)
		if id == plan.NodeID {
			p := plan.NewParentID
			n.ParentID = &p
		}
		n.UpdatedAt = now
		r.nodes[id] = n
		rewritten++
	}
	if rewritten != expected {
		return apperr.New(apperr.ValidationFailed, "row count mismatch")
	}
	return nil
}

func (r *fakeRepo) ListActiveByType(_ context.Context, _ port.Tx, t dnode.Type, q string, c *port.ChildCursor, limit int) ([]dnode.Node, error) {
	var all []dnode.Node
	for _, n := range r.nodes {
		n = r.effective(n)
		if n.Type == t && n.EffectiveActive() && strings.Contains(n.Name.Key(), q) && after(n, c) {
			all = append(all, n)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name.Key() < all[j].Name.Key() })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (r *fakeRepo) Ancestors(_ context.Context, _ port.Tx, id dnode.ID) ([]dnode.Node, error) {
	var out []dnode.Node
	for _, a := range r.nodes[id].Path {
		out = append(out, r.nodes[a])
	}
	return out, nil
}

func (r *fakeRepo) DeleteStats(ctx context.Context, tx port.Tx, id dnode.ID) (dnode.DeleteStats, error) {
	s := r.stats[id]
	c, _ := r.CountChildren(ctx, tx, id)
	s.Children = c
	return s, nil
}

func (r *fakeRepo) AccessRoots(_ context.Context, _ port.Tx, a user.ActorID) ([]port.AccessRoot, error) {
	return r.roots[a], nil
}

// fakeChecker resolves roles from in-memory assignments.
type fakeChecker struct {
	repo   *fakeRepo
	grants map[user.ActorID][]role.Assignment
}

func (c *fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, a user.ActorID, id dnode.ID) (role.Role, bool, error) {
	n, ok := c.repo.nodes[id]
	if !ok {
		return "", false, nil
	}
	r, ok := role.Effective(n.Path, c.grants[a])
	return r, ok, nil
}

// world is the tree
//
//	root(platform) > A(partner) > A1(company) > u1, u2(unit)
//	               > B(partner) > B1(company)
//
// with users: owner (owner on root), adminA (admin on A), adminA1 (admin on
// A1 only), viewerA (viewer on A), adminB (admin on B), nobody (no role).
type world struct {
	t      *testing.T
	ev     *events
	repo   *fakeRepo
	audit  *fakeAudit
	chk    *fakeChecker
	deps   uc.Deps
	nodes  map[string]dnode.Node
	actors map[string]user.ActorID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ev := &events{}
	repo := &fakeRepo{
		ev: ev, nodes: map[dnode.ID]dnode.Node{}, children: map[dnode.ID]int{},
		stats: map[dnode.ID]dnode.DeleteStats{}, roots: map[user.ActorID][]port.AccessRoot{},
	}
	chk := &fakeChecker{repo: repo, grants: map[user.ActorID][]role.Assignment{}}
	audit := &fakeAudit{}
	w := &world{
		t: t, ev: ev, repo: repo, audit: audit, chk: chk,
		nodes: map[string]dnode.Node{}, actors: map[string]user.ActorID{},
	}
	w.deps = uc.Deps{
		Runner: fakeRunner{ev}, Checker: chk, Nodes: repo, Lock: fakeLock{ev}, Audit: audit,
		Clock: fixedClock{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, IDs: &seqIDs{},
	}
	w.add("root", dnode.TypePlatform, "")
	w.add("A", dnode.TypePartner, "root")
	w.add("A1", dnode.TypeCompany, "A")
	w.add("u1", dnode.TypeUnit, "A1")
	w.add("u2", dnode.TypeUnit, "A1")
	w.add("B", dnode.TypePartner, "root")
	w.add("B1", dnode.TypeCompany, "B")
	for _, a := range []string{"owner", "adminA", "adminA1", "viewerA", "adminB", "nobody"} {
		w.actors[a] = user.ActorID(uuid.New())
	}
	w.grant("owner", "root", role.Owner)
	w.grant("adminA", "A", role.Admin)
	w.grant("adminA1", "A1", role.Admin)
	w.grant("viewerA", "A", role.Viewer)
	w.grant("adminB", "B", role.Admin)
	return w
}

func (w *world) add(key string, typ dnode.Type, parent string) {
	w.t.Helper()
	id := dnode.ID(uuid.New())
	name, err := dnode.ParseName(key)
	require.NoError(w.t, err)
	n := dnode.Node{
		ID: id, Type: typ, Path: dnode.Path{id}, Name: name, Status: dnode.StatusActive,
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}
	if parent != "" {
		p := w.nodes[parent]
		n.ParentID = &p.ID
		n.Path = p.Path.Append(id)
	}
	w.nodes[key] = n
	w.repo.nodes[id] = n
}

func (w *world) grant(who, where string, r role.Role) {
	a := w.actors[who]
	w.chk.grants[a] = append(w.chk.grants[a], role.Assignment{NodeID: w.nodes[where].ID, Role: r})
}

func (w *world) id(key string) dnode.ID { return w.nodes[key].ID }

func (w *world) stored(key string) dnode.Node { return w.repo.nodes[w.nodes[key].ID] }

func (w *world) deactivate(key string) {
	n := w.repo.nodes[w.id(key)]
	n.Status = dnode.StatusInactive
	w.repo.nodes[n.ID] = n
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

// untouched asserts that a refused request wrote nothing and took no lock.
func (w *world) untouched() {
	w.t.Helper()
	require.Empty(w.t, w.ev.list, "no repository write and no lock expected")
	require.Empty(w.t, w.audit.entries)
}

// requireBefore asserts that both events happened and first came before second.
func (w *world) requireBefore(first, second string) {
	w.t.Helper()
	i, j := w.ev.index(first), w.ev.index(second)
	require.GreaterOrEqual(w.t, i, 0, "%s never happened", first)
	require.GreaterOrEqual(w.t, j, 0, "%s never happened", second)
	require.Less(w.t, i, j, "%s must come before %s", first, second)
}
