package balance_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

func TestTransfer(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 300)
	out, err := w.transfer("adminA", "u1", "u2", "120", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.Equal(t, money.Amount(180), w.amount("u1"))
	assert.Equal(t, money.Amount(120), w.amount("u2"))

	assert.Equal(t, dbalance.TransferOut, out.Out.Kind)
	assert.Equal(t, dbalance.TransferIn, out.In.Kind)
	assert.Equal(t, out.TransferID, *out.Out.TransferID)
	assert.Equal(t, out.TransferID, *out.In.TransferID)
	assert.Equal(t, out.Out.Amount, out.In.Amount)
	assert.Equal(t, w.id("u2"), *out.Out.CounterpartyNodeID)
	assert.Equal(t, w.id("u1"), *out.In.CounterpartyNodeID)
	assert.Equal(t, money.Amount(180), out.Out.BalanceAfter)
	assert.Equal(t, money.Amount(120), out.In.BalanceAfter)
	assert.Len(t, w.s.ops, 2)

	require.Len(t, w.audit.entries, 2)
	assert.Equal(t, "balance.transferred", w.audit.entries[0].Action)
	assert.Equal(t, "balance.transferred", w.audit.entries[1].Action)
	assert.Equal(t, w.id("u1"), w.audit.entries[0].NodeID)
	assert.Equal(t, w.id("u2"), w.audit.entries[1].NodeID)
	assert.Equal(t, "transfer_out", w.audit.entries[0].After["direction"])
	assert.Equal(t, "transfer_in", w.audit.entries[1].After["direction"])
}

// Test-ID: T-U-25
// EC-78: a transfer to oneself is refused before any store access.
func TestSameNode(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 300)
	before := w.snapshot()
	_, err := w.transfer("adminA", "u1", "u1", "10", "k1")
	requireCode(t, err, apperr.SameNode)
	w.untouched(before)
	assert.Empty(t, w.s.locks)
}

// Test-ID: T-U-61
// EC-79: balances are locked in ascending node id order for both directions.
func TestTransferLocksInAscendingOrder(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 1000)
	w.fund("u2", 1000)
	lo, hi := "u1", "u2"
	if lessID(w.id("u2"), w.id("u1")) {
		lo, hi = hi, lo
	}
	_, err := w.transfer("adminA", lo, hi, "10", "k1")
	require.NoError(t, err)
	_, err = w.transfer("adminA", hi, lo, "10", "k2")
	require.NoError(t, err)
	want := []dnode.ID{w.id(lo), w.id(hi), w.id(lo), w.id(hi)}
	assert.Equal(t, want, w.s.locks, "both directions must lock the lower id first")
}

func lessID(a, b dnode.ID) bool {
	x, y := uuid.UUID(a), uuid.UUID(b)
	return bytes.Compare(x[:], y[:]) < 0
}

func TestTransferInsufficientFunds(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 50)
	before := w.snapshot()
	_, err := w.transfer("adminA", "u1", "u2", "51", "k1")
	requireCode(t, err, apperr.InsufficientFunds)
	w.untouched(before)
}

func TestTransferOverflow(t *testing.T) {
	w := newWorld(t)
	w.lim.max[w.id("u1")] = money.Max
	w.lim.max[w.id("u2")] = money.Max
	w.fund("u1", 100)
	w.fund("u2", money.Max-10)
	before := w.snapshot()
	_, err := w.transfer("adminA", "u1", "u2", "11", "k1")
	requireCode(t, err, apperr.BalanceOverflow)
	w.untouched(before)
}

// EC-50: both nodes must be visible and allowed; one foreign node is not_found.
func TestTransferAuthorization(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	w.fund("B1", 100)
	before := w.snapshot()
	for _, c := range []struct{ who, from, to string }{
		{"adminA", "u1", "B1"}, {"adminA", "B1", "u1"}, {"adminB", "u1", "B1"}, {"nobody", "u1", "u2"},
	} {
		_, err := w.transfer(c.who, c.from, c.to, "10", "k"+c.who+c.from)
		requireCode(t, err, apperr.NotFound)
	}
	// Visible but without the right.
	_, err := w.transfer("viewerA", "u1", "u2", "10", "kv")
	requireCode(t, err, apperr.Forbidden)
	w.untouched(before)
}

