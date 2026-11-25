package member_test

import (
	"context"
	"errors"
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
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

type akey struct {
	node dnode.ID
	user user.ID
}

type fakeUser struct {
	email  user.Email
	hash   user.PasswordHash
	status user.Status
}

// store is the whole in-memory database. The runner restores a snapshot when a
// transaction fails, so a refused request must leave no trace.
type store struct {
	nodes   map[dnode.ID]dnode.Node
	assign  map[akey]port.Member
	users   map[user.ID]fakeUser
	log     []string
	hashing []bool // for each Hash call: was a transaction open
	inTx    bool
}

func (s *store) snapshot() (map[akey]port.Member, map[user.ID]fakeUser) {
	a := make(map[akey]port.Member, len(s.assign))
	for k, v := range s.assign {
		a[k] = v
	}
	u := make(map[user.ID]fakeUser, len(s.users))
	for k, v := range s.users {
		u[k] = v
	}
	return a, u
}

func (s *store) add(e string) { s.log = append(s.log, e) }

func (s *store) count(e string) int {
	n := 0
	for _, x := range s.log {
		if x == e {
			n++
		}
	}
	return n
}

type fakeRunner struct{ s *store }

func (r fakeRunner) InTx(ctx context.Context, actor user.ActorID, _ port.TxOptions, fn func(context.Context, port.Tx) error) error {
	if actor.IsZero() {
		return apperr.New(apperr.Forbidden, "no actor")
	}
	a, u := r.s.snapshot()
	r.s.inTx = true
	err := fn(ctx, fakeTx{})
	r.s.inTx = false
	if err != nil {
		r.s.assign, r.s.users = a, u
	}
	return err
}

type fakeChecker struct{ s *store }

func (c fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, a user.ActorID, id dnode.ID) (role.Role, bool, error) {
	n, ok := c.s.nodes[id]
	if !ok {
		return "", false, nil
	}
	var as []role.Assignment
	for k, m := range c.s.assign {
		if k.user == a {
			as = append(as, role.Assignment{UserID: a, NodeID: k.node, Role: m.Role})
		}
	}
	r, ok := role.Effective(n.Path, as)
	return r, ok, nil
}

// fakeNodes implements only Get; any other call panics through the nil
// embedded interface, which shows a use case reaching for more than it needs.
type fakeNodes struct {
	port.NodeRepo
	s *store
}

func (n fakeNodes) Get(_ context.Context, _ port.Tx, id dnode.ID) (dnode.Node, error) {
	v, ok := n.s.nodes[id]
	if !ok {
		return dnode.Node{}, apperr.NotFoundError("node")
	}
	return v, nil
}

type fakeMembers struct{ s *store }

func (m fakeMembers) GetForUpdate(_ context.Context, _ port.Tx, n dnode.ID, u user.ID) (port.Member, bool, error) {
	v, ok := m.s.assign[akey{n, u}]
	return v, ok, nil
}

func (m fakeMembers) Insert(_ context.Context, _ port.Tx, a role.Assignment) (bool, error) {
	k := akey{a.NodeID, a.UserID}
	if _, ok := m.s.assign[k]; ok {
		return false, nil
	}
	m.s.add("assign-insert")
	m.s.assign[k] = port.Member{UserID: a.UserID, Email: m.s.users[a.UserID].email, Role: a.Role, NodeID: a.NodeID, GrantedAt: a.GrantedAt}
	return true, nil
}

func (m fakeMembers) Update(_ context.Context, _ port.Tx, a role.Assignment) error {
	k := akey{a.NodeID, a.UserID}
	v, ok := m.s.assign[k]
	if !ok {
		return apperr.NotFoundError("member")
	}
	m.s.add("assign-update")
	v.Role, v.GrantedAt = a.Role, a.GrantedAt
	m.s.assign[k] = v
	return nil
}

func (m fakeMembers) Delete(_ context.Context, _ port.Tx, n dnode.ID, u user.ID) error {
	if _, ok := m.s.assign[akey{n, u}]; !ok {
		return apperr.NotFoundError("member")
	}
	m.s.add("assign-delete")
	delete(m.s.assign, akey{n, u})
	return nil
}

func (m fakeMembers) LockActiveOwners(_ context.Context, _ port.Tx, n dnode.ID) ([]user.ID, error) {
	m.s.add("lock-owners")
	var out []user.ID
	for k, v := range m.s.assign {
		if k.node == n && v.Role == role.Owner && m.s.users[k.user].status == user.StatusActive {
			out = append(out, k.user)
		}
	}
	return out, nil
}

func (m fakeMembers) List(_ context.Context, _ port.Tx, n dnode.ID, inherited bool, c *port.MemberCursor, limit int) ([]port.Member, error) {
	path := m.s.nodes[n].Path
	var all []port.Member
	for k, v := range m.s.assign {
		if k.node == n || (inherited && path.Contains(k.node)) {
			all = append(all, v)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Email != b.Email {
			return a.Email < b.Email
		}
		if a.UserID != b.UserID {
			return a.UserID.String() < b.UserID.String()
		}
		return a.NodeID.String() < b.NodeID.String()
	})
	var out []port.Member
	for _, v := range all {
		if c != nil && v.Email.String() <= c.Email {
			continue
		}
		out = append(out, v)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type fakeUsers struct{ s *store }

func (u fakeUsers) IDByEmail(_ context.Context, _ port.Tx, e user.Email) (user.ID, bool, error) {
	for id, v := range u.s.users {
		if v.email == e {
			return id, true, nil
		}
	}
	return user.ID{}, false, nil
}

func (u fakeUsers) Insert(ctx context.Context, tx port.Tx, n port.NewUser) (bool, error) {
	if _, found, _ := u.IDByEmail(ctx, tx, n.Email); found {
		return false, nil
	}
	u.s.add("user-insert")
	u.s.users[n.ID] = fakeUser{email: n.Email, hash: n.Hash, status: user.StatusActive}
	return true, nil
}

func (u fakeUsers) Status(_ context.Context, _ port.Tx, id user.ID) (user.Status, error) {
	v, ok := u.s.users[id]
	if !ok {
		return "", apperr.NotFoundError("user")
	}
	return v.status, nil
}

type fakeHasher struct{ s *store }

func (h fakeHasher) Hash(p string) (user.PasswordHash, error) {
	h.s.hashing = append(h.s.hashing, h.s.inTx)
	if p == "fail-the-hasher" {
		return "", errors.New("hasher down")
	}
	return user.PasswordHash("hash(" + p + ")"), nil
}

func (h fakeHasher) Verify(user.PasswordHash, string) (bool, error) { return false, nil }

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

// world is the tree
//
//	root(platform) > A(partner) > A1(company)
//	               > B(partner)
//
// with users: owner (owner on root, the only root owner), ownerA (owner on A),
// adminA, analystA, viewerA (on A), adminB (admin on B), nobody (no role),
// ghost (inactive, no role), member (viewer on A1).
type world struct {
	t     *testing.T
	s     *store
	audit *fakeAudit
	deps  uc.Deps
	nodes map[string]dnode.Node
	ids   map[string]user.ID

	beforeAssign map[akey]port.Member
	beforeUsers  map[user.ID]fakeUser
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s := &store{nodes: map[dnode.ID]dnode.Node{}, assign: map[akey]port.Member{}, users: map[user.ID]fakeUser{}}
	w := &world{t: t, s: s, audit: &fakeAudit{}, nodes: map[string]dnode.Node{}, ids: map[string]user.ID{}}
	w.deps = uc.Deps{
		Runner: fakeRunner{s}, Checker: fakeChecker{s}, Nodes: fakeNodes{s: s}, Members: fakeMembers{s}, Users: fakeUsers{s},
		Hasher: fakeHasher{s}, Audit: w.audit, Clock: fixedClock{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, IDs: &seqIDs{},
	}
	w.addNode("root", dnode.TypePlatform, "")
	w.addNode("A", dnode.TypePartner, "root")
	w.addNode("A1", dnode.TypeCompany, "A")
	w.addNode("B", dnode.TypePartner, "root")
	for _, n := range []string{"owner", "ownerA", "adminA", "analystA", "viewerA", "adminB", "nobody", "member"} {
		w.addUser(n, user.StatusActive)
	}
	w.addUser("ghost", user.StatusInactive)
	w.grant("owner", "root", role.Owner)
	w.grant("ownerA", "A", role.Owner)
	w.grant("adminA", "A", role.Admin)
	w.grant("analystA", "A", role.Analyst)
	w.grant("viewerA", "A", role.Viewer)
	w.grant("adminB", "B", role.Admin)
	w.grant("member", "A1", role.Viewer)
	w.reset()
	return w
}

func (w *world) addNode(key string, typ dnode.Type, parent string) {
	w.t.Helper()
	id := dnode.ID(uuid.New())
	name, err := dnode.ParseName(key)
	require.NoError(w.t, err)
	n := dnode.Node{ID: id, Type: typ, Path: dnode.Path{id}, Name: name, Status: dnode.StatusActive}
	if parent != "" {
		p := w.nodes[parent]
		n.ParentID = &p.ID
		n.Path = p.Path.Append(id)
	}
	w.nodes[key] = n
	w.s.nodes[id] = n
}

func (w *world) email(name string) user.Email {
	return user.Email(strings.ToLower(name) + "@example.test")
}

func (w *world) addUser(name string, st user.Status) {
	id := user.ID(uuid.New())
	w.ids[name] = id
	w.s.users[id] = fakeUser{email: w.email(name), hash: "hash(original)", status: st}
}

func (w *world) grant(who, where string, r role.Role) {
	k := akey{w.nodes[where].ID, w.ids[who]}
	w.s.assign[k] = port.Member{UserID: w.ids[who], Email: w.email(who), Role: r, NodeID: k.node, GrantedAt: time.Unix(0, 0).UTC()}
}

func (w *world) id(key string) dnode.ID { return w.nodes[key].ID }

// roleOf returns the stored role of a user on a node, "" when there is none.
func (w *world) roleOf(who, where string) role.Role {
	return w.s.assign[akey{w.id(where), w.ids[who]}].Role
}

// reset forgets the setup events so a test sees only its own.
func (w *world) reset() {
	w.s.log, w.audit.entries, w.s.hashing = nil, nil, nil
	w.beforeAssign, w.beforeUsers = w.s.snapshot()
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

// untouched asserts that a refused request changed and audited nothing.
func (w *world) untouched() {
	w.t.Helper()
	require.Equal(w.t, w.beforeAssign, w.s.assign)
	require.Equal(w.t, w.beforeUsers, w.s.users)
	require.Empty(w.t, w.audit.entries)
}
