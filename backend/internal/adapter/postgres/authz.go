package postgres

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// effectiveRoleSQL returns the highest role rank of the user among the
// assignments on the node and its ancestors, NULL when there is none (the node
// does not exist, lies outside the actor's subtrees, or the user is inactive).
// Roles are read from the database on every call: they are not part of a token.
const effectiveRoleSQL = `
SELECT max(CASE ra.role WHEN 'viewer' THEN 1 WHEN 'analyst' THEN 2
                        WHEN 'admin' THEN 3 WHEN 'owner' THEN 4 END)
  FROM role_assignments ra
  JOIN nodes a ON a.id = ra.node_id
  JOIN users u ON u.id = ra.user_id AND u.status = 'active'
 WHERE ra.user_id = $1
   AND a.path @> (SELECT path FROM nodes WHERE id = $2)`

// Checker implements authz.Checker.
type Checker struct{ log *slog.Logger }

var _ authz.Checker = (*Checker)(nil)

// NewChecker builds a checker; log receives unexpected storage rejections.
func NewChecker(log *slog.Logger) *Checker { return &Checker{log: log} }

// EffectiveRole implements authz.Checker.
func (c *Checker) EffectiveRole(ctx context.Context, tx port.Tx, actor user.ActorID, nodeID node.ID) (role.Role, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return "", false, err
	}
	var rank *int
	if err := t.QueryRow(ctx, effectiveRoleSQL, uuid.UUID(actor), uuid.UUID(nodeID)).Scan(&rank); err != nil {
		return "", false, mapError(c.log, "effective role", "node", err)
	}
	if rank == nil {
		return "", false, nil
	}
	for _, r := range role.All() {
		if r.Rank() == *rank {
			return r, true, nil
		}
	}
	return "", false, nil
}
