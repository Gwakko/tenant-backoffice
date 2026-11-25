package node

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// MoveNodeInput is the request of UC-N3.
type MoveNodeInput struct {
	Actor       user.ActorID
	RequestID   string
	NodeID      dnode.ID
	NewParentID dnode.ID
}

// Validate checks the input without touching the store. NodeID == NewParentID
// is not rejected here: the domain reports it as move_cycle, so the order of
// errors stays in one place.
func (in MoveNodeInput) Validate() error {
	if err := requireID("node_id", in.NodeID); err != nil {
		return err
	}
	return requireID("new_parent_id", in.NewParentID)
}

// MoveNodeOutput is the moved node.
type MoveNodeOutput struct{ Node View }

// MoveNode is UC-N3.
type MoveNode struct{ d Deps }

// NewMoveNode builds the use case.
func NewMoveNode(d Deps) *MoveNode { return &MoveNode{d: d} }

// Execute moves a node with its subtree under a new parent. A move changes two
// subtrees, so node.move is required on the current parent and on the new one.
// Everything that can be refused (cycle, parent type, inactive parent, room for
// one more child) is decided before the first write, under the tree lock.
func (uc *MoveNode) Execute(ctx context.Context, in MoveNodeInput) (MoveNodeOutput, error) {
	if err := in.Validate(); err != nil {
		return MoveNodeOutput{}, err
	}
	d := uc.d

	var out MoveNodeOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRead); err != nil {
			return err
		}
		n, err := d.Nodes.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		if n.ParentID == nil {
			return apperr.New(apperr.RootImmutable, "the root node cannot be moved")
		}
		if err := d.requireOnParent(ctx, tx, in.Actor, *n.ParentID, role.NodeMove); err != nil {
			return err
		}
		if err := d.require(ctx, tx, in.Actor, in.NewParentID, role.NodeMove); err != nil {
			return err
		}

		if err := d.Lock.Lock(ctx, tx); err != nil {
			return err
		}
		// Reread under the lock: paths may have changed since the checks above.
		oldParent := *n.ParentID
		n, err = d.Nodes.GetForUpdate(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		if n.ParentID == nil {
			return apperr.New(apperr.RootImmutable, "the root node cannot be moved")
		}
		if *n.ParentID != oldParent {
			// Another move got in first: the right on the parent it checked
			// belongs to the old parent.
			if err := d.requireOnParent(ctx, tx, in.Actor, *n.ParentID, role.NodeMove); err != nil {
				return err
			}
		}
		newParent, err := d.Nodes.Get(ctx, tx, in.NewParentID)
		if err != nil {
			return err
		}
		plan, err := n.PlanMove(newParent)
		if err != nil {
			return err
		}
		if plan.NoOp {
			out.Node, err = d.view(ctx, tx, n)
			return err
		}
		count, err := d.Nodes.CountChildren(ctx, tx, newParent.ID)
		if err != nil {
			return err
		}
		if count >= dnode.MaxChildren {
			return apperr.New(apperr.ChildrenLimit, "the new parent has the maximum number of children")
		}
		subtree, err := d.Nodes.Subtree(ctx, tx, n.ID)
		if err != nil {
			return err
		}
		// A sibling with the same name under the new parent surfaces as
		// name_taken from the unique index on the write.
		if err := d.Nodes.Move(ctx, tx, plan, len(subtree), d.Clock.Now()); err != nil {
			return err
		}
		if err := d.auditMove(ctx, tx, in, plan); err != nil {
			return err
		}
		moved, err := d.Nodes.Get(ctx, tx, n.ID)
		if err != nil {
			return err
		}
		out.Node, err = d.view(ctx, tx, moved)
		return err
	})
	if err != nil {
		return MoveNodeOutput{}, err
	}
	return out, nil
}

// auditMove writes the three records of a move: on the node, on the parent it
// left and on the parent it joined.
func (d Deps) auditMove(ctx context.Context, tx port.Tx, in MoveNodeInput, plan dnode.MovePlan) error {
	old := plan.OldParentID.String()
	next := plan.NewParentID.String()
	moved := map[string]string{"node_id": plan.NodeID.String()}
	entries := []port.AuditEntry{
		{Action: "node.moved", NodeID: plan.NodeID, Before: map[string]string{"parent_id": old}, After: map[string]string{"parent_id": next}},
		{Action: "node.child_moved_out", NodeID: *plan.OldParentID, Before: moved},
		{Action: "node.child_moved_in", NodeID: plan.NewParentID, After: moved},
	}
	for _, e := range entries {
		e.ActorID, e.RequestID = in.Actor, in.RequestID
		if err := d.audit(ctx, tx, e); err != nil {
			return err
		}
	}
	return nil
}
