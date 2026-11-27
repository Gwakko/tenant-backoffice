package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// fakeTx remembers the actor, as the real transaction does.
type fakeTx struct{ actor user.ActorID }

func (fakeTx) TxMarker() {}

type fakeRunner struct{}

func (fakeRunner) InTx(ctx context.Context, actor user.ActorID, _ port.TxOptions, fn func(context.Context, port.Tx) error) error {
	if actor.IsZero() {
		return apperr.New(apperr.Forbidden, "no actor")
	}
	return fn(ctx, fakeTx{actor: actor})
}

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

type key struct {
	node dnode.ID
	key  dset.Key
}

// fakeRepo is an in-memory port.SettingsRepo. Like tb_settings_chain it hides
// the id of an ancestor outside the actor's access roots, but keeps its value.
type fakeRepo struct {
	w      *world
	values map[key]string
	writes int
}

var _ port.SettingsRepo = (*fakeRepo)(nil)

func (r *fakeRepo) Chain(_ context.Context, tx port.Tx, id dnode.ID) ([]port.SettingEntry, error) {
	actor := tx.(fakeTx).actor
	var out []port.SettingEntry
	for depth, anc := range r.w.nodes[id].Path {
		visible := false
		for _, g := range r.w.grants[actor] {
			for _, p := range r.w.nodes[anc].Path {
				if p == g.NodeID {
					visible = true
				}
			}
		}
		for _, k := range dset.Keys() {
			v, ok := r.values[key{anc, k}]
			if !ok {
				continue
			}
			e := port.SettingEntry{Depth: depth + 1, Key: k, Value: v}
			if visible {
				a := anc
				e.NodeID = &a
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *fakeRepo) Set(_ context.Context, _ port.Tx, id dnode.ID, k dset.Key, v string, _ time.Time) (port.SettingWrite, error) {
	cur, had := r.values[key{id, k}]
	if had && cur == v {
		return port.SettingWrite{Before: cur, HadBefore: true}, nil
	}
	r.values[key{id, k}] = v
	r.writes++
	return port.SettingWrite{Before: cur, HadBefore: had, Changed: true}, nil
}

func (r *fakeRepo) Unset(_ context.Context, _ port.Tx, id dnode.ID, k dset.Key) (string, bool, error) {
	cur, had := r.values[key{id, k}]
	if !had {
		return "", false, nil
	}
	delete(r.values, key{id, k})
	r.writes++
	return cur, true, nil
}

type fakeChecker struct{ w *world }

func (c fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, a user.ActorID, id dnode.ID) (role.Role, bool, error) {
	n, ok := c.w.nodes[id]
	if !ok {
		return "", false, nil
	}
	r, ok := role.Effective(n.Path, c.w.grants[a])
	return r, ok, nil
}

// world is the tree
//
//	root(platform) > A(partner) > A1(company) > u1(unit)
//	               > B(partner)
//
// with users: owner (owner on root), adminA (admin on A), adminA1 (admin on A1
// only), viewerA (viewer on A), adminB (admin on B), nobody (no role).
type world struct {
	t      *testing.T
	repo   *fakeRepo
	audit  *fakeAudit
	deps   uc.Deps
	nodes  map[dnode.ID]dnode.Node
	byKey  map[string]dnode.ID
	actors map[string]user.ActorID
	grants map[user.ActorID][]role.Assignment
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		t: t, nodes: map[dnode.ID]dnode.Node{}, byKey: map[string]dnode.ID{},
		actors: map[string]user.ActorID{}, grants: map[user.ActorID][]role.Assignment{},
		audit: &fakeAudit{},
	}
	w.repo = &fakeRepo{w: w, values: map[key]string{}}
	w.deps = uc.Deps{
		Runner: fakeRunner{}, Checker: fakeChecker{w}, Settings: w.repo, Audit: w.audit,
		Clock: fixedClock{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	w.add("root", dnode.TypePlatform, "")
	w.add("A", dnode.TypePartner, "root")
	w.add("A1", dnode.TypeCompany, "A")
	w.add("u1", dnode.TypeUnit, "A1")
	w.add("B", dnode.TypePartner, "root")
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

func (w *world) add(name string, typ dnode.Type, parent string) {
	w.t.Helper()
	id := dnode.ID(uuid.New())
	nm, err := dnode.ParseName(name)
	require.NoError(w.t, err)
	n := dnode.Node{ID: id, Type: typ, Path: dnode.Path{id}, Name: nm, Status: dnode.StatusActive}
	if parent != "" {
		p := w.nodes[w.byKey[parent]]
		n.ParentID = &p.ID
		n.Path = p.Path.Append(id)
	}
	w.nodes[id] = n
	w.byKey[name] = id
}

func (w *world) grant(who, where string, r role.Role) {
	a := w.actors[who]
	w.grants[a] = append(w.grants[a], role.Assignment{NodeID: w.byKey[where], Role: r})
}

func (w *world) id(name string) dnode.ID { return w.byKey[name] }

// seed stores an override as setup, bypassing the use cases.
func (w *world) seed(node string, k dset.Key, v string) {
	w.repo.values[key{w.id(node), k}] = v
}

func (w *world) get(actor, node string) (uc.GetSettingsOutput, error) {
	return uc.NewGetSettings(w.deps).Execute(context.Background(), uc.GetSettingsInput{
		Actor: w.actors[actor], NodeID: w.id(node),
	})
}

func (w *world) set(actor, node, k string, v uc.RawValue) (uc.SetSettingOutput, error) {
	return uc.NewSetSetting(w.deps).Execute(context.Background(), uc.SetSettingInput{
		Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node), Key: k, Value: v,
	})
}

func (w *world) unset(actor, node, k string) (uc.UnsetSettingOutput, error) {
	return uc.NewUnsetSetting(w.deps).Execute(context.Background(), uc.UnsetSettingInput{
		Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node), Key: k,
	})
}

func str(s string) uc.RawValue   { return uc.RawValue{Kind: uc.KindString, Str: s} }
func boolean(b bool) uc.RawValue { return uc.RawValue{Kind: uc.KindBool, Bool: b} }

func item(items []uc.Item, k dset.Key) uc.Item {
	for _, it := range items {
		if it.Key == k {
			return it
		}
	}
	return uc.Item{}
}

func code(t *testing.T, err error) apperr.Code {
	t.Helper()
	c, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	return c
}
