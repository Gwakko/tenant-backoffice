package node

import (
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// Status is the node's own activity flag.
type Status string

// Node statuses.
const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

// Node is a tenant tree node. All methods are pure and return copies.
type Node struct {
	ID       ID
	Type     Type
	ParentID *ID
	Path     Path
	Name     Name
	Status   Status
	// AncestorsActive is supplied by the repository: true when every ancestor
	// is active (always true for the root).
	AncestorsActive bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EffectiveActive reports whether the node and all its ancestors are active.
func (n Node) EffectiveActive() bool {
	return n.Status == StatusActive && n.AncestorsActive
}

// NewChild builds a new active child of parent.
func NewChild(parent Node, id ID, t Type, name Name, now time.Time) (Node, error) {
	if !parent.Type.CanHaveChild(t) {
		return Node{}, errInvalidParentType()
	}
	if !parent.EffectiveActive() {
		return Node{}, errNodeInactive()
	}
	pid := parent.ID
	now = now.UTC()
	return Node{
		ID:              id,
		Type:            t,
		ParentID:        &pid,
		Path:            parent.Path.Append(id),
		Name:            name,
		Status:          StatusActive,
		AncestorsActive: true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// Rename returns the node with a new name; changed is false when the
// normalized name is identical.
func (n Node) Rename(name Name, now time.Time) (Node, bool) {
	if n.Name.Equal(name) {
		return n, false
	}
	n.Name = name
	n.UpdatedAt = now.UTC()
	return n, true
}

// Deactivate sets the node inactive; changed is false if it already was.
func (n Node) Deactivate(now time.Time) (Node, bool) { return n.setStatus(StatusInactive, now) }

// Activate sets the node active; changed is false if it already was. The
// effective activity still depends on the ancestors.
func (n Node) Activate(now time.Time) (Node, bool) { return n.setStatus(StatusActive, now) }

func (n Node) setStatus(s Status, now time.Time) (Node, bool) {
	if n.Status == s {
		return n, false
	}
	n.Status = s
	n.UpdatedAt = now.UTC()
	return n, true
}

// MovePlan describes a validated move of a node under a new parent.
type MovePlan struct {
	NodeID      ID
	OldParentID *ID
	NewParentID ID
	OldPath     Path
	NewPath     Path
	// NoOp is true when the new parent is the current one.
	NoOp bool
}

// PlanMove validates moving n under newParent. Checks run in this order:
// root, cycle, type, inactive new parent, same parent (no-op).
func (n Node) PlanMove(newParent Node) (MovePlan, error) {
	if n.Type == TypePlatform {
		return MovePlan{}, errRootImmutable()
	}
	if newParent.Path.Contains(n.ID) {
		return MovePlan{}, apperr.New(apperr.MoveCycle, "a node cannot be moved into itself or its descendant")
	}
	if !newParent.Type.CanHaveChild(n.Type) {
		return MovePlan{}, errInvalidParentType()
	}
	if !newParent.EffectiveActive() {
		return MovePlan{}, errNodeInactive()
	}
	return MovePlan{
		NodeID:      n.ID,
		OldParentID: n.ParentID,
		NewParentID: newParent.ID,
		OldPath:     n.Path,
		NewPath:     newParent.Path.Append(n.ID),
		NoOp:        n.ParentID != nil && *n.ParentID == newParent.ID,
	}, nil
}

// DeleteStats carries repository facts needed to decide whether a node can be
// deleted.
type DeleteStats struct {
	Children int
	// Balance is the node balance in minor currency units, never negative.
	Balance       int64
	HasOperations bool
}

// CanDelete returns the reason a node cannot be deleted, or nil.
func (n Node) CanDelete(stats DeleteStats) error {
	switch {
	case n.Type == TypePlatform:
		return errRootImmutable()
	case stats.Children > 0:
		return apperr.New(apperr.NodeHasChildren, "node has children")
	case stats.Balance > 0:
		return apperr.New(apperr.NodeHasBalance, "node has a non-zero balance")
	case stats.HasOperations:
		return apperr.New(apperr.NodeHasHistory, "node has operation history; deactivate it instead")
	}
	return nil
}

func errInvalidParentType() error {
	return apperr.New(apperr.InvalidParentType, "parent node type does not allow this child type")
}

func errNodeInactive() error {
	return apperr.New(apperr.NodeInactive, "node or one of its ancestors is inactive")
}

func errRootImmutable() error {
	return apperr.New(apperr.RootImmutable, "the root node cannot be moved or deleted")
}
