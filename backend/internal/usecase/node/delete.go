package node

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// DeleteNodeInput is the request of UC-N6.
type DeleteNodeInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
}

// Validate checks the input without touching the store.
func (in DeleteNodeInput) Validate() error { return requireID("node_id", in.NodeID) }

// DeleteNodeOutput is empty.
type DeleteNodeOutput struct{}

// DeleteNode is UC-N6.
type DeleteNode struct{ d Deps }

// NewDeleteNode builds the use case.
func NewDeleteNode(d Deps) *DeleteNode { return &DeleteNode{d: d} }

// Execute deletes an empty node. It needs node.delete on the parent: the change
// is in the parent's subtree and the audit record goes on the parent. Children,
// a balance and an operation history each block the deletion; the balance and
// history facts come from DeleteStats and are filled once the ledger exists.
func (uc *DeleteNode) Execute(ctx context.Context, in DeleteNodeInput) (DeleteNodeOutput, error) {
	if err := in.Validate(); err != nil {
		return DeleteNodeOutput{}, err
	}
	d := uc.d

	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRead); err != nil {
			return err
		}
		n, err := d.Nodes.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		if n.ParentID == nil {
			return apperr.New(apperr.RootImmutable, "the root node cannot be deleted")
		}
		if err := d.requireOnParent(ctx, tx, in.Actor, *n.ParentID, role.NodeDelete); err != nil {
			return err
		}

		if err := d.Lock.Lock(ctx, tx); err != nil {
			return err
		}
		checked := *n.ParentID
		n, err = d.Nodes.GetForUpdate(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		if n.ParentID == nil {
			return apperr.New(apperr.RootImmutable, "the root node cannot be deleted")
		}
		if *n.ParentID != checked {
			if err := d.requireOnParent(ctx, tx, in.Actor, *n.ParentID, role.NodeDelete); err != nil {
				return err
			}
		}
		stats, err := d.Nodes.DeleteStats(ctx, tx, n.ID)
		if err != nil {
			return err
		}
		if err := n.CanDelete(stats); err != nil {
			return err
		}
		// Role assignments and settings go with the node by cascade.
		if err := d.Nodes.Delete(ctx, tx, n.ID); err != nil {
			return err
		}
		return d.audit(ctx, tx, port.AuditEntry{
			Action: "node.deleted", NodeID: *n.ParentID, ActorID: in.Actor, RequestID: in.RequestID,
			Before: map[string]string{"node_id": n.ID.String(), "name": n.Name.String(), "type": string(n.Type)},
		})
	})
	return DeleteNodeOutput{}, err
}
