package balance_test

import (
	"math/big"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

var now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got)
}

func bal(amount money.Amount) balance.Balance {
	return balance.Balance{NodeID: node.ID(uuid.New()), Amount: amount, Version: 3}
}

// Test-ID: T-U-22
func TestInsufficientFunds(t *testing.T) {
	// EC-74: balance 50, debit 51 -> rejected, state untouched.
	b := bal(50)
	for _, k := range []balance.Kind{balance.Debit, balance.TransferOut} {
		_, err := b.Apply(k, 51, now)
		requireCode(t, err, apperr.InsufficientFunds)
	}
	require.Equal(t, money.Amount(50), b.Amount)
	require.Equal(t, int64(3), b.Version)

	// Exactly the whole balance is allowed and leaves zero, never below.
	got, err := b.Apply(balance.Debit, 50, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(0), got.Amount)
	require.Equal(t, int64(4), got.Version)
	require.Equal(t, now, got.UpdatedAt)
	_, err = got.Apply(balance.Debit, 1, now)
	requireCode(t, err, apperr.InsufficientFunds)

	// No partial debits: the failed call returns the zero Balance.
	failed, err := b.Apply(balance.Debit, 51, now)
	require.Error(t, err)
	require.Equal(t, balance.Balance{}, failed)
}

func TestApplyKinds(t *testing.T) {
	b := bal(100)
	got, err := b.Apply(balance.Credit, 25, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(125), got.Amount)
	got, err = b.Apply(balance.TransferIn, 25, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(125), got.Amount)
	got, err = b.Apply(balance.Debit, 25, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(75), got.Amount)
	got, err = b.Apply(balance.TransferOut, 25, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(75), got.Amount)
	require.Equal(t, b.NodeID, got.NodeID)

	_, err = b.Apply(balance.Credit, 0, now)
	requireCode(t, err, apperr.AmountMustBePositive)
	_, err = b.Apply(balance.Debit, -5, now)
	requireCode(t, err, apperr.AmountMustBePositive)
	_, err = b.Apply(balance.Kind("bogus"), 5, now)
	requireCode(t, err, apperr.ValidationFailed)

	// EC-77: overflow on credit.
	hi := bal(money.Max - 10)
	_, err = hi.Apply(balance.Credit, 11, now)
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = hi.Apply(balance.TransferIn, 11, now)
	requireCode(t, err, apperr.BalanceOverflow)
	got, err = hi.Apply(balance.Credit, 10, now)
	require.NoError(t, err)
	require.Equal(t, money.Max, got.Amount)
}

func TestKinds(t *testing.T) {
	for _, k := range balance.Kinds() {
		parsed, err := balance.ParseKind(string(k))
		require.NoError(t, err)
		require.Equal(t, k, parsed)
	}
	_, err := balance.ParseKind("refund")
	requireCode(t, err, apperr.ValidationFailed)
	require.True(t, balance.Credit.Increases())
	require.True(t, balance.TransferIn.Increases())
	require.False(t, balance.Debit.Increases())
	require.False(t, balance.TransferOut.Increases())
	require.True(t, balance.TransferOut.IsTransfer())
	require.False(t, balance.Credit.IsTransfer())
	require.False(t, balance.Kind("x").Valid())
}

func TestTransfer(t *testing.T) {
	a, b := bal(100), bal(money.Max-5)
	_, _, err := balance.Transfer(a, a, 10, now)
	requireCode(t, err, apperr.SameNode)

	na, nb, err := balance.Transfer(a, bal(7), 40, now)
	require.NoError(t, err)
	require.Equal(t, money.Amount(60), na.Amount)
	require.Equal(t, money.Amount(47), nb.Amount)

	// Either side failing yields no result for both.
	na, nb, err = balance.Transfer(a, b, 6, now)
	requireCode(t, err, apperr.BalanceOverflow)
	require.Equal(t, balance.Balance{}, na)
	require.Equal(t, balance.Balance{}, nb)
	_, _, err = balance.Transfer(a, bal(0), 101, now)
	requireCode(t, err, apperr.InsufficientFunds)
}

func TestLimitAndOperable(t *testing.T) {
	require.NoError(t, balance.CheckLimit(1000, 1000))
	requireCode(t, balance.CheckLimit(1001, 1000), apperr.AmountExceedsLimit)
	require.NoError(t, balance.CheckOperable(true, true))
	requireCode(t, balance.CheckOperable(false, true), apperr.NodeInactive)
	requireCode(t, balance.CheckOperable(true, false), apperr.OperationsDisabled)
	requireCode(t, balance.CheckOperable(false, false), apperr.NodeInactive)
}

func TestComment(t *testing.T) {
	c, err := balance.ParseComment("  top   up\t now ")
	require.NoError(t, err)
	require.Equal(t, "top up now", c.String())
	c, err = balance.ParseComment("")
	require.NoError(t, err)
	require.Equal(t, "", c.String())

	long := make([]rune, 200)
	for i := range long {
		long[i] = 'ж'
	}
	_, err = balance.ParseComment(string(long))
	require.NoError(t, err)
	_, err = balance.ParseComment(string(long) + "x")
	requireCode(t, err, apperr.ValidationFailed)
	for _, s := range []string{"a\x00b", "a\u0007b", "bad\xff"} {
		_, err = balance.ParseComment(s)
		requireCode(t, err, apperr.ValidationFailed)
	}
}

// Property (INV-L1, INV-L2): after any random sequence of operations, the
// balance equals the exact credits minus debits of the accepted ones, never
// negative, and Replay reproduces it.
func TestLedgerInvariant(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	kinds := balance.Kinds()
	for round := 0; round < 50; round++ {
		b := bal(0)
		var ops []balance.Operation
		exact := new(big.Int)
		for i := 0; i < 200; i++ {
			k := kinds[rng.IntN(len(kinds))]
			amt := money.Amount(1 + rng.Int64N(500))
			next, err := b.Apply(k, amt, now)
			if err != nil {
				requireCode(t, err, apperr.InsufficientFunds)
				require.False(t, k.Increases())
				continue
			}
			if k.Increases() {
				exact.Add(exact, big.NewInt(int64(amt)))
			} else {
				exact.Sub(exact, big.NewInt(int64(amt)))
			}
			b = next
			ops = append(ops, balance.Operation{Kind: k, Amount: amt, BalanceAfter: b.Amount})
			require.GreaterOrEqual(t, b.Amount.Minor(), int64(0))
			require.Equal(t, exact.Int64(), b.Amount.Minor())
		}
		got, err := balance.Replay(ops)
		require.NoError(t, err)
		require.Equal(t, b.Amount, got)
	}
}

func TestReplayErrors(t *testing.T) {
	_, err := balance.Replay([]balance.Operation{{Kind: balance.Debit, Amount: 1}})
	requireCode(t, err, apperr.InsufficientFunds)
	_, err = balance.Replay([]balance.Operation{{Kind: balance.Credit, Amount: money.Max}, {Kind: balance.Credit, Amount: 1}})
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = balance.Replay([]balance.Operation{{Kind: "x", Amount: 1}})
	requireCode(t, err, apperr.ValidationFailed)
	got, err := balance.Replay(nil)
	require.NoError(t, err)
	require.Equal(t, money.Amount(0), got)
}
