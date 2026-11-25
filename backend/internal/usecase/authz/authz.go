// Package authz is the first line of authorization: the role of the actor on
// the node, taken from the database on every request, checked against the
// permission matrix. The second line is row-level security.
package authz

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Checker resolves the effective role of an actor on a node: the highest role
// among the actor's assignments on the node and its ancestors. found is false
// when the actor has no role there, which includes a node that does not exist.
type Checker interface {
	EffectiveRole(ctx context.Context, tx port.Tx, actor user.ActorID, nodeID node.ID) (r role.Role, found bool, err error)
}

// Require checks that actor may perform perm on the node.
//
// No role on the node (absent, or outside the actor's subtrees) is
// apperr.NotFound, the same answer for both. A role that does not hold perm is
// apperr.Forbidden: the node is visible, so its existence is no secret. The
// effective role is returned in both cases when known.
func Require(ctx context.Context, c Checker, tx port.Tx, actor user.ActorID, nodeID node.ID, perm role.Permission) (role.Role, error) {
	r, found, err := c.EffectiveRole(ctx, tx, actor, nodeID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", apperr.NotFoundError("node")
	}
	if !role.Allows(r, perm) {
		return r, apperr.ForbiddenError("the role does not allow this action")
	}
	return r, nil
}
