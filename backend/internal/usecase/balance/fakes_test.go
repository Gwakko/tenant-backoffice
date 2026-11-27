package balance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

// store is the shared in-memory state of all fake repositories. The runner
// snapshots it before a transaction and restores it when the function fails,
// which models rollback.
type store struct {
	balances map[dnode.ID]dbalance.Balance
	ops      []dbalance.Operation
	keys     map[string]port.IdempotencyRecord
	nodes    map[dnode.ID]dnode.Node // set by the world; ops are listed by path
	locks    []dnode.ID              // order of LockForUpdate calls
	saves    int
}

func newStore() *store {
	return &store{balances: map[dnode.ID]dbalance.Balance{}, keys: map[string]port.IdempotencyRecord{}}
}

func (s *store) clone() *store {
	c := &store{balances: map[dnode.ID]dbalance.Balance{}, keys: map[string]port.IdempotencyRecord{}, saves: s.saves, nodes: s.nodes}
	for k, v := range s.balances {
		c.balances[k] = v
	}
	for k, v := range s.keys {
		c.keys[k] = v
	}
	c.ops = append(c.ops, s.ops...)
	c.locks = append(c.locks, s.locks...)
	return c
}

type fakeRunner struct{ s *store }

func (r fakeRunner) InTx(ctx context.Context, actor user.ActorID, _ port.TxOptions, fn func(context.Context, port.Tx) error) error {
	if actor.IsZero() {
		return apperr.New(apperr.Forbidden, "no actor")
	}
	before := r.s.clone()
	if err := fn(ctx, fakeTx{}); err != nil {
		locks := r.s.locks // lock attempts stay visible to assertions
		*r.s = *before
		r.s.locks = locks
		return err
	}
	return nil
}

type fakeNodes struct {
	port.NodeRepo
	nodes map[dnode.ID]dnode.Node
}

func (r *fakeNodes) Get(_ context.Context, _ port.Tx, id dnode.ID) (dnode.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return dnode.Node{}, apperr.NotFoundError("node")
	}
	n.AncestorsActive = true
	for _, a := range n.Path[:len(n.Path)-1] {
		if r.nodes[a].Status != dnode.StatusActive {
			n.AncestorsActive = false
		}
	}
	return n, nil
}

func (r *fakeNodes) IsInSubtree(_ context.Context, _ port.Tx, id, ancestor dnode.ID) (bool, error) {
	n, ok := r.nodes[id]
	if !ok {
		return false, apperr.NotFoundError("node")
	}
	return slices.Contains(n.Path, ancestor), nil
}

type fakeChecker struct {
	nodes  *fakeNodes
	grants map[user.ActorID][]role.Assignment
}

func (c *fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, a user.ActorID, id dnode.ID) (role.Role, bool, error) {
	n, ok := c.nodes.nodes[id]
	if !ok {
		return "", false, nil
	}
	r, ok := role.Effective(n.Path, c.grants[a])
	return r, ok, nil
}

type fakeBalances struct{ s *store }

func (r fakeBalances) Get(_ context.Context, _ port.Tx, id dnode.ID) (dbalance.Balance, error) {
	b, ok := r.s.balances[id]
	if !ok {
		return dbalance.Balance{NodeID: id}, nil
	}
	return b, nil
}

func (r fakeBalances) LockForUpdate(_ context.Context, _ port.Tx, id dnode.ID, now time.Time) (dbalance.Balance, error) {
	r.s.locks = append(r.s.locks, id)
	b, ok := r.s.balances[id]
	if !ok {
		b = dbalance.Balance{NodeID: id, UpdatedAt: now}
		r.s.balances[id] = b
	}
	return b, nil
}

func (r fakeBalances) Save(_ context.Context, _ port.Tx, b dbalance.Balance) error {
	r.s.saves++
	r.s.balances[b.NodeID] = b
	return nil
}

type fakeOps struct{ s *store }

func (r fakeOps) Insert(_ context.Context, _ port.Tx, op dbalance.Operation) error {
	r.s.ops = append(r.s.ops, op)
	return nil
}

func (r fakeOps) Get(_ context.Context, _ port.Tx, id dbalance.OperationID) (dbalance.Operation, error) {
	for _, op := range r.s.ops {
		if op.ID == id {
			return op, nil
		}
	}
	return dbalance.Operation{}, apperr.NotFoundError("operation")
}