// EC-81 for a transfer: either node blocks it.
func TestTransferChecksBothNodes(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	w.fund("u2", 100)
	before := w.snapshot()

	w.lim.enabled[w.id("u2")] = false
	_, err := w.transfer("adminA", "u1", "u2", "10", "k1")
	requireCode(t, err, apperr.OperationsDisabled)
	_, err = w.transfer("adminA", "u2", "u1", "10", "k2")
	requireCode(t, err, apperr.OperationsDisabled)
	w.lim.enabled[w.id("u2")] = true

	w.deactivate("u2")
	_, err = w.transfer("adminA", "u1", "u2", "10", "k3")
	requireCode(t, err, apperr.NodeInactive)
	w.untouched(before)

	w.nodes.nodes[w.id("u2")] = activeCopy(w.nodes.nodes[w.id("u2")])
	w.lim.max[w.id("u2")] = 5
	_, err = w.transfer("adminA", "u1", "u2", "10", "k4")
	requireCode(t, err, apperr.AmountExceedsLimit)
	w.untouched(before)
}

func activeCopy(n dnode.Node) dnode.Node {
	n.Status = dnode.StatusActive
	return n
}

// Test-ID: T-U-62
// EC-80: a failure on the second leg rolls everything back, key included.
func TestTransferAtomicOnFailure(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	boom := errors.New("boom")
	w.deps.Ops = failingOps{fakeOps: fakeOps{w.s}, failKind: dbalance.TransferIn, err: boom}
	before := w.snapshot()
	_, err := w.transfer("adminA", "u1", "u2", "10", "k1")
	require.ErrorIs(t, err, boom)
	w.untouched(before)
	assert.Empty(t, w.s.keys)
}

type failingOps struct {
	fakeOps
	failKind dbalance.Kind
	err      error
}

func (f failingOps) Insert(ctx context.Context, tx port.Tx, op dbalance.Operation) error {
	if op.Kind == f.failKind {
		return f.err
	}
	return f.fakeOps.Insert(ctx, tx, op)
}

// EC-65 for a transfer: the replay restores both legs.
func TestTransferReplay(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	first, err := w.transfer("adminA", "u1", "u2", "60", "k1")
	require.NoError(t, err)
	second, err := w.transfer("adminA", "u1", "u2", "60", "k1")
	require.NoError(t, err, "a replay must not hit insufficient_funds")
	assert.True(t, second.Replayed)
	assert.Equal(t, first.TransferID, second.TransferID)
	assert.Equal(t, first.Out, second.Out)
	assert.Equal(t, first.In, second.In)
	assert.Len(t, w.s.ops, 2)
	assert.Equal(t, money.Amount(40), w.amount("u1"))
	assert.Equal(t, money.Amount(60), w.amount("u2"))
	assert.Len(t, w.audit.entries, 2)
}

// EC-66, EC-69 for a transfer: another body, another direction, another route.
func TestTransferKeyReuse(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	_, err := w.transfer("adminA", "u1", "u2", "10", "k1")
	require.NoError(t, err)
	_, err = w.transfer("adminA", "u1", "u2", "20", "k1")
	requireCode(t, err, apperr.IdempotencyKeyReused)

	// Same hash, other direction: the stored legs tell them apart.
	in := baseTransfer(w, "u1", "u2")
	in.IdempotencyKey = "k2"
	in.RequestHash = hash("fixed")
	_, err = newTransfer(w).Execute(t.Context(), in)
	require.NoError(t, err)
	rev := baseTransfer(w, "u2", "u1")
	rev.IdempotencyKey = "k2"
	rev.RequestHash = hash("fixed")
	_, err = newTransfer(w).Execute(t.Context(), rev)
	requireCode(t, err, apperr.IdempotencyKeyReused)

	// A credit key replayed as a transfer.
	_, err = w.credit("adminA", "u1", "5", "k3")
	require.NoError(t, err)
	c := baseTransfer(w, "u1", "u2")
	c.IdempotencyKey = "k3"
	c.RequestHash = hash("u1", "5", "k3")
	_, err = newTransfer(w).Execute(t.Context(), c)
	requireCode(t, err, apperr.IdempotencyKeyReused)
}

// EC-71 for a transfer.
func TestTransferFailedAttemptNotStored(t *testing.T) {
	w := newWorld(t)
	_, err := w.transfer("adminA", "u1", "u2", "10", "k1")
	requireCode(t, err, apperr.InsufficientFunds)
	assert.Empty(t, w.s.keys)
	w.fund("u1", 10)
	out, err := w.transfer("adminA", "u1", "u2", "10", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
}
