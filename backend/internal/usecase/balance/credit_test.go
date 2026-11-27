package balance_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
)

func TestCreditAndDebit(t *testing.T) {
	w := newWorld(t)
	out, err := w.credit("adminA", "A1", "500", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.Equal(t, dbalance.Credit, out.Operation.Kind)
	assert.Equal(t, money.Amount(500), out.Operation.BalanceAfter)
	assert.Equal(t, money.Amount(500), w.amount("A1"))
	assert.Equal(t, int64(1), w.s.balances[w.id("A1")].Version)

	out, err = w.debit("adminA", "A1", "120", "k2")
	require.NoError(t, err)
	assert.Equal(t, money.Amount(380), out.Operation.BalanceAfter)
	assert.Equal(t, money.Amount(380), w.amount("A1"))
	require.Len(t, w.s.ops, 2)

	require.Len(t, w.audit.entries, 2)
	assert.Equal(t, "balance.credited", w.audit.entries[0].Action)
	assert.Equal(t, "balance.debited", w.audit.entries[1].Action)
	assert.Equal(t, "380", w.audit.entries[1].After["balance_after"])
	assert.Equal(t, "120", w.audit.entries[1].After["amount"])
	assert.NotEmpty(t, w.audit.entries[1].After["operation_id"])
	assert.Equal(t, w.id("A1"), w.audit.entries[1].NodeID)
}

// EC-74: a real shortfall is insufficient_funds (409), never not_found, and the
// balance never goes below zero.
func TestDebitInsufficientFunds(t *testing.T) {
	w := newWorld(t)
	w.fund("A1", 50)
	before := w.snapshot()
	_, err := w.debit("adminA", "A1", "51", "k1")
	requireCode(t, err, apperr.InsufficientFunds)
	w.untouched(before)
	assert.Equal(t, money.Amount(50), w.amount("A1"))

	// Exactly the balance is allowed and leaves zero.
	out, err := w.debit("adminA", "A1", "50", "k2")
	require.NoError(t, err)
	assert.Equal(t, money.Amount(0), out.Operation.BalanceAfter)
}

// EC-77: overflow at the top of int64.
func TestCreditOverflow(t *testing.T) {
	w := newWorld(t)
	w.lim.max[w.id("A1")] = money.Max
	w.fund("A1", money.Max-10)
	before := w.snapshot()
	_, err := w.credit("adminA", "A1", "11", "k1")
	requireCode(t, err, apperr.BalanceOverflow)
	w.untouched(before)
	_, err = w.credit("adminA", "A1", "10", "k2")
	require.NoError(t, err)
}

// EC-75, EC-76: amount validation happens before any store access.
func TestAmountValidation(t *testing.T) {
	w := newWorld(t)
	before := w.snapshot()
	for raw, code := range map[string]apperr.Code{
		"0": apperr.AmountMustBePositive, "-5": apperr.InvalidAmount, "10.5": apperr.InvalidAmount,
		"1e3": apperr.InvalidAmount, "007": apperr.InvalidAmount, "9223372036854775808": apperr.InvalidAmount,
		"": apperr.InvalidAmount,
	} {
		_, err := w.credit("adminA", "A1", raw, "k")
		requireCode(t, err, code)
	}
	w.untouched(before)
}

// Test-ID: T-U-24
// EC-77: the effective max_operation_amount.
func TestLimitExceeded(t *testing.T) {
	w := newWorld(t)
	w.lim.max[w.id("A1")] = 1000
	before := w.snapshot()
	_, err := w.credit("adminA", "A1", "1001", "k1")
	requireCode(t, err, apperr.AmountExceedsLimit)
	_, err = w.debit("adminA", "A1", "1001", "k2")
	requireCode(t, err, apperr.AmountExceedsLimit)
	w.untouched(before)
	_, err = w.credit("adminA", "A1", "1000", "k3")
	require.NoError(t, err)
}

// Test-ID: T-U-09
// EC-18, EC-20: an inactive node or ancestor blocks operations.
func TestInactiveAncestorBlocks(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	w.deactivate("A1")
	before := w.snapshot()
	for _, node := range []string{"A1", "u1"} {
		_, err := w.credit("adminA", node, "10", "k1")
		requireCode(t, err, apperr.NodeInactive)
		_, err = w.debit("adminA", node, "10", "k2")
		requireCode(t, err, apperr.NodeInactive)
	}
	w.untouched(before)
}

// Test-ID: T-U-26
// EC-81: operations_disabled and node_inactive.
func TestOperationsDisabledOrInactive(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 100)
	w.lim.enabled[w.id("u1")] = false
	before := w.snapshot()
	_, err := w.credit("adminA", "u1", "10", "k1")
	requireCode(t, err, apperr.OperationsDisabled)
	_, err = w.debit("adminA", "u1", "10", "k2")
	requireCode(t, err, apperr.OperationsDisabled)
	w.untouched(before)

	w.lim.enabled[w.id("u1")] = true
	w.deactivate("u1")
	_, err = w.credit("adminA", "u1", "10", "k3")
	requireCode(t, err, apperr.NodeInactive)
	w.untouched(before)
}

