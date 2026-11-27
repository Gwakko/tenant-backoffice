//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	bal "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// Test-ID: T-I-70
// EC-65: the same key and body return the stored operation.
func TestReplaySameBodyOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	first, err := c.creditAs("adminA", "A1u1", "500", "k1")
	require.NoError(t, err)
	ops, audits := w.countRows("operations"), len(c.audit.entries)

	second, err := c.creditAs("adminA", "A1u1", "500", "k1")
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.Operation, second.Operation, "the output is restored from the stored operation")
	assert.Equal(t, ops, w.countRows("operations"))
	assert.Len(t, c.audit.entries, audits)
	assert.EqualValues(t, 500, w.balanceOf("A1u1"))

	// A debit that would no longer fit still replays instead of failing.
	_, err = c.debitAs("adminA", "A1u1", "400", "d1")
	require.NoError(t, err)
	again, err := c.debitAs("adminA", "A1u1", "400", "d1")
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.EqualValues(t, 100, w.balanceOf("A1u1"), "no double debit")

	// A transfer replays both legs.
	tr, err := c.transferAs("adminA", "A1u1", "A1u2", "60", "t1")
	require.NoError(t, err)
	tr2, err := c.transferAs("adminA", "A1u1", "A1u2", "60", "t1")
	require.NoError(t, err)
	assert.True(t, tr2.Replayed)
	assert.Equal(t, tr.TransferID, tr2.TransferID)
	assert.Equal(t, tr.Out, tr2.Out)
	assert.Equal(t, tr.In, tr2.In)
	assert.EqualValues(t, 40, w.balanceOf("A1u1"))
	assert.EqualValues(t, 60, w.balanceOf("A1u2"))
	w.requireLedgerInvariant("after replays")
}

// Test-ID: T-I-71
// EC-66, EC-69: the same key for another body or route is refused; nothing is
// written by the refused call.
func TestReuseDifferentBodyOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "100", "k1")
	require.NoError(t, err)
	ops := w.countRows("operations")

	_, err = c.creditAs("adminA", "A1u1", "200", "k1")
	requireLedgerCode(t, err, apperr.IdempotencyKeyReused)
	_, err = c.debitAs("adminA", "A1u1", "100", "k1")
	requireLedgerCode(t, err, apperr.IdempotencyKeyReused)
	_, err = c.creditAs("adminA", "A1u2", "100", "k1")
	requireLedgerCode(t, err, apperr.IdempotencyKeyReused)
	_, err = c.transferAs("adminA", "A1u1", "A1u2", "100", "k1")
	requireLedgerCode(t, err, apperr.IdempotencyKeyReused)
	assert.Equal(t, ops, w.countRows("operations"))
	assert.EqualValues(t, 100, w.balanceOf("A1u1"))
	assert.EqualValues(t, 0, w.balanceOf("A1u2"))
}

// Test-ID: T-I-19
// EC-67: parallel requests with one key: one operation, the loser waits on the
// unique index and replays the winner's result.
func TestConcurrentSameKey(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	for round := range 20 {
		key := fmt.Sprintf("race-%d", round)
		const n = 6
		var wg sync.WaitGroup
		start := make(chan struct{})
		outs := make([]bal.OperateOutput, n)
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				outs[i], errs[i] = c.creditAs("adminA", "A1u1", "10", key)
			}()
		}
		close(start)
		wg.Wait()
		replayed := 0
		for i := range n {
			require.NoError(t, errs[i])
			assert.Equal(t, outs[0].Operation, outs[i].Operation, "every caller gets the winner's operation")
			if outs[i].Replayed {
				replayed++
			}
		}
		assert.Equal(t, n-1, replayed, "exactly one caller performs the operation")
		assert.EqualValues(t, 10*(round+1), w.balanceOf("A1u1"))
	}
	assert.Equal(t, 20, w.countRows("operations"))
	w.requireLedgerInvariant("after the key races")
}

// Test-ID: T-I-20
// EC-68: the same key text of two users gives two independent operations.
func TestKeyScopedPerUser(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	a, err := c.creditAs("adminA", "A1u1", "10", "shared")
	require.NoError(t, err)
	b, err := c.creditAs("ownerRoot", "A1u1", "10", "shared")
	require.NoError(t, err)
	assert.False(t, a.Replayed)
	assert.False(t, b.Replayed)
	assert.NotEqual(t, a.Operation.ID, b.Operation.ID)
	assert.EqualValues(t, 20, w.balanceOf("A1u1"))
	assert.Equal(t, 2, w.countRows("idempotency_keys"))
}

// Test-ID: T-I-21
// EC-71: a first attempt that failed stored no key; the retry runs.
func TestFailedAttemptNotStored(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.debitAs("adminA", "A1u1", "10", "k1")
	requireLedgerCode(t, err, apperr.InsufficientFunds)
	assert.Zero(t, w.countRows("idempotency_keys"))
	assert.Zero(t, w.countRows("operations"))

	_, err = c.creditAs("adminA", "A1u1", "10", "fund")
	require.NoError(t, err)
	out, err := c.debitAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.EqualValues(t, 0, w.balanceOf("A1u1"))
}

// Test-ID: T-I-22
// EC-72: a key older than 24 hours is replaced and the request runs again.
func TestExpiredKeyReplaced(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	first, err := c.creditAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)

	c.clock.advance(23 * time.Hour)
	again, err := c.creditAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)
	assert.True(t, again.Replayed, "still live after 23 hours")

	c.clock.advance(time.Hour + time.Second)
	out, err := c.creditAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)
	assert.False(t, out.Replayed)
	assert.NotEqual(t, first.Operation.ID, out.Operation.ID)
	assert.EqualValues(t, 20, w.balanceOf("A1u1"))
	assert.Equal(t, 1, w.countRows("idempotency_keys"), "the expired row was replaced, not added")

	// An expired key is replaced even for another body.
	c.clock.advance(25 * time.Hour)
	_, err = c.creditAs("adminA", "A1u1", "30", "k1")
	require.NoError(t, err)
	assert.EqualValues(t, 50, w.balanceOf("A1u1"))
}

// Test-ID: T-I-69
// A replay after the user lost access to the node is not_found: the right is
// checked before the key is looked up, and row-level security would hide the
// stored operation anyway. The stored operation is untouched.
func TestReplayAfterAccessLost(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)

	_, err = w.db.Owner.Exec(context.Background(),
		`DELETE FROM role_assignments WHERE user_id = $1`, uuid.UUID(w.users["adminA"]))
	require.NoError(t, err)
	_, err = c.creditAs("adminA", "A1u1", "10", "k1")
	requireLedgerCode(t, err, apperr.NotFound)
	_, err = c.transferAs("adminA", "A1u1", "A1u2", "10", "k1")
	requireLedgerCode(t, err, apperr.NotFound)
	assert.Equal(t, 1, w.countRows("operations"))
	assert.EqualValues(t, 10, w.balanceOf("A1u1"))
}
