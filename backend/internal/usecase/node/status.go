package node

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// SetStatusInput is the request of UC-N4 and UC-N5.
type SetStatusInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
}

// Validate checks the input without touching the store.
func (in SetStatusInput) Validate() error { return requireID("node_id", in.NodeID) }

// SetStatusOutput is the node after the change.
type SetStatusOutput struct{ Node View }

// DeactivateNode is UC-N4.
type DeactivateNode struct{ d Deps }

// NewDeactivateNode builds the use case.
func NewDeactivateNode(d Deps) *DeactivateNode { return &DeactivateNode{d: d} }

// Execute deactivates the node; the root cannot be deactivated. An inactive
// node stays inactive without a write or an audit entry.
func (uc *DeactivateNode) Execute(ctx context.Context, in SetStatusInput) (SetStatusOutput, error) {
	return setStatus(ctx, uc.d, in, false)
}

// ActivateNode is UC-N5.
type ActivateNode struct{ d Deps }

// NewActivateNode builds the use case.
func NewActivateNode(d Deps) *ActivateNode { return &ActivateNode{d: d} }

// Execute activates the node. It is allowed under an inactive ancestor; the
// view then shows EffectiveActive false.
func (uc *ActivateNode) Execute(ctx context.Context, in SetStatusInput) (SetStatusOutput, error) {
	return setStatus(ctx, uc.d, in, true)
}

func setStatus(ctx context.Context, d Deps, in SetStatusInput, active bool) (SetStatusOutput, error) {
	if err := in.Validate(); err != nil {
		return SetStatusOutput{}, err
	}
	var out SetStatusOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeDeactivate); err != nil {
			return err
		}
		n, err := d.Nodes.GetForUpdate(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		if !active && n.Type == dnode.TypePlatform {
			return apperr.New(apperr.RootImmutable, "the root node cannot be deactivated")
		}
		var next dnode.Node
		var changed bool
		action := "node.deactivated"
		if active {
			next, changed = n.Activate(d.Clock.Now())
			action = "node.activated"
		} else {
			next, changed = n.Deactivate(d.Clock.Now())
		}
		if changed {
			if err := d.Nodes.Update(ctx, tx, next); err != nil {
				return err
			}
			if err := d.audit(ctx, tx, port.AuditEntry{
				Action: action, NodeID: n.ID, ActorID: in.Actor, RequestID: in.RequestID,
				Before: map[string]string{"status": string(n.Status)},
				After:  map[string]string{"status": string(next.Status)},
			}); err != nil {
				return err
			}
			// Read the node back: whether its hidden ancestors are active is
			// known to the store only once the node itself is active.
			next, err = d.Nodes.Get(ctx, tx, in.NodeID)
			if err != nil {
				return err
			}
		}
		out.Node, err = d.view(ctx, tx, next)
		return err
	})
	if err != nil {
		return SetStatusOutput{}, err
	}
	return out, nil
}
