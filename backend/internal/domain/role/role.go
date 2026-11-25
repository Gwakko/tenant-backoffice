// Package role is the authorization model: roles, permissions, the
// role-by-permission matrix, grant/revoke rules and effective-role resolution
// along a node path.
package role

import (
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Role is an access level on a node; higher rank includes lower ones.
type Role string

// Roles, from the lowest to the highest rank.
const (
	Viewer  Role = "viewer"
	Analyst Role = "analyst"
	Admin   Role = "admin"
	Owner   Role = "owner"
)

// All lists every role in ascending rank order.
func All() []Role { return []Role{Viewer, Analyst, Admin, Owner} }

// Parse validates a raw role name.
func Parse(s string) (Role, error) {
	r := Role(s)
	if r.Rank() == 0 {
		return "", apperr.New(apperr.InvalidRole, "unknown role")
	}
	return r, nil
}

// Rank returns 1..4 for known roles and 0 for an unknown or empty one.
func (r Role) Rank() int {
	switch r {
	case Viewer:
		return 1
	case Analyst:
		return 2
	case Admin:
		return 3
	case Owner:
		return 4
	default:
		return 0
	}
}

// Assignment grants a role to a user on a node (and, by inheritance, on its
// subtree). There is at most one assignment per (user, node).
type Assignment struct {
	UserID    user.ID
	NodeID    node.ID
	Role      Role
	GrantedBy user.ActorID
	GrantedAt time.Time
}