// EC-65: the same key and request return the stored operation and write nothing.
func TestReplayReturnsStoredOperation(t *testing.T) {
	w := newWorld(t)
	first, err := w.credit("adminA", "A1", "500", "k1")
	require.NoError(t, err)
	audits, saves := len(w.audit.entries), w.s.saves

	// The clock and the balance moved on; the replay must not depend on them.
	w.clk.t = w.clk.t.Add(time.Hour)
	w.s.balances[w.id("A1")] = dbalance.Balance{NodeID: w.id("A1"), Amount: 9999, Version: 7}
	second, err := w.credit("adminA", "A1", "500", "k1")
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.Operation, second.Operation)
	assert.Len(t, w.s.ops, 1, "a replay creates no operation")
	assert.Equal(t, saves, w.s.saves, "a replay saves no balance")
	assert.Len(t, w.audit.entries, audits, "a replay writes no audit entry")
	assert.Equal(t, money.Amount(9999), w.amount("A1"))
}

func TestReplayDebitDoesNotDebitTwice(t *testing.T) {
	w := newWorld(t)
	w.fund("A1", 100)
	_, err := w.debit("adminA", "A1", "70", "k1")
	require.NoError(t, err)
	out, err := w.debit("adminA", "A1", "70", "k1")
	require.NoError(t, err, "the second call is a replay, not a second debit that would fail")
	assert.True(t, out.Replayed)
	assert.Equal(t, money.Amount(30), w.amount("A1"))
	assert.Len(t, w.s.ops, 1)
}

// EC-66: the same key with another request is refused.
func TestSameKeyOtherBody(t *testing.T) {
	w := newWorld(t)
	_, err := w.credit("adminA", "A1", "100", "k1")
	require.NoError(t, err)
	before := w.snapshot()
	in := w.in("adminA", "A1", "200", "k1")
	in.RequestHash = hash("another body")
	_, err = newCredit(w).Execute(t.Context(), in)
	requireCode(t, err, apperr.IdempotencyKeyReused)
	assert.Len(t, w.s.ops, 1)
	assert.Equal(t, before.balances, w.s.balances)
	assert.Equal(t, money.Amount(100), w.amount("A1"))
}

// EC-69 at the use case: a key stored by a credit does not replay as a debit
// of the same node (the hash of another route differs; the stored operation
// kind differs even if a hash collided).
func TestSameKeyOtherRoute(t *testing.T) {
	w := newWorld(t)
	w.fund("A1", 100)
	in := w.in("adminA", "A1", "10", "k1")
	_, err := newCredit(w).Execute(t.Context(), in)
	require.NoError(t, err)
	// A debit with the very same hash: only the stored kind tells them apart.
	_, err = newDebit(w).Execute(t.Context(), in)
	requireCode(t, err, apperr.IdempotencyKeyReused)
	assert.Equal(t, money.Amount(110), w.amount("A1"))
	// A debit with its own hash is refused too.
	_, err = w.debit("adminA", "A1", "10", "k1")
	requireCode(t, err, apperr.IdempotencyKeyReused)
	assert.Len(t, w.s.ops, 1)
}

// EC-68: the key belongs to the user.
func TestKeyScopedPerUser(t *testing.T) {
	w := newWorld(t)
	_, err := w.credit("adminA", "A1", "100", "same")
	require.NoError(t, err)
	out, err := w.credit("owner", "A1", "100", "same")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.Len(t, w.s.ops, 2)
	assert.Equal(t, money.Amount(200), w.amount("A1"))
}

