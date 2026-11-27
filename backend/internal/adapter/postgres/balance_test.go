//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	bal "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

func requireLedgerCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

func TestCreditDebitOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	require.Zero(t, w.countRows("balances"), "a node has no balance row until it is used")

	out, err := c.creditAs("adminA", "A1u1", "500", "k1")
	require.NoError(t, err)
	assert.Equal(t, money.Amount(500), out.Operation.BalanceAfter)
	assert.EqualValues(t, 500, w.balanceOf("A1u1"))
	out, err = c.debitAs("adminA", "A1u1", "120", "k2")
	require.NoError(t, err)
	assert.Equal(t, money.Amount(380), out.Operation.BalanceAfter)
	assert.EqualValues(t, 380, w.balanceOf("A1u1"))

	var version int64
	require.NoError(t, w.db.Owner.QueryRow(context.Background(),
		`SELECT version FROM balances WHERE node_id = $1`, uuid.UUID(w.id("A1u1"))).Scan(&version))
	assert.EqualValues(t, 2, version)

	assert.Equal(t, []string{"balance.credited", "balance.debited"}, c.audit.actions())
	assert.Equal(t, "380", c.audit.entriesOf("balance.debited")[0].After["balance_after"])

	got, err := c.get.Execute(context.Background(), bal.GetBalanceInput{Actor: w.users["viewerA1u1"], NodeID: w.id("A1u1")})
	require.NoError(t, err)
	assert.Equal(t, money.Amount(380), got.Amount)
	_, err = c.get.Execute(context.Background(), bal.GetBalanceInput{Actor: w.users["adminB"], NodeID: w.id("A1u1")})
	requireLedgerCode(t, err, apperr.NotFound)

	// A node that never had an operation reads as zero.
	got, err = c.get.Execute(context.Background(), bal.GetBalanceInput{Actor: w.users["adminA"], NodeID: w.id("A1u2")})
	require.NoError(t, err)
	assert.Zero(t, got.Amount)
	w.requireLedgerInvariant("after credit and debit")
}

// Test-ID: T-I-72
// EC-74: a real shortfall is the 409 insufficient_funds raised by the use case.
// It must never surface as not_found (a database rejection mapped to 404), and
// the balance must never go below zero.
func TestShortfallIsNotNotFound(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "50", "k1")
	require.NoError(t, err)
	opsBefore := w.countRows("operations")

	_, err = c.debitAs("adminA", "A1u1", "51", "k2")
	requireLedgerCode(t, err, apperr.InsufficientFunds)
	_, err = c.transferAs("adminA", "A1u1", "A1u2", "51", "k3")
	requireLedgerCode(t, err, apperr.InsufficientFunds)
	assert.EqualValues(t, 50, w.balanceOf("A1u1"))
	assert.Equal(t, opsBefore, w.countRows("operations"))
	assert.NotContains(t, w.logs.String(), "storage rejected", "the database must not have been asked to refuse it")
	w.requireLedgerInvariant("after refused debits")
}

// Test-ID: T-I-08
// EC-18: a deactivated subtree blocks every operation, reactivation restores them.
func TestDeactivatedSubtreeBlocksOperations(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "100", "k0")
	require.NoError(t, err)
	_, err = c.creditAs("adminA", "A1u2", "100", "k00")
	require.NoError(t, err)

	w.setStatus("A1", "inactive")
	for i, f := range []func() error{
		func() error { _, err := c.creditAs("adminA", "A1u1", "10", "k1"); return err },
		func() error { _, err := c.debitAs("adminA", "A1u1", "10", "k2"); return err },
		func() error { _, err := c.transferAs("adminA", "A1u1", "A1u2", "10", "k3"); return err },
		func() error { _, err := c.transferAs("adminA", "A1u2", "A1u1", "10", "k4"); return err },
		func() error { _, err := c.creditAs("adminA", "A1", "10", "k5"); return err },
	} {
		requireLedgerCode(t, f(), apperr.NodeInactive)
		assert.EqualValues(t, 100, w.balanceOf("A1u1"), "case %d", i)
	}
	assert.Equal(t, 2, w.countRows("operations"))
	assert.Equal(t, 2, w.countRows("idempotency_keys"), "refused requests leave no key")

	w.setStatus("A1", "active")
	_, err = c.creditAs("adminA", "A1u1", "10", "k1")
	require.NoError(t, err)
	w.requireLedgerInvariant("after reactivation")
}

