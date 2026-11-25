package postgres

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// nodeColumns lists the columns nodeRow scans, in order.
const nodeColumns = `n.id, n.type, n.parent_id, n.path::text, n.name, n.status, n.created_at, n.updated_at`

// nodeRow is one row of the nodes table.
type nodeRow struct {
	ID        uuid.UUID
	Type      string
	ParentID  *uuid.UUID
	Path      string
	Name      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// scan returns the arguments for pgx Scan in nodeColumns order.
func (r *nodeRow) scan() []any {
	return []any{&r.ID, &r.Type, &r.ParentID, &r.Path, &r.Name, &r.Status, &r.CreatedAt, &r.UpdatedAt}
}

// toNode maps a row to the domain entity. AncestorsActive is not stored: the
// caller sets it.
func (r nodeRow) toNode() (node.Node, error) {
	typ, err := node.ParseType(r.Type)
	if err != nil {
		return node.Node{}, fmt.Errorf("node %s: %w", r.ID, err)
	}
	name, err := node.ParseName(r.Name)
	if err != nil {
		return node.Node{}, fmt.Errorf("node %s: %w", r.ID, err)
	}
	status := node.Status(r.Status)
	if status != node.StatusActive && status != node.StatusInactive {
		return node.Node{}, fmt.Errorf("node %s: unknown status %q", r.ID, r.Status)
	}
	path, err := parsePath(r.Path)
	if err != nil {
		return node.Node{}, fmt.Errorf("node %s: %w", r.ID, err)
	}
	var parent *node.ID
	if r.ParentID != nil {
		p := node.ID(*r.ParentID)
		parent = &p
	}
	return node.Node{
		ID:        node.ID(r.ID),
		Type:      typ,
		ParentID:  parent,
		Path:      path,
		Name:      name,
		Status:    status,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

// label is the ltree label of a node id: the id without dashes (tb_label in SQL).
func label(id node.ID) string {
	return strings.ReplaceAll(uuid.UUID(id).String(), "-", "")
}

// pathText renders a path as ltree text.
func pathText(p node.Path) string {
	labels := make([]string, len(p))
	for i, id := range p {
		labels[i] = label(id)
	}
	return strings.Join(labels, ".")
}

// parsePath reads ltree text back into a path.
func parsePath(s string) (node.Path, error) {
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	parts := strings.Split(s, ".")
	out := make(node.Path, len(parts))
	for i, p := range parts {
		u, err := uuid.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("path label %q is not a node id", p)
		}
		out[i] = node.ID(u)
	}
	return out, nil
}

func uid(id node.ID) uuid.UUID { return uuid.UUID(id) }
