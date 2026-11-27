package member

import (
	"context"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// RemoveMemberInput is the request of UC-M3.
type RemoveMemberInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	UserID    user.ID
}

// Validate checks the input without touching the store.
func (in RemoveMemberInput) Validate() error {
	if err := requireNodeID(in.NodeID); err != nil {
		return err
	}
	return requireUserID(in.UserID)
}

// RemoveMember is UC-M3.
type RemoveMember struct{ d Deps }

// NewRemoveMember builds the use case.
func NewRemoveMember(d Deps) *RemoveMember { return &RemoveMember{d: d} }

// Execute removes the assignment of the user on the node. An actor may remove
// their own assignment (EC-42); the last active owner of the root cannot be
// removed (EC-35).
//
// The audit entry is written before the delete: removing one's own assignment
// takes the node out of the actor's sight, and an entry written afterwards
// would be refused by row-level security.
func (uc *RemoveMember) Execute(ctx context.Context, in RemoveMemberInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	d := uc.d
	return d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		actorRole, err := d.requireManage(ctx, tx, in.Actor, in.NodeID)
		if err != nil {
			return err
		}
		root, err := d.isRoot(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		cur, found, err := d.Members.GetForUpdate(ctx, tx, in.NodeID, in.UserID)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFoundError("member")
		}
		if !role.CanRevoke(actorRole, cur.Role) {
			return notGrantable()
		}
		if err := d.guardLastRootOwner(ctx, tx, root, in.NodeID, in.UserID, cur.Role, ""); err != nil {
			return err
		}
		if err := d.audit(ctx, tx, port.AuditEntry{
			Action: "member.revoked", NodeID: in.NodeID, TargetID: uuid.UUID(in.UserID), ActorID: in.Actor, RequestID: in.RequestID,
			Before: map[string]string{"user_id": in.UserID.String(), "role": string(cur.Role)},
		}); err != nil {
			return err
		}
		return d.Members.Delete(ctx, tx, in.NodeID, in.UserID)
	})
}