func (r fakeOps) GetTransfer(_ context.Context, _ port.Tx, id dbalance.TransferID) (dbalance.Operation, dbalance.Operation, error) {
	var out, in *dbalance.Operation
	for i := range r.s.ops {
		op := r.s.ops[i]
		if op.TransferID == nil || *op.TransferID != id {
			continue
		}
		if op.Kind == dbalance.TransferOut {
			out = &op
		} else {
			in = &op
		}
	}
	if out == nil || in == nil {
		return dbalance.Operation{}, dbalance.Operation{}, apperr.NotFoundError("operation")
	}
	return *out, *in, nil
}

func (r fakeOps) List(_ context.Context, _ port.Tx, f port.OperationFilter, after *port.OperationCursor, limit int) ([]port.OperationRecord, error) {
	var all []port.OperationRecord
	for _, op := range r.s.ops {
		n, ok := r.s.nodes[op.NodeID]
		if !ok || !slices.Contains(n.Path, f.ScopeNodeID) {
			continue
		}
		if f.NodeID != nil && op.NodeID != *f.NodeID {
			continue
		}
		if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, op.Kind) {
			continue
		}
		if f.From != nil && op.CreatedAt.Before(*f.From) || f.To != nil && !op.CreatedAt.Before(*f.To) {
			continue
		}
		all = append(all, port.OperationRecord{Operation: op, NodeName: n.Name.String()})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].Operation, all[j].Operation
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return bytes.Compare(a.ID[:], b.ID[:]) > 0
	})
	if after != nil {
		var rest []port.OperationRecord
		for _, r := range all {
			o := r.Operation
			if o.CreatedAt.Before(after.CreatedAt) || o.CreatedAt.Equal(after.CreatedAt) && bytes.Compare(o.ID[:], after.ID[:]) < 0 {
				rest = append(rest, r)
			}
		}
		all = rest
	}
	return all[:min(limit, len(all))], nil
}

type fakeKeys struct{ s *store }

func (r fakeKeys) Claim(_ context.Context, _ port.Tx, rec port.IdempotencyRecord, now time.Time) (bool, port.IdempotencyRecord, error) {
	k := rec.UserID.String() + "/" + rec.Key
	if ex, ok := r.s.keys[k]; ok {
		if ex.ExpiresAt.After(now) {
			return false, ex, nil
		}
		delete(r.s.keys, k)
	}
	r.s.keys[k] = rec
	return true, port.IdempotencyRecord{}, nil
}

type fakeLimits struct {
	enabled map[dnode.ID]bool
	max     map[dnode.ID]money.Amount
}

func (l fakeLimits) EffectiveLimits(_ context.Context, _ port.Tx, id dnode.ID) (port.Limits, error) {
	out := port.Limits{OperationsEnabled: true, MaxOperationAmount: 1_000_000}
	if v, ok := l.enabled[id]; ok {
		out.OperationsEnabled = v
	}
	if v, ok := l.max[id]; ok {
		out.MaxOperationAmount = v
	}
	return out, nil
}

type fakeAudit struct{ entries []port.AuditEntry }