// Test-ID: T-I-73
// EC-81: settings above the actor's access root still gate the operation.
func TestLimitsFromHiddenAncestors(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	w.seedLimit("root", "max_operation_amount", `"100"`)
	_, err := c.creditAs("adminA", "A1u1", "101", "k1")
	requireLedgerCode(t, err, apperr.AmountExceedsLimit)
	_, err = c.creditAs("adminA", "A1u1", "100", "k2")
	require.NoError(t, err)

	// A lower limit lower in the chain wins; a higher one below cannot relax it.
	w.seedLimit("A1", "max_operation_amount", `"50"`)
	_, err = c.creditAs("adminA", "A1u1", "51", "k3")
	requireLedgerCode(t, err, apperr.AmountExceedsLimit)
	w.seedLimit("A1u1", "max_operation_amount", `"1000"`)
	_, err = c.creditAs("adminA", "A1u1", "51", "k4")
	requireLedgerCode(t, err, apperr.AmountExceedsLimit)
	_, err = c.creditAs("adminA", "A1u1", "50", "k5")
	require.NoError(t, err)

	w.seedLimit("root", "operations_enabled", `false`)
	_, err = c.creditAs("adminA", "A1u1", "1", "k6")
	requireLedgerCode(t, err, apperr.OperationsDisabled)
	_, err = c.debitAs("adminA", "A1u1", "1", "k7")
	requireLedgerCode(t, err, apperr.OperationsDisabled)
	_, err = c.transferAs("adminA", "A1u1", "A1u2", "1", "k8")
	requireLedgerCode(t, err, apperr.OperationsDisabled)
	assert.EqualValues(t, 150, w.balanceOf("A1u1"))
}

// Test-ID: T-I-74
// EC-50: a node of another branch, as either side of a transfer, is not_found
// and nothing is written.
func TestTransferAcrossBranchesIsNotFound(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "100", "k0")
	require.NoError(t, err)
	_, err = c.creditAs("adminB", "B1u1", "100", "k00")
	require.NoError(t, err)
	ops := w.countRows("operations")

	for i, f := range []func() error{
		func() error { _, err := c.transferAs("adminA", "A1u1", "B1u1", "10", "x1"); return err },
		func() error { _, err := c.transferAs("adminA", "B1u1", "A1u1", "10", "x2"); return err },
		func() error { _, err := c.transferAs("adminB", "A1u1", "B1u1", "10", "x3"); return err },
		func() error { _, err := c.creditAs("adminB", "A1u1", "10", "x4"); return err },
		func() error { _, err := c.debitAs("adminB", "A1u1", "10", "x5"); return err },
	} {
		requireLedgerCode(t, f(), apperr.NotFound)
		assert.Equal(t, ops, w.countRows("operations"), "case %d", i)
	}
	assert.EqualValues(t, 100, w.balanceOf("A1u1"))
	assert.EqualValues(t, 100, w.balanceOf("B1u1"))
	assert.Equal(t, 2, w.countRows("idempotency_keys"))
}

// The right is checked on every call: a viewer reads but cannot move money.
func TestViewerCannotOperate(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "100", "k0")
	require.NoError(t, err)
	_, err = c.debitAs("viewerA1u1", "A1u1", "10", "k1")
	requireLedgerCode(t, err, apperr.Forbidden)
	_, err = c.creditAs("viewerA1u1", "A1u1", "10", "k2")
	requireLedgerCode(t, err, apperr.Forbidden)
	_, err = c.transferAs("viewerA1u1", "A1u1", "A1u2", "10", "k3")
	requireLedgerCode(t, err, apperr.Forbidden)
	assert.EqualValues(t, 100, w.balanceOf("A1u1"))
	assert.Equal(t, 1, w.countRows("operations"))
}

