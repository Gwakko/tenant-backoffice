package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	resourceNode = "node"

	// selectNode reads one node together with the activity of its ancestors.
	// tb_node_effective_active is SECURITY DEFINER, because ancestors above the
	// actor's access root are hidden by RLS; it returns NULL for a node the actor
	// cannot see.
	selectNode = `SELECT ` + nodeColumns + `,
       tb_node_effective_active(n.id),
       CASE WHEN n.parent_id IS NULL THEN true ELSE tb_node_effective_active(n.parent_id) END
  FROM nodes n WHERE n.id = $1`

	insertNode = `INSERT INTO nodes (id, type, parent_id, path, name, name_key, status, created_at, updated_at)
VALUES ($1, $2, $3, $4::ltree, $5, $6, $7, $8, $9)`

	updateNode = `UPDATE nodes SET name = $2, name_key = $3, status = $4, updated_at = $5 WHERE id = $1`

	deleteNode = `DELETE FROM nodes WHERE id = $1`

	selectChildren = `SELECT ` + nodeColumns + ` FROM nodes n
 WHERE n.parent_id = $1
 ORDER BY n.name_key, n.id LIMIT $2`

	selectChildrenAfter = `SELECT ` + nodeColumns + ` FROM nodes n
 WHERE n.parent_id = $1 AND (n.name_key, n.id) > ($2, $3)
 ORDER BY n.name_key, n.id LIMIT $4`

	countChildren = `SELECT count(*) FROM nodes WHERE parent_id = $1`

	selectSubtree = `SELECT ` + nodeColumns + ` FROM nodes n WHERE n.path <@ $1::ltree ORDER BY n.path`

	selectInSubtree = `SELECT x.path <@ y.path FROM nodes x, nodes y WHERE x.id = $1 AND y.id = $2`

	moveSubtree = `UPDATE nodes
   SET path = $2::ltree || subpath(path, nlevel($1::ltree) - 1),
       parent_id = CASE WHEN id = $3 THEN $4 ELSE parent_id END,
       updated_at = $5
 WHERE path <@ $1::ltree`
)

// NodeRepo implements port.NodeRepo. It holds no connection: every method
// works inside the transaction it is given, so the actor of that transaction
// decides what is visible.
type NodeRepo struct{ log *slog.Logger }

var _ port.NodeRepo = (*NodeRepo)(nil)

// NewNodeRepo builds the repository; log receives writes that row-level
// security rejected.
func NewNodeRepo(log *slog.Logger) *NodeRepo { return &NodeRepo{log: log} }

// Get implements port.NodeRepo.
func (r *NodeRepo) Get(ctx context.Context, tx port.Tx, id node.ID) (node.Node, error) {
	return r.get(ctx, tx, id, "")
}

// GetForUpdate implements port.NodeRepo.
func (r *NodeRepo) GetForUpdate(ctx context.Context, tx port.Tx, id node.ID) (node.Node, error) {
	return r.get(ctx, tx, id, " FOR UPDATE OF n")
}

func (r *NodeRepo) get(ctx context.Context, tx port.Tx, id node.ID, lock string) (node.Node, error) {
	t, err := unwrap(tx)
	if err != nil {
		return node.Node{}, err
	}
	var row nodeRow
	var eff, anc *bool
	err = t.QueryRow(ctx, selectNode+lock, uid(id)).Scan(append(row.scan(), &eff, &anc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return node.Node{}, apperr.NotFoundError(resourceNode)
	}
	if err != nil {
		return node.Node{}, mapError(r.log, "get node", resourceNode, err)
	}
	n, err := row.toNode()
	if err != nil {
		return node.Node{}, err
	}
	n.AncestorsActive = ancestorsActive(n.Status, eff, anc)
	return n, nil
}

// ancestorsActive derives the activity of the ancestors from the two values of
// tb_node_effective_active. anc is the effective activity of the parent; it is
// NULL when the parent is hidden (the node is the actor's access root). Then
// an active node's own effective activity equals that of its ancestors; for an
// inactive access root the ancestors cannot be learned without a new
// SECURITY DEFINER function, so the answer is the safe one: not active.
func ancestorsActive(status node.Status, eff, anc *bool) bool {
	switch {
	case anc != nil:
		return *anc
	case status == node.StatusActive && eff != nil:
		return *eff
	default:
		return false
	}
}

// Insert implements port.NodeRepo.
func (r *NodeRepo) Insert(ctx context.Context, tx port.Tx, n node.Node) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	var parent any
	if n.ParentID != nil {
		parent = uid(*n.ParentID)
	}
	// No RETURNING: the row is written under a policy, and a statement that
	// also reads it back is checked against the read policy too.
	_, err = t.Exec(ctx, insertNode, uid(n.ID), string(n.Type), parent, pathText(n.Path),
		n.Name.String(), n.Name.Key(), string(n.Status), n.CreatedAt.UTC(), n.UpdatedAt.UTC())
	return mapWriteError(r.log, "insert node", resourceNode, err)
}