func (a *fakeAudit) Write(_ context.Context, _ port.Tx, e port.AuditEntry) error {
	a.entries = append(a.entries, e)
	return nil
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type seqIDs struct{ n int }

func (s *seqIDs) New() uuid.UUID {
	s.n++
	return uuid.UUID{14: byte(s.n >> 8), 15: byte(s.n)}
}

// world is the tree
//
//	root > A > A1 > u1, u2
//	     > B > B1
//
// with owner (owner on root), adminA (admin on A), viewerA (viewer on A),
// adminB (admin on B) and nobody (no role).
type world struct {
	t      *testing.T
	s      *store
	nodes  *fakeNodes
	chk    *fakeChecker
	lim    fakeLimits
	audit  *fakeAudit
	clk    *clock
	deps   uc.Deps
	byKey  map[string]dnode.Node
	actors map[string]user.ActorID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s := newStore()
	nodes := &fakeNodes{nodes: map[dnode.ID]dnode.Node{}}
	chk := &fakeChecker{nodes: nodes, grants: map[user.ActorID][]role.Assignment{}}
	w := &world{
		t: t, s: s, nodes: nodes, chk: chk, audit: &fakeAudit{},
		lim:   fakeLimits{enabled: map[dnode.ID]bool{}, max: map[dnode.ID]money.Amount{}},
		clk:   &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		byKey: map[string]dnode.Node{}, actors: map[string]user.ActorID{},
	}
	w.deps = uc.Deps{
		Runner: fakeRunner{s}, Checker: chk, Nodes: nodes, Balances: fakeBalances{s}, Ops: fakeOps{s},
		Keys: fakeKeys{s}, Limits: w.lim, Audit: w.audit, Clock: w.clk, IDs: &seqIDs{},
	}
	w.add("root", dnode.TypePlatform, "")
	w.add("A", dnode.TypePartner, "root")
	w.add("A1", dnode.TypeCompany, "A")
	w.add("u1", dnode.TypeUnit, "A1")
	w.add("u2", dnode.TypeUnit, "A1")
	w.add("B", dnode.TypePartner, "root")
	w.add("B1", dnode.TypeCompany, "B")
	for _, a := range []string{"owner", "adminA", "viewerA", "adminB", "nobody"} {
		w.actors[a] = user.ActorID(uuid.New())
	}
	w.grant("owner", "root", role.Owner)
	w.grant("adminA", "A", role.Admin)
	w.grant("viewerA", "A", role.Viewer)
	w.grant("adminB", "B", role.Admin)
	return w
}

func (w *world) add(key string, typ dnode.Type, parent string) {
	w.t.Helper()
	id := dnode.ID(uuid.New())
	name, err := dnode.ParseName(key)
	require.NoError(w.t, err)
	n := dnode.Node{ID: id, Type: typ, Path: dnode.Path{id}, Name: name, Status: dnode.StatusActive}
	if parent != "" {
		p := w.byKey[parent]
		n.ParentID = &p.ID
		n.Path = p.Path.Append(id)
	}
	w.byKey[key] = n
	w.nodes.nodes[id] = n
	w.s.nodes = w.nodes.nodes
}

func (w *world) grant(who, where string, r role.Role) {
	a := w.actors[who]
	w.chk.grants[a] = append(w.chk.grants[a], role.Assignment{NodeID: w.byKey[where].ID, Role: r})
}

func (w *world) id(key string) dnode.ID { return w.byKey[key].ID }

func (w *world) deactivate(key string) {
	n := w.nodes.nodes[w.id(key)]
	n.Status = dnode.StatusInactive
	w.nodes.nodes[n.ID] = n
}

// fund sets a balance directly.
func (w *world) fund(key string, amount money.Amount) {
	w.s.balances[w.id(key)] = dbalance.Balance{NodeID: w.id(key), Amount: amount, UpdatedAt: w.clk.t}
}

func (w *world) amount(key string) money.Amount { return w.s.balances[w.id(key)].Amount }

// hash stands in for the transport layer's request hash.
func hash(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{'\n'})
	}
	return h.Sum(nil)
}

func (w *world) in(who, node, amount, key string) uc.OperateInput {
	return uc.OperateInput{
		Actor: w.actors[who], RequestID: "r1", NodeID: w.id(node), Amount: amount, IdempotencyKey: key,
		RequestHash: hash(node, amount, key),
	}
}

func (w *world) credit(who, node, amount, key string) (uc.OperateOutput, error) {
	return uc.NewCredit(w.deps).Execute(context.Background(), w.in(who, node, amount, key))
}

func (w *world) debit(who, node, amount, key string) (uc.OperateOutput, error) {
	return uc.NewDebit(w.deps).Execute(context.Background(), w.in(who, node, amount, key))
}

func (w *world) transfer(who, from, to, amount, key string) (uc.TransferOutput, error) {
	return uc.NewTransfer(w.deps).Execute(context.Background(), uc.TransferInput{
		Actor: w.actors[who], RequestID: "r1", FromNodeID: w.id(from), ToNodeID: w.id(to),
		Amount: amount, IdempotencyKey: key, RequestHash: hash(from, to, amount, key),
	})
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

// untouched asserts that a refused request changed nothing.
func (w *world) untouched(before *store) {
	w.t.Helper()
	require.Equal(w.t, before.balances, w.s.balances)
	require.Equal(w.t, before.ops, w.s.ops)
	require.Equal(w.t, before.keys, w.s.keys)
	require.Empty(w.t, w.audit.entries)
}

func (w *world) snapshot() *store { return w.s.clone() }

func newCredit(w *world) *uc.Credit         { return uc.NewCredit(w.deps) }
func newDebit(w *world) *uc.Debit           { return uc.NewDebit(w.deps) }
func newGetBalance(w *world) *uc.GetBalance { return uc.NewGetBalance(w.deps) }
func getIn(w *world, who, node string) uc.GetBalanceInput {
	return uc.GetBalanceInput{Actor: w.actors[who], RequestID: "r", NodeID: w.id(node)}
}

func newTransfer(w *world) *uc.Transfer { return uc.NewTransfer(w.deps) }

func baseTransfer(w *world, from, to string) uc.TransferInput {
	return uc.TransferInput{
		Actor: w.actors["adminA"], RequestID: "r", FromNodeID: w.id(from), ToNodeID: w.id(to), Amount: "10",
	}
}
