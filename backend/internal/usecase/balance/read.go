package balance

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// GetBalanceInput is the request of UC-B4.
type GetBalanceInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
}

// GetBalanceOutput is the current balance of a node.
type GetBalanceOutput struct {
	NodeID    dnode.ID
	Amount    money.Amount
	UpdatedAt time.Time
}

// GetBalance is UC-B4.
type GetBalance struct{ d Deps }

// NewGetBalance builds the use case.
func NewGetBalance(d Deps) *GetBalance { return &GetBalance{d: d} }

// Execute returns the balance of the node. A node that never had an operation
// has the balance zero, stamped with the time the node was created.
func (uc *GetBalance) Execute(ctx context.Context, in GetBalanceInput) (GetBalanceOutput, error) {
	d := uc.d
	if err := requireID("node_id", in.NodeID); err != nil {
		return GetBalanceOutput{}, err
	}
	var out GetBalanceOutput
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.BalanceRead); err != nil {
			return err
		}
		n, err := d.Nodes.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		b, err := d.Balances.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		out = GetBalanceOutput{NodeID: in.NodeID, Amount: b.Amount, UpdatedAt: b.UpdatedAt}
		if b.UpdatedAt.IsZero() {
			out.UpdatedAt = n.CreatedAt
		}
		return nil
	})
	if err != nil {
		return GetBalanceOutput{}, err
	}
	return out, nil
}
