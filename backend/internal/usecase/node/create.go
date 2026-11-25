package node

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// CreateNodeInput is the request of UC-N1.
type CreateNodeInput struct {
	Actor     user.ActorID
	RequestID string
	ParentID  dnode.ID
	Type      string
	Name      string
}

// Validate checks the input without touching the store.
func (in CreateNodeInput) Validate() error {
	if err := requireID("parent_id", in.ParentID); err != nil {
		return err
	}
	if _, err := dnode.ParseType(in.Type); err != nil {
		return validation("type", err)
	}
	if _, err := dnode.ParseName(in.Name); err != nil {
		return validation("name", err)
	}
	return nil
}

// CreateNodeOutput is the new node.
type CreateNodeOutput struct{ Node View }

// CreateNode is UC-N1.
type CreateNode struct{ d Deps }

// NewCreateNode builds the use case.
func NewCreateNode(d Deps) *CreateNode { return &CreateNode{d: d} }

// Execute creates a child of ParentID. It needs node.create on the parent and
// runs under the tree lock; the parent is checked here (type, activity, room),
// so a rejection by the store never has to stand for one of these reasons.
func (uc *CreateNode) Execute(ctx context.Context, in CreateNodeInput) (CreateNodeOutput, error) {
	if err := in.Validate(); err != nil {
		return CreateNodeOutput{}, err
	}
	typ, _ := dnode.ParseType(in.Type)
	name, _ := dnode.ParseName(in.Name)
	d := uc.d

	var out CreateNodeOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.ParentID, role.NodeCreate); err != nil {
			return err
		}
		if err := d.Lock.Lock(ctx, tx); err != nil {
			return err
		}
		parent, err := d.Nodes.Get(ctx, tx, in.ParentID)
		if err != nil {
			return err
		}
		count, err := d.Nodes.CountChildren(ctx, tx, parent.ID)
		if err != nil {
			return err
		}
		if count >= dnode.MaxChildren {
			return apperr.New(apperr.ChildrenLimit, "the parent has the maximum number of children")
		}
		n, err := dnode.NewChild(parent, dnode.ID(d.IDs.New()), typ, name, d.Clock.Now())
		if err != nil {
			return err
		}
		if err := d.Nodes.Insert(ctx, tx, n); err != nil {
			return err
		}
		if err := d.audit(ctx, tx, port.AuditEntry{
			Action: "node.created", NodeID: n.ID, ActorID: in.Actor, RequestID: in.RequestID,
			After: map[string]string{"type": string(n.Type), "name": n.Name.String(), "parent_id": parent.ID.String()},
		}); err != nil {
			return err
		}
		out.Node, err = d.view(ctx, tx, n)
		return err
	})
	if err != nil {
		return CreateNodeOutput{}, err
	}
	return out, nil
}
