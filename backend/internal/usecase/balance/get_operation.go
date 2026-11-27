package balance

import (
	"context"

	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// GetOperationInput is the request of UC-B6.
type GetOperationInput struct {
	Actor       user.ActorID
	OperationID dbalance.OperationID
}

// Validate checks the input without touching the store.
func (in GetOperationInput) Validate() error {
	if in.OperationID == (dbalance.OperationID{}) {
		return validation("operation_id", "required", "operation_id is required")
	}
	return nil
}

// GetOperationOutput is one ledger record.
type GetOperationOutput struct {
	Operation dbalance.Operation
}

// GetOperation is UC-B6. It is read only.
type GetOperation struct{ d Deps }

// NewGetOperation builds the use case.
func NewGetOperation(d Deps) *GetOperation { return &GetOperation{d: d} }

// Execute returns one operation. An operation on a node outside the actor's
// subtrees is not_found, like a missing one; operation.read is checked on the
// node of the operation on every call.
func (uc *GetOperation) Execute(ctx context.Context, in GetOperationInput) (GetOperationOutput, error) {
	if err := in.Validate(); err != nil {
		return GetOperationOutput{}, err
	}
	d := uc.d
	var out GetOperationOutput
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		op, err := d.Ops.Get(ctx, tx, in.OperationID)
		if err != nil {
			return err
		}
		if err := d.require(ctx, tx, in.Actor, op.NodeID, role.OperationRead); err != nil {
			return err
		}
		out = GetOperationOutput{Operation: op}
		return nil
	})
	if err != nil {
		return GetOperationOutput{}, err
	}
	return out, nil
}
