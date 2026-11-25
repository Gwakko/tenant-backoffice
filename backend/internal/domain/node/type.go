// Package node is the domain model of the tenant tree: node types and their
// allowed parent/child relations, names, paths, statuses and the pure
// operations (create child, move planning, rename, activation, deletion
// checks). It has no I/O and never reads the current time.
package node

import "github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"

// Type is the fixed kind of a node; it determines the node's depth.
type Type string

// Node types, from the root down to the leaf.
const (
	TypePlatform Type = "platform"
	TypePartner  Type = "partner"
	TypeCompany  Type = "company"
	TypeUnit     Type = "unit"
)

// MaxChildren is the maximum number of direct children of one node.
const MaxChildren = 1000

// ParseType validates a raw node type.
func ParseType(s string) (Type, error) {
	t := Type(s)
	if t.Level() == 0 {
		return "", apperr.New(apperr.InvalidNodeType, "unknown node type")
	}
	return t, nil
}

// Level returns the depth of the type, 1..4, or 0 for an unknown type.
func (t Type) Level() int {
	switch t {
	case TypePlatform:
		return 1
	case TypePartner:
		return 2
	case TypeCompany:
		return 3
	case TypeUnit:
		return 4
	default:
		return 0
	}
}

// AllowedParent returns the only type a node of type t may sit under. It
// returns false for the root and for unknown types.
func (t Type) AllowedParent() (Type, bool) {
	switch t {
	case TypePartner:
		return TypePlatform, true
	case TypeCompany:
		return TypePartner, true
	case TypeUnit:
		return TypeCompany, true
	case TypePlatform:
		return "", false
	default:
		return "", false
	}
}

// CanHaveChild reports whether a node of type t may have a child of type c.
func (t Type) CanHaveChild(c Type) bool {
	p, ok := c.AllowedParent()
	return ok && t.Level() != 0 && p == t
}
