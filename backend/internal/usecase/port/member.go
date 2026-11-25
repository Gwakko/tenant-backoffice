package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Member is a role assignment together with the email of the assigned user.
type Member struct {
	UserID    user.ID
	Email     user.Email
	Role      role.Role
	NodeID    node.ID
	GrantedAt time.Time
}

// MemberCursor is the keyset position after which the next page of members
// starts.
type MemberCursor struct {
	Email  string
	UserID user.ID
	NodeID node.ID
}

// MemberRepo stores role assignments. Every method works inside the
// transaction of the acting user, so row-level security limits what it sees and
// writes to the actor's subtrees. RLS does not check roles: the use case does.
type MemberRepo interface {
	// GetForUpdate returns the assignment of the user on exactly this node
	// (not an inherited one) and locks it until the transaction ends.
	GetForUpdate(ctx context.Context, tx Tx, nodeID node.ID, userID user.ID) (m Member, found bool, err error)
	// Insert stores a new assignment. It reports false, without error, when an
	// assignment of the user on the node already exists (a concurrent insert).
	Insert(ctx context.Context, tx Tx, a role.Assignment) (inserted bool, err error)
	// Update replaces role, granter and time of an existing assignment.
	Update(ctx context.Context, tx Tx, a role.Assignment) error
	// Delete removes the assignment; a missing one is apperr.NotFound.
	Delete(ctx context.Context, tx Tx, nodeID node.ID, userID user.ID) error
	// LockActiveOwners locks and returns the active users holding owner on the
	// node, in a fixed order. Concurrent callers queue on these rows, which is
	// what makes the last-owner check race-free.
	LockActiveOwners(ctx context.Context, tx Tx, nodeID node.ID) ([]user.ID, error)
	// List returns members ordered by (email, user id, node id) after the
	// cursor. Without inherited only assignments on the node itself are
	// returned; with it also those on its visible ancestors.
	List(ctx context.Context, tx Tx, nodeID node.ID, inherited bool, after *MemberCursor, limit int) ([]Member, error)
}
