package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Limits are the effective settings that gate a balance operation on a node.
type Limits struct {
	// OperationsEnabled is the effective operations_enabled (AND over the chain
	// from the root to the node).
	OperationsEnabled bool
	// MaxOperationAmount is the effective max_operation_amount (minimum over
	// the chain).
	MaxOperationAmount money.Amount
}

// LimitSource resolves the effective limits of a node, including settings of
// ancestors the actor cannot see.
type LimitSource interface {
	EffectiveLimits(ctx context.Context, tx Tx, id node.ID) (Limits, error)
}

// BalanceRepo stores balances. Rows are created lazily: a node without a row
// has the balance zero.
type BalanceRepo interface {
	// Get returns the balance of a node without locking; a node without a row
	// has a zero balance (Version 0, zero UpdatedAt).
	Get(ctx context.Context, tx Tx, id node.ID) (balance.Balance, error)
	// LockForUpdate creates the row when it is absent, then locks it until the
	// end of the transaction and returns the current balance. Use cases lock
	// several balances in ascending node id order.
	LockForUpdate(ctx context.Context, tx Tx, id node.ID, now time.Time) (balance.Balance, error)
	// Save stores amount, version and updated_at of a locked balance.
	Save(ctx context.Context, tx Tx, b balance.Balance) error
}

// OperationRepo stores the ledger. Records are never changed.
type OperationRepo interface {
	Insert(ctx context.Context, tx Tx, op balance.Operation) error
	// Get returns an operation the actor can see; otherwise apperr.NotFound.
	Get(ctx context.Context, tx Tx, id balance.OperationID) (balance.Operation, error)
	// GetTransfer returns both legs of a transfer the actor can see. Both legs
	// must be visible, otherwise apperr.NotFound.
	GetTransfer(ctx context.Context, tx Tx, id balance.TransferID) (out, in balance.Operation, err error)
	// List returns the operations of nodes inside the subtree of
	// f.ScopeNodeID that the actor can see, newest first by
	// (created_at DESC, id DESC), starting after the cursor. It returns at
	// most limit records.
	List(ctx context.Context, tx Tx, f OperationFilter, after *OperationCursor, limit int) ([]OperationRecord, error)
}

// OperationFilter selects the operations of UC-B5. Only nodes inside the
// subtree of ScopeNodeID are searched. The filters combine with AND; Kinds
// combine with OR.
type OperationFilter struct {
	ScopeNodeID node.ID
	NodeID      *node.ID
	Kinds       []balance.Kind
	// From is inclusive, To exclusive.
	From, To *time.Time
}

// OperationCursor is the keyset position after which the next page starts.
type OperationCursor struct {
	CreatedAt time.Time
	ID        balance.OperationID
}

// OperationRecord is an operation with the name of its node.
type OperationRecord struct {
	Operation balance.Operation
	NodeName  string
}

// IdempotencyRecord is a stored idempotency key. Exactly one of OperationID
// and TransferID is set.
type IdempotencyRecord struct {
	UserID      user.ID
	Key         string
	RequestHash []byte
	OperationID *balance.OperationID
	TransferID  *balance.TransferID
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// IdempotencyRepo stores idempotency keys in the transaction of the operation.
type IdempotencyRepo interface {
	// Claim stores rec unless a live key of the same user and text exists. A
	// concurrent claim of the same key waits for the other transaction to end:
	// when that one commits the result is the stored row, when it rolls back
	// this claim succeeds. An expired row (ExpiresAt <= now) is replaced.
	// claimed is true when rec was stored; otherwise existing is the live row.
	Claim(ctx context.Context, tx Tx, rec IdempotencyRecord, now time.Time) (claimed bool, existing IdempotencyRecord, err error)
}
