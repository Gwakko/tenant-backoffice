//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	bal "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// freshUnit adds a unit under A1 whose balance row does not exist yet.
func (w *world) freshUnit(key string) {
	w.t.Helper()
	w.addNode(context.Background(), key, node.TypeUnit, "A1")
}

func (w *world) countWhere(table, where string, args ...any) int {
	w.t.Helper()
	var n int
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&n))
	return n
}

func isInsufficient(err error) bool {
	code, ok := apperr.CodeOf(err)
	return ok && code == apperr.InsufficientFunds
}

// isDeadlock recognises SQLSTATE 40P01 anywhere in the error chain.
func isDeadlock(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "40P01"
}

// Test-ID: T-I-23
// EC-73: two parallel debits of 70 against 100 give exactly one success and
// one insufficient_funds; a wider variant admits exactly as many debits as the
// balance covers. The balance never goes below zero.
func TestParallelDebitsLowBalance(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	for i := 0; i < repeats; i++ {
		node1 := fmt.Sprintf("low%d", i)
		w.freshUnit(node1)
		_, err := c.creditAs("adminA", node1, "100", fmt.Sprintf("seed-%d", i))
		require.NoError(t, err)

		errs := parallel(
			func() error { _, err := c.debitAs("adminA", node1, "70", fmt.Sprintf("a-%d", i)); return err },
			func() error { _, err := c.debitAs("adminA", node1, "70", fmt.Sprintf("b-%d", i)); return err },
		)
		ok, short := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case isInsufficient(err):
				short++
			default:
				t.Fatalf("iteration %d: unexpected error: %v", i, err)
			}
		}
		require.Equal(t, []int{1, 1}, []int{ok, short}, "iteration %d", i)
		require.EqualValues(t, 30, w.balanceOf(node1), "iteration %d", i)
		require.Equal(t, 1, w.countWhere("operations", "node_id = $1 AND kind = 'debit'", w.id(node1).String()), "iteration %d", i)

		// Eight debits of 30 against 100: three fit.
		wide := fmt.Sprintf("wide%d", i)
		w.freshUnit(wide)
		_, err = c.creditAs("adminA", wide, "100", fmt.Sprintf("wseed-%d", i))
		require.NoError(t, err)
		fns := make([]func() error, 8)
		for g := range fns {
			fns[g] = func() error { _, err := c.debitAs("adminA", wide, "30", fmt.Sprintf("w%d-%d", g, i)); return err }
		}
		ok = 0
		for _, err := range parallel(fns...) {
			switch {
			case err == nil:
				ok++
			case isInsufficient(err):
			default:
				t.Fatalf("iteration %d: unexpected error: %v", i, err)
			}
		}
		require.Equal(t, 3, ok, "iteration %d", i)
		require.EqualValues(t, 10, w.balanceOf(wide), "iteration %d", i)
		w.requireLedgerInvariant(fmt.Sprint("debits, iteration ", i))
	}
}

// meeting makes two transactions overlap between their first and second
// balance lock. A transaction that took its first lock waits until the other
// one has taken its own first lock too, or until some backend of the database
// waits for a row lock (the other transaction is queued behind the first lock, as
// with a consistent lock order), or until the cap passes.
type meeting struct {
	w       *world
	arrived atomic.Int32
}

