package balance

import (
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Balance is the current balance of one node. Amount is never negative (INV-L2).
type Balance struct {
	NodeID    node.ID
	Amount    money.Amount
	Version   int64
	UpdatedAt time.Time
}

// Apply returns the balance after one operation of the given kind and amount.
// The amount must be positive. Increasing kinds fail with balance_overflow
// instead of wrapping; decreasing kinds fail with insufficient_funds instead of
// going below zero (partial debits are not supported). On error the receiver is
// unchanged and nothing is to be recorded. Version grows by one and UpdatedAt is
// set to now.
func (b Balance) Apply(kind Kind, amount money.Amount, now time.Time) (Balance, error) {
	if !kind.Valid() {
		return Balance{}, apperr.New(apperr.ValidationFailed, "unknown operation kind")
	}
	if err := amount.Positive(); err != nil {
		return Balance{}, err
	}
	var next money.Amount
	var err error
	if kind.Increases() {
		next, err = b.Amount.Add(amount)
	} else {
		next, err = b.Amount.Sub(amount)
	}
	if err != nil {
		return Balance{}, err
	}
	b.Amount = next
	b.Version++
	b.UpdatedAt = now
	return b, nil
}

// Transfer applies a transfer of amount from one balance to another and returns
// both new balances. Both must succeed, otherwise neither is returned. A
// transfer to the same node is rejected with same_node.
func Transfer(from, to Balance, amount money.Amount, now time.Time) (newFrom, newTo Balance, err error) {
	if from.NodeID == to.NodeID {
		return Balance{}, Balance{}, apperr.New(apperr.SameNode, "source and destination must differ")
	}
	newFrom, err = from.Apply(TransferOut, amount, now)
	if err != nil {
		return Balance{}, Balance{}, err
	}
	newTo, err = to.Apply(TransferIn, amount, now)
	if err != nil {
		return Balance{}, Balance{}, err
	}
	return newFrom, newTo, nil
}

// CheckLimit rejects an amount above the effective max_operation_amount.
func CheckLimit(amount, limit money.Amount) error {
	if amount > limit {
		return apperr.New(apperr.AmountExceedsLimit, "amount exceeds the operation limit").
			WithField("amount", string(apperr.AmountExceedsLimit))
	}
	return nil
}

// CheckOperable rejects an operation on a node that is not effectively active
// (node_inactive) or whose effective operations_enabled setting is false
// (operations_disabled). For a transfer, call it for both nodes.
func CheckOperable(effectivelyActive, operationsEnabled bool) error {
	if !effectivelyActive {
		return apperr.New(apperr.NodeInactive, "node is inactive")
	}
	if !operationsEnabled {
		return apperr.New(apperr.OperationsDisabled, "operations are disabled for this node")
	}
	return nil
}
