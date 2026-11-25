package member

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// ChangeMemberRoleInput is the request of UC-M2.
type ChangeMemberRoleInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	UserID    user.ID
	Role      string
}

// Validate checks the input without touching the store.
func (in ChangeMemberRoleInput) Validate() error {
	if err := requireNodeID(in.NodeID); err != nil {
		return err
	}
	if err := requireUserID(in.UserID); err != nil {
		return err
	}
	if _, err := role.Parse(in.Role); err != nil {
		return validation("role", err)
	}
	return nil
}

// ChangeMemberRoleOutput is the assignment after the change.
type ChangeMemberRoleOutput struct{ Member Member }

// ChangeMemberRole is UC-M2.
type ChangeMemberRole struct{ d Deps }

// NewChangeMemberRole builds the use case.
func NewChangeMemberRole(d Deps) *ChangeMemberRole { return &ChangeMemberRole{d: d} }

// Execute replaces the role of an existing assignment on the node. The actor
// must be able to revoke the old role and to grant the new one; the last
// active owner of the root cannot be demoted.
func (uc *ChangeMemberRole) Execute(ctx context.Context, in ChangeMemberRoleInput) (ChangeMemberRoleOutput, error) {
	if err := in.Validate(); err != nil {
		return ChangeMemberRoleOutput{}, err
	}
	next, _ := role.Parse(in.Role)
	d := uc.d

	var out ChangeMemberRoleOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
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
		if !role.CanRevoke(actorRole, cur.Role) || !role.CanGrant(actorRole, next) {
			return notGrantable()
		}
		if cur.Role == next {
			out.Member = fromPort(cur)
			return nil
		}
		if err := d.guardLastRootOwner(ctx, tx, root, in.NodeID, in.UserID, cur.Role, next); err != nil {
			return err
		}
		a := role.Assignment{UserID: in.UserID, NodeID: in.NodeID, Role: next, GrantedBy: in.Actor, GrantedAt: d.Clock.Now()}
		if err := d.Members.Update(ctx, tx, a); err != nil {
			return err
		}
		out.Member = Member{UserID: cur.UserID, Email: cur.Email, Role: next, NodeID: in.NodeID, GrantedAt: a.GrantedAt}
		return d.audit(ctx, tx, port.AuditEntry{
			Action: "member.changed", NodeID: in.NodeID, ActorID: in.Actor, RequestID: in.RequestID,
			Before: map[string]string{"role": string(cur.Role)},
			After:  map[string]string{"user_id": in.UserID.String(), "role": string(next)},
		})
	})
	if err != nil {
		return ChangeMemberRoleOutput{}, err
	}
	return out, nil
}
