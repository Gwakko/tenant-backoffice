package node

import (
	"context"

	"github.com/google/uuid"

	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// RenameNodeInput is the request of UC-N2.
type RenameNodeInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	Name      string
}

// Validate checks the input without touching the store.
func (in RenameNodeInput) Validate() error {
	if err := requireID("node_id", in.NodeID); err != nil {
		return err
	}
	if _, err := dnode.ParseName(in.Name); err != nil {
		return validation("name", err)
	}
	return nil
}

// RenameNodeOutput is the renamed node.
type RenameNodeOutput struct{ Node View }

// RenameNode is UC-N2.
type RenameNode struct{ d Deps }

// NewRenameNode builds the use case.
func NewRenameNode(d Deps) *RenameNode { return &RenameNode{d: d} }

// Execute renames the node. The same normalized name is no change: no write and
// no audit entry.
func (uc *RenameNode) Execute(ctx context.Context, in RenameNodeInput) (RenameNodeOutput, error) {
	if err := in.Validate(); err != nil {
		return RenameNodeOutput{}, err
	}
	name, _ := dnode.ParseName(in.Name)
	d := uc.d

	var out RenameNodeOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRename); err != nil {
			return err
		}
		n, err := d.Nodes.GetForUpdate(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		renamed, changed := n.Rename(name, d.Clock.Now())
		if changed {
			if err := d.Nodes.Update(ctx, tx, renamed); err != nil {
				return err
			}
			if err := d.audit(ctx, tx, port.AuditEntry{
				Action: "node.renamed", NodeID: n.ID, TargetID: uuid.UUID(n.ID), ActorID: in.Actor, RequestID: in.RequestID,
				Before: map[string]string{"name": n.Name.String()},
				After:  map[string]string{"name": renamed.Name.String()},
			}); err != nil {
				return err
			}
		}
		out.Node, err = d.view(ctx, tx, renamed)
		return err
	})
	if err != nil {
		return RenameNodeOutput{}, err
	}
	return out, nil
}
