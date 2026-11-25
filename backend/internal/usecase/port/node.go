package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// ChildCursor is the keyset position after which the next page of children
// starts: the sort key of the last returned child and its id.
type ChildCursor struct {
	NameKey string
	ID      node.ID
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
}
