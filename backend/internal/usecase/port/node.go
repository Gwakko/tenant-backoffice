package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// ChildCursor is the keyset position after which the next page of children
// starts: the sort key of the last returned child and its id.
type ChildCursor struct {
	NameKey string
	ID      node.ID
}

// ChildFilter narrows a list of children.
type ChildFilter struct {
	// Status keeps only nodes with this own status; empty keeps all.
	Status node.Status
	// Query keeps nodes whose name key contains it; it must already be
	// lower-cased. Empty keeps all.
	Query string
}

// AccessRoot is a node at which the actor holds a role that is not inherited
// from a visible ancestor: the top of one branch the actor can see.
// Node.AncestorsActive is not set.
type AccessRoot struct {
	Node node.Node
	Role role.Role
}

// NodeRepo stores tenant tree nodes. Every method needs the transaction of the
// acting user: what the repository returns is already limited to the actor's
// subtrees, and a node outside them is reported as absent.
type NodeRepo interface {
	// Get returns the node with the effective activity of its ancestors
	// (Node.AncestorsActive). A missing or invisible node is apperr.NotFound.
	Get(ctx context.Context, tx Tx, id node.ID) (node.Node, error)
	// GetForUpdate is Get with a row lock held until the transaction ends.
	GetForUpdate(ctx context.Context, tx Tx, id node.ID) (node.Node, error)
	// Insert stores a new node. A duplicate name under the parent is
	// apperr.NameTaken; a parent the actor cannot see is apperr.NotFound.
	Insert(ctx context.Context, tx Tx, n node.Node) error
	// Update stores name, status and updated_at of an existing node.
	Update(ctx context.Context, tx Tx, n node.Node) error
	// Delete removes a node without children.
	Delete(ctx context.Context, tx Tx, id node.ID) error
	// ListChildren returns up to limit children ordered by (name key, id)
	// after the cursor; an invisible parent has no children.
	ListChildren(ctx context.Context, tx Tx, parent node.ID, after *ChildCursor, limit int) ([]node.Node, error)
	// CountChildren returns the number of direct children the actor can see
	// (all of them for a visible parent).
	CountChildren(ctx context.Context, tx Tx, parent node.ID) (int, error)
	// Subtree returns the node and all its descendants, ancestors first.
	Subtree(ctx context.Context, tx Tx, root node.ID) ([]node.Node, error)
	// IsInSubtree reports whether node lies in the subtree of ancestor
	// (a node is in its own subtree); both must be visible.
	IsInSubtree(ctx context.Context, tx Tx, nodeID, ancestor node.ID) (bool, error)
	// Move rewrites the paths of the moved node and its descendants. expected is
	// the subtree size read under the tree lock; another row count aborts.
	Move(ctx context.Context, tx Tx, plan node.MovePlan, expected int, now time.Time) error
	// FindChildren is ListChildren with a filter on status and name.
	FindChildren(ctx context.Context, tx Tx, parent node.ID, f ChildFilter, after *ChildCursor, limit int) ([]node.Node, error)
	// ListActiveByType returns visible, effectively active nodes of one type
	// whose name key contains query (lower-cased; empty keeps all), ordered by
	// (name key, id) after the cursor.
	ListActiveByType(ctx context.Context, tx Tx, t node.Type, query string, after *ChildCursor, limit int) ([]node.Node, error)
	// Ancestors returns the visible nodes on the path to id, the node itself
	// last: it starts at the actor's access root, not at the tree root.
	// AncestorsActive of the result is not set.
	Ancestors(ctx context.Context, tx Tx, id node.ID) ([]node.Node, error)
	// DeleteStats reports what blocks deleting the node. Until the ledger
	// exists (M3) only Children is filled; Balance and HasOperations are zero.
	DeleteStats(ctx context.Context, tx Tx, id node.ID) (node.DeleteStats, error)
	// AccessRoots returns the actor's access roots, ordered by path.
	AccessRoots(ctx context.Context, tx Tx, actor user.ActorID) ([]AccessRoot, error)
}
