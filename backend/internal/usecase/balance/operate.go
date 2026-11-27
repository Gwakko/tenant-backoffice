package balance

import (
	"context"

	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// OperateInput is the request of UC-B1 (credit) and UC-B2 (debit).
// RequestHash is sha256 over the request, computed by the transport layer
// (spec 03 section 4).
type OperateInput struct {
	Actor          user.ActorID
	RequestID      string
	NodeID         dnode.ID
	Amount         string
	Comment        string
	IdempotencyKey string
	RequestHash    []byte
}

// OperateOutput is the stored operation; Replayed is true when it was
// restored for a repeated idempotency key.
type OperateOutput struct {
	Operation dbalance.Operation
	Replayed  bool
}

// Credit is UC-B1.
type Credit struct{ d Deps }

// NewCredit builds the use case.
func NewCredit(d Deps) *Credit { return &Credit{d: d} }

// Execute adds the amount to the balance of the node.
func (uc *Credit) Execute(ctx context.Context, in OperateInput) (OperateOutput, error) {
	return operate(ctx, uc.d, in, dbalance.Credit, role.BalanceCredit)
}

// Debit is UC-B2.
type Debit struct{ d Deps }

// NewDebit builds the use case.
func NewDebit(d Deps) *Debit { return &Debit{d: d} }

// Execute subtracts the amount from the balance of the node; a balance below
// the amount is apperr.InsufficientFunds and nothing is written.
func (uc *Debit) Execute(ctx context.Context, in OperateInput) (OperateOutput, error) {
	return operate(ctx, uc.d, in, dbalance.Debit, role.BalanceDebit)
}

func operate(ctx context.Context, d Deps, in OperateInput, kind dbalance.Kind, perm role.Permission) (OperateOutput, error) {
	if err := requireID("node_id", in.NodeID); err != nil {
		return OperateOutput{}, err
	}
	req, err := validateCommon(in.Amount, in.Comment, in.IdempotencyKey, in.RequestHash)
	if err != nil {
		return OperateOutput{}, err
	}
	var out OperateOutput
	err = d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, perm); err != nil {
			return err
		}
		opID := d.newOperationID()
		claimed, existing, err := d.claim(ctx, tx, port.IdempotencyRecord{
			UserID: in.Actor, Key: in.IdempotencyKey, RequestHash: in.RequestHash, OperationID: &opID,
		})
		if err != nil {
			return err
		}
		if !claimed {
			if existing.OperationID == nil {
				return reused()
			}
			op, err := d.Ops.Get(ctx, tx, *existing.OperationID)
			if err != nil {
				return err
			}
			if op.NodeID != in.NodeID || op.Kind != kind {
				return reused()
			}
			out = OperateOutput{Operation: op, Replayed: true}
			return nil
		}
		if err := d.checkNode(ctx, tx, in.NodeID, req.amount); err != nil {
			return err
		}
		bal, err := d.Balances.LockForUpdate(ctx, tx, in.NodeID, d.Clock.Now())
		if err != nil {
			return err
		}
		// The stamp is taken under the row lock, so the order of created_at on
		// one node is the order of balance changes.
		now := d.Clock.Now()
		next, err := bal.Apply(kind, req.amount, now)
		if err != nil {
			return err
		}
		op := dbalance.Operation{
			ID: opID, NodeID: in.NodeID, Kind: kind, Amount: req.amount, BalanceAfter: next.Amount,
			Comment: req.comment, ActorID: in.Actor, CreatedAt: now,
		}
		if err := d.Balances.Save(ctx, tx, next); err != nil {
			return err
		}
		if err := d.Ops.Insert(ctx, tx, op); err != nil {
			return err
		}
		action := "balance.credited"
		if kind == dbalance.Debit {
			action = "balance.debited"
		}
		if err := d.Audit.Write(ctx, tx, port.AuditEntry{
			Action: action, NodeID: in.NodeID, ActorID: in.Actor, RequestID: in.RequestID,
			After: map[string]string{
				"operation_id":  opUUID(opID).String(),
				"amount":        req.amount.String(),
				"balance_after": next.Amount.String(),
			},
		}); err != nil {
			return err
		}
		out = OperateOutput{Operation: op}
		return nil
	})
	if err != nil {
		return OperateOutput{}, err
	}
	return out, nil
}