// EC-71: a failed first attempt leaves no key; the retry runs.
func TestFailedAttemptNotStored(t *testing.T) {
	w := newWorld(t)
	_, err := w.debit("adminA", "A1", "10", "k1")
	requireCode(t, err, apperr.InsufficientFunds)
	assert.Empty(t, w.s.keys)
	_, err = w.credit("adminA", "A1", "10", "other")
	require.NoError(t, err)
	out, err := w.debit("adminA", "A1", "10", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
}

// EC-72: an expired key is replaced.
func TestExpiredKeyRunsAgain(t *testing.T) {
	w := newWorld(t)
	_, err := w.credit("adminA", "A1", "10", "k1")
	require.NoError(t, err)
	w.clk.t = w.clk.t.Add(24*time.Hour + time.Second)
	out, err := w.credit("adminA", "A1", "10", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.Equal(t, money.Amount(20), w.amount("A1"))
}

// EC-70: the key and the hash are validated before anything else.
func TestKeyValidation(t *testing.T) {
	w := newWorld(t)
	before := w.snapshot()
	for key, code := range map[string]apperr.Code{
		"": apperr.IdempotencyKeyRequired, strings.Repeat("k", 129): apperr.IdempotencyKeyInvalid,
		"has space": apperr.IdempotencyKeyInvalid, "a/b": apperr.IdempotencyKeyInvalid,
	} {
		_, err := w.credit("adminA", "A1", "10", key)
		requireCode(t, err, code)
	}
	in := w.in("adminA", "A1", "10", "k")
	in.RequestHash = []byte("short")
	_, err := newCredit(w).Execute(t.Context(), in)
	requireCode(t, err, apperr.ValidationFailed)
	w.untouched(before)
	_, err = w.credit("adminA", "A1", "10", strings.Repeat("k", 128))
	require.NoError(t, err)
}

func TestCommentValidation(t *testing.T) {
	w := newWorld(t)
	in := w.in("adminA", "A1", "10", "k1")
	in.Comment = strings.Repeat("x", 201)
	_, err := newCredit(w).Execute(t.Context(), in)
	requireCode(t, err, apperr.ValidationFailed)
	in.Comment = "  rent   for  May "
	out, err := newCredit(w).Execute(t.Context(), in)
	require.NoError(t, err)
	assert.Equal(t, "rent for May", out.Operation.Comment.String())
}

// EC-50 and the permission matrix: the right is checked on every call.
func TestAuthorization(t *testing.T) {
	w := newWorld(t)
	w.fund("A1", 100)
	before := w.snapshot()

	// Another branch and a user without a role: not_found, nothing leaks.
	_, err := w.credit("adminB", "A1", "10", "k1")
	requireCode(t, err, apperr.NotFound)
	_, err = w.debit("adminB", "A1", "10", "k2")
	requireCode(t, err, apperr.NotFound)
	_, err = w.credit("nobody", "A1", "10", "k3")
	requireCode(t, err, apperr.NotFound)

	// A visible node without the right: forbidden. A viewer holds
	// balance.read only, so neither credit nor debit passes.
	_, err = w.credit("viewerA", "A1", "10", "k4")
	requireCode(t, err, apperr.Forbidden)
	_, err = w.debit("viewerA", "A1", "10", "k5")
	requireCode(t, err, apperr.Forbidden)
	w.untouched(before)
	assert.Equal(t, money.Amount(100), w.amount("A1"))
}

// A replay after the user lost access to the node is not_found (the check of
// the right precedes the key lookup).
func TestReplayAfterAccessLost(t *testing.T) {
	w := newWorld(t)
	_, err := w.credit("adminA", "A1", "10", "k1")
	require.NoError(t, err)
	w.chk.grants[w.actors["adminA"]] = nil
	_, err = w.credit("adminA", "A1", "10", "k1")
	requireCode(t, err, apperr.NotFound)
	assert.Len(t, w.s.ops, 1)
}

func TestGetBalance(t *testing.T) {
	w := newWorld(t)
	out, err := newGetBalance(w).Execute(t.Context(), getIn(w, "viewerA", "A1"))
	require.NoError(t, err)
	assert.Equal(t, money.Amount(0), out.Amount)
	w.fund("A1", 42)
	out, err = newGetBalance(w).Execute(t.Context(), getIn(w, "viewerA", "A1"))
	require.NoError(t, err)
	assert.Equal(t, money.Amount(42), out.Amount)
	_, err = newGetBalance(w).Execute(t.Context(), getIn(w, "adminB", "A1"))
	requireCode(t, err, apperr.NotFound)
	_, err = newGetBalance(w).Execute(t.Context(), getIn(w, "nobody", "A1"))
	requireCode(t, err, apperr.NotFound)
}