// Update implements port.NodeRepo.
func (r *NodeRepo) Update(ctx context.Context, tx port.Tx, n node.Node) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, updateNode, uid(n.ID), n.Name.String(), n.Name.Key(), string(n.Status), n.UpdatedAt.UTC())
	if err != nil {
		return mapWriteError(r.log, "update node", resourceNode, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFoundError(resourceNode)
	}
	return nil
}

// Delete implements port.NodeRepo.
func (r *NodeRepo) Delete(ctx context.Context, tx port.Tx, id node.ID) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, deleteNode, uid(id))
	if err != nil {
		if pe, ok := pgError(err); ok && (pe.Code == codeRestrictViolation || pe.Code == codeForeignKeyViolation) {
			return apperr.New(apperr.NodeHasChildren, "node has children or dependent records")
		}
		return mapWriteError(r.log, "delete node", resourceNode, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFoundError(resourceNode)
	}
	return nil
}

// ListChildren implements port.NodeRepo.
func (r *NodeRepo) ListChildren(ctx context.Context, tx port.Tx, parent node.ID, after *port.ChildCursor, limit int) ([]node.Node, error) {
	if limit < 1 {
		return nil, fmt.Errorf("postgres: list children: limit must be positive, got %d", limit)
	}
	p, err := r.Get(ctx, tx, parent)
	if errors.Is(err, apperr.NotFoundError(resourceNode)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if after == nil {
		rows, err = t.Query(ctx, selectChildren, uid(parent), limit)
	} else {
		rows, err = t.Query(ctx, selectChildrenAfter, uid(parent), after.NameKey, uid(after.ID), limit)
	}
	if err != nil {
		return nil, mapError(r.log, "list children", resourceNode, err)
	}
	nodes, err := collectNodes(rows)
	if err != nil {
		return nil, mapError(r.log, "list children", resourceNode, err)
	}
	// All children share their ancestors: the parent and everything above it.
	for i := range nodes {
		nodes[i].AncestorsActive = p.EffectiveActive()
	}
	return nodes, nil
}

// CountChildren implements port.NodeRepo.
func (r *NodeRepo) CountChildren(ctx context.Context, tx port.Tx, parent node.ID) (int, error) {
	t, err := unwrap(tx)
	if err != nil {
		return 0, err
	}
	var n int
	if err := t.QueryRow(ctx, countChildren, uid(parent)).Scan(&n); err != nil {
		return 0, mapError(r.log, "count children", resourceNode, err)
	}
	return n, nil
}

// Subtree implements port.NodeRepo.
func (r *NodeRepo) Subtree(ctx context.Context, tx port.Tx, root node.ID) ([]node.Node, error) {
	rootNode, err := r.Get(ctx, tx, root)
	if err != nil {
		return nil, err
	}
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, selectSubtree, pathText(rootNode.Path))
	if err != nil {
		return nil, mapError(r.log, "subtree", resourceNode, err)
	}
	nodes, err := collectNodes(rows)
	if err != nil {
		return nil, mapError(r.log, "subtree", resourceNode, err)
	}
	// Ancestors sort before descendants, so the activity of a parent is known
	// when its children are reached; only the root's own ancestors come from SQL.
	effective := make(map[node.ID]bool, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		switch {
		case n.ID == root:
			n.AncestorsActive = rootNode.AncestorsActive
		case n.ParentID != nil:
			n.AncestorsActive = effective[*n.ParentID]
		}
		effective[n.ID] = n.EffectiveActive()
	}
	return nodes, nil
}

// IsInSubtree implements port.NodeRepo.
func (r *NodeRepo) IsInSubtree(ctx context.Context, tx port.Tx, nodeID, ancestor node.ID) (bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return false, err
	}
	var in bool
	err = t.QueryRow(ctx, selectInSubtree, uid(nodeID), uid(ancestor)).Scan(&in)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, apperr.NotFoundError(resourceNode)
	}
	if err != nil {
		return false, mapError(r.log, "is in subtree", resourceNode, err)
	}
	return in, nil
}

// Move implements port.NodeRepo.
func (r *NodeRepo) Move(ctx context.Context, tx port.Tx, plan node.MovePlan, expected int, now time.Time) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, moveSubtree, pathText(plan.OldPath), pathText(plan.NewPath.Parent()),
		uid(plan.NodeID), uid(plan.NewParentID), now.UTC())
	if err != nil {
		return mapWriteError(r.log, "move subtree", resourceNode, err)
	}
	if got := tag.RowsAffected(); got != int64(expected) {
		return fmt.Errorf("postgres: move updated %d rows, the subtree read under the tree lock has %d", got, expected)
	}
	return nil
}

func collectNodes(rows pgx.Rows) ([]node.Node, error) {
	defer rows.Close()
	var out []node.Node
	for rows.Next() {
		var row nodeRow
		if err := rows.Scan(row.scan()...); err != nil {
			return nil, err
		}
		n, err := row.toNode()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