// Test-ID: T-I-25
// EC-80: a failure on the second leg rolls back both legs, both balances and
// the idempotency key.
func TestTransferAtomic(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "100", "k0")
	require.NoError(t, err)
	ctx := context.Background()
	for _, q := range []string{
		`CREATE FUNCTION tb_test_fail_in() RETURNS trigger LANGUAGE plpgsql AS
		 $$ BEGIN IF NEW.kind = 'transfer_in' THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER tb_test_fail_in BEFORE INSERT ON operations FOR EACH ROW EXECUTE FUNCTION tb_test_fail_in()`,
	} {
		_, err := w.db.Owner.Exec(ctx, q)
		require.NoError(t, err)
	}
	keys := w.countRows("idempotency_keys")

	_, err = c.transferAs("adminA", "A1u1", "A1u2", "40", "k1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "injected failure")
	assert.EqualValues(t, 100, w.balanceOf("A1u1"))
	assert.EqualValues(t, 0, w.balanceOf("A1u2"))
	assert.Equal(t, 1, w.countRows("operations"), "the transfer_out leg must be rolled back")
	assert.Equal(t, keys, w.countRows("idempotency_keys"))
	assert.Equal(t, []string{"balance.credited"}, c.audit.actions()[:1])

	_, err = w.db.Owner.Exec(ctx, `DROP TRIGGER tb_test_fail_in ON operations`)
	require.NoError(t, err)
	out, err := c.transferAs("adminA", "A1u1", "A1u2", "40", "k1")
	require.NoError(t, err, "the key of the failed attempt was not stored")
	assert.False(t, out.Replayed)
	w.requireLedgerInvariant("after the retried transfer")
}

// Test-ID: T-I-68
// EC-82 (serial and light concurrent part): after a mixed series of credits,
// debits and transfers, for every node the balance equals the signed sum of its
// operations, balance_after follows the operations in time order, and the legs
// of each transfer share the transfer id and the amount.
func TestLedgerInvariantAfterMixedOperations(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	nodes := []string{"A1", "A1u1", "A1u2"}
	for i, n := range nodes {
		_, err := c.creditAs("adminA", n, "1000", fmt.Sprintf("seed-%d", i))
		require.NoError(t, err)
	}
	w.requireLedgerInvariant("after seeding")

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), 7))
			who := "adminA"
			if g%2 == 1 {
				who = "ownerRoot"
			}
			for i := range 25 {
				key := fmt.Sprintf("g%d-i%d", g, i)
				amount := fmt.Sprint(1 + rng.IntN(300))
				a, b := nodes[rng.IntN(3)], nodes[rng.IntN(3)]
				var err error
				switch rng.IntN(3) {
				case 0:
					_, err = c.creditAs(who, a, amount, key)
				case 1:
					_, err = c.debitAs(who, a, amount, key)
				default:
					if a == b {
						continue
					}
					_, err = c.transferAs(who, a, b, amount, key)
				}
				if err != nil {
					if code, ok := apperr.CodeOf(err); ok && code == apperr.InsufficientFunds {
						continue
					}
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	w.requireLedgerInvariant("after the concurrent series")

	var transfers int
	require.NoError(t, w.db.Owner.QueryRow(context.Background(),
		`SELECT count(DISTINCT transfer_id) FROM operations`).Scan(&transfers))
	assert.Positive(t, transfers, "the series should include transfers")
	var negative int
	require.NoError(t, w.db.Owner.QueryRow(context.Background(), `SELECT count(*) FROM balances WHERE amount < 0`).Scan(&negative))
	assert.Zero(t, negative)
}