func (m *meeting) wait() {
	m.arrived.Add(1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.arrived.Load() >= 2 {
			return
		}
		var waiting int
		_ = m.w.db.Owner.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks q JOIN pg_locks t ON t.pid = q.pid
			  WHERE q.locktype = 'transactionid' AND NOT q.granted
			    AND t.locktype = 'tuple' AND t.database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&waiting)
		if waiting > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// gatedBalances pauses its transaction after the first balance lock.
type gatedBalances struct {
	port.BalanceRepo
	m     *meeting
	locks int
}

func (g *gatedBalances) LockForUpdate(ctx context.Context, tx port.Tx, id node.ID, now time.Time) (dbalance.Balance, error) {
	b, err := g.BalanceRepo.LockForUpdate(ctx, tx, id, now)
	if err == nil {
		g.locks++
		if g.locks == 1 {
			g.m.wait()
		}
	}
	return b, err
}

// Test-ID: T-I-24
// EC-79: transfers A->B and B->A at the same moment never deadlock because the
// balances are locked in ascending id order. A test gate between the first and
// the second lock forces the two transactions to overlap there, so a lock
// order that follows the parameters deadlocks (SQLSTATE 40P01) on every
// repetition instead of now and then. The context timeout turns a hang into a
// failure.
func TestParallelOppositeTransfersNoDeadlock(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	_, err := c.creditAs("adminA", "A1u1", "1000", "seed-1")
	require.NoError(t, err)
	_, err = c.creditAs("adminA", "A1u2", "1000", "seed-2")
	require.NoError(t, err)

	for i := 0; i < repeats; i++ {
		m := &meeting{w: w}
		gated := func() *bal.Transfer {
			d := c.deps
			d.Balances = &gatedBalances{BalanceRepo: c.deps.Balances, m: m}
			return bal.NewTransfer(d)
		}
		ab, ba := gated(), gated()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		run := func(uc *bal.Transfer, from, to, key string) func() error {
			return func() error {
				_, err := uc.Execute(ctx, bal.TransferInput{
					Actor: w.users["adminA"], RequestID: "t", FromNodeID: w.id(from), ToNodeID: w.id(to),
					Amount: "10", IdempotencyKey: key,
					RequestHash: ledgerHash("transfers", w.id(from).String(), w.id(to).String(), "10", key),
				})
				return err
			}
		}
		errs := parallel(
			run(ab, "A1u1", "A1u2", fmt.Sprintf("ab-%d", i)),
			run(ba, "A1u2", "A1u1", fmt.Sprintf("ba-%d", i)),
		)
		cancel()
		for _, err := range errs {
			require.False(t, isDeadlock(err), "iteration %d: deadlock (40P01): %v", i, err)
			require.NoError(t, err, "iteration %d", i)
		}
		require.EqualValues(t, 1000, w.balanceOf("A1u1"), "iteration %d", i)
		require.EqualValues(t, 1000, w.balanceOf("A1u2"), "iteration %d", i)
		w.requireLedgerInvariant(fmt.Sprint("opposite transfers, iteration ", i))
	}
	assert.Equal(t, 2*repeats, w.countWhere("operations", "kind = 'transfer_out'"))
}

// TestParallelDuplicateKeys: parallel requests with one idempotency key give
// one operation; the others replay it (EC-67 on the ledger level).
func TestParallelDuplicateKeys(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	for i := 0; i < repeats; i++ {
		n := fmt.Sprintf("dup%d", i)
		w.freshUnit(n)
		_, err := c.creditAs("adminA", n, "1000", fmt.Sprintf("seed-%d", i))
		require.NoError(t, err)

		key := fmt.Sprintf("same-%d", i)
		outs := make([]bal.OperateOutput, 6)
		fns := make([]func() error, len(outs))
		for g := range fns {
			fns[g] = func() (err error) { outs[g], err = c.debitAs("adminA", n, "100", key); return err }
		}
		for g, err := range parallel(fns...) {
			require.NoError(t, err, "iteration %d, caller %d", i, g)
		}
		replays := 0
		for _, o := range outs {
			require.Equal(t, outs[0].Operation.ID, o.Operation.ID, "iteration %d: every caller sees the same operation", i)
			if o.Replayed {
				replays++
			}
		}
		require.Equal(t, len(outs)-1, replays, "iteration %d", i)
		require.Equal(t, 1, w.countWhere("operations", "node_id = $1 AND kind = 'debit'", w.id(n).String()), "iteration %d", i)
		require.EqualValues(t, 900, w.balanceOf(n), "iteration %d", i)
		w.requireLedgerInvariant(fmt.Sprint("duplicates, iteration ", i))
	}
}

// TestParallelFirstLockRace: the balance row of a node is created at the
// first lock; parallel first actions on a node without a row neither fail nor
// lose an operation.
func TestParallelFirstLockRace(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	for i := 0; i < repeats; i++ {
		n := fmt.Sprintf("first%d", i)
		w.freshUnit(n)
		require.Zero(t, w.countWhere("balances", "node_id = $1", w.id(n).String()))

		fns := make([]func() error, 4)
		for g := range fns {
			fns[g] = func() error { _, err := c.creditAs("adminA", n, "10", fmt.Sprintf("f%d-%d", g, i)); return err }
		}
		for g, err := range parallel(fns...) {
			require.NoError(t, err, "iteration %d, caller %d", i, g)
		}
		require.EqualValues(t, 40, w.balanceOf(n), "iteration %d", i)
		require.Equal(t, 4, w.countWhere("operations", "node_id = $1", w.id(n).String()), "iteration %d", i)
		require.Equal(t, 1, w.countWhere("balances", "node_id = $1", w.id(n).String()), "iteration %d", i)
		w.requireLedgerInvariant(fmt.Sprint("first lock, iteration ", i))
	}
}

// Test-ID: T-I-26
// EC-82: after a parallel run of credits, debits, transfers (opposite ones
// among them) and repeated keys, for every node the balance equals the signed
// sum of its operations, balance_after follows the chain and the legs of a
// transfer are paired. Checked after every repetition.
func TestParallelLedgerInvariant(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	nodes := []string{"A1", "A1u1", "A1u2"}
	for i, n := range nodes {
		_, err := c.creditAs("adminA", n, "500", fmt.Sprintf("seed-%d", i))
		require.NoError(t, err)
	}
	for rep := 0; rep < repeats; rep++ {
		fns := make([]func() error, 6)
		for g := range fns {
			fns[g] = func() error {
				rng := rand.New(rand.NewPCG(uint64(g), uint64(rep)))
				for i := range 6 {
					key := fmt.Sprintf("r%d-g%d-i%d", rep, g, i)
					amount := fmt.Sprint(1 + rng.IntN(200))
					a, b := nodes[rng.IntN(3)], nodes[rng.IntN(3)]
					var err error
					switch rng.IntN(4) {
					case 0:
						_, err = c.creditAs("adminA", a, amount, key)
					case 1:
						_, err = c.debitAs("adminA", a, amount, key)
					case 2:
						// A repeated key: both callers may run at once.
						if _, err = c.creditAs("adminA", a, amount, "dup-"+key); err == nil {
							_, err = c.creditAs("adminA", a, amount, "dup-"+key)
						}
					default:
						if a == b {
							continue
						}
						_, err = c.transferAs("adminA", a, b, amount, key)
					}
					if err != nil && !isInsufficient(err) {
						return err
					}
				}
				return nil
			}
		}
		for g, err := range parallel(fns...) {
			require.NoError(t, err, "repetition %d, worker %d", rep, g)
		}
		w.requireLedgerInvariant(fmt.Sprint("repetition ", rep))
		require.Zero(t, w.countWhere("balances", "amount < 0"), "repetition %d", rep)
	}
}

// Test-ID: T-I-28
// EC-84: a transfer into a node while that node is moved to another branch
// succeeds together with the move; the balance and the ledger stay consistent
// and the node ends up under its new parent.
func TestParallelTransferDuringMove(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	nc := w.useCases()
	_, err := c.creditAs("ownerRoot", "A1u2", "10000", "seed")
	require.NoError(t, err)

	for i := 0; i < repeats; i++ {
		require.NoError(t, w.moveAs(nc, "ownerRoot", "A1u1", "A1"))
		before := w.balanceOf("A1u1")
		errs := parallel(
			func() error { return w.moveAs(nc, "ownerRoot", "A1u1", "B1") },
			func() error { _, err := c.transferAs("ownerRoot", "A1u2", "A1u1", "10", fmt.Sprintf("t-%d", i)); return err },
		)
		require.NoError(t, errs[0], "iteration %d: move", i)
		require.NoError(t, errs[1], "iteration %d: transfer", i)
		require.EqualValues(t, before+10, w.balanceOf("A1u1"), "iteration %d", i)
		_, parent := w.pathOf(w.id("A1u1"))
		require.Equal(t, w.id("B1").String(), *parent, "iteration %d", i)
		w.requireConsistent(fmt.Sprint("iteration ", i))
		w.requireLedgerInvariant(fmt.Sprint("move, iteration ", i))
	}
}
