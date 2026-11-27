package node

import (
	"context"

	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// GetNodeInput is the request of UC-N7.
type GetNodeInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
}

// Validate checks the input without touching the store.
func (in GetNodeInput) Validate() error { return requireID("node_id", in.NodeID) }

// GetNodeOutput is the node with breadcrumbs from the actor's access root.
type GetNodeOutput struct{ Node View }

// GetNode is UC-N7.
type GetNode struct{ d Deps }

// NewGetNode builds the use case.
func NewGetNode(d Deps) *GetNode { return &GetNode{d: d} }

// Execute returns one node; it needs node.read.
func (uc *GetNode) Execute(ctx context.Context, in GetNodeInput) (GetNodeOutput, error) {
	if err := in.Validate(); err != nil {
		return GetNodeOutput{}, err
	}
	d := uc.d
	var out GetNodeOutput
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRead); err != nil {
			return err
		}
		n, err := d.Nodes.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		out.Node, err = d.view(ctx, tx, n)
		return err
	})
	if err != nil {
		return GetNodeOutput{}, err
	}
	return out, nil
}

// ListChildrenInput is the request of UC-N8. Status is "", "all", "active" or
// "inactive".
type ListChildrenInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
	Status string
	Query  string
	Limit  int
	Cursor string
}

// Validate checks the input without touching the store.
func (in ListChildrenInput) Validate() error {
	if err := requireID("node_id", in.NodeID); err != nil {
		return err
	}
	if _, err := parseStatus(in.Status); err != nil {
		return err
	}
	if err := validateList(in.Limit, in.Query); err != nil {
		return err
	}
	_, err := decodeCursor(in.Cursor)
	return err
}

func parseStatus(s string) (dnode.Status, error) {
	switch s {
	case "", "all":
		return "", nil
	case string(dnode.StatusActive), string(dnode.StatusInactive):
		return dnode.Status(s), nil
	}
	return "", validation("status", nil)
}

// ListChildrenOutput is one page of children; NextCursor is empty on the last.
type ListChildrenOutput struct {
	Items      []Summary
	NextCursor string
}

// ListChildren is UC-N8.
type ListChildren struct{ d Deps }

// NewListChildren builds the use case.
func NewListChildren(d Deps) *ListChildren { return &ListChildren{d: d} }

// Execute lists the children of a node, ordered by name.
func (uc *ListChildren) Execute(ctx context.Context, in ListChildrenInput) (ListChildrenOutput, error) {
	if err := in.Validate(); err != nil {
		return ListChildrenOutput{}, err
	}
	status, _ := parseStatus(in.Status)
	after, _ := decodeCursor(in.Cursor)
	d := uc.d
	out := ListChildrenOutput{Items: []Summary{}}
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRead); err != nil {
			return err
		}
		kids, err := d.Nodes.FindChildren(ctx, tx, in.NodeID,
			port.ChildFilter{Status: status, Query: searchKey(in.Query)}, after, in.Limit+1)
		if err != nil {
			return err
		}
		if len(kids) > in.Limit {
			kids = kids[:in.Limit]
			last := kids[len(kids)-1]
			out.NextCursor = encodeCursor(port.ChildCursor{NameKey: last.Name.Key(), ID: last.ID})
		}
		items, err := d.summarizeAll(ctx, tx, kids)
		if err != nil {
			return err
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return ListChildrenOutput{}, err
	}
	return out, nil
}

// ListMoveTargetsInput is the request of UC-N9.
type ListMoveTargetsInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
	Query  string
	Limit  int
	Cursor string
}

// Validate checks the input without touching the store.
func (in ListMoveTargetsInput) Validate() error {
	if err := requireID("node_id", in.NodeID); err != nil {
		return err
	}
	if err := validateList(in.Limit, in.Query); err != nil {
		return err
	}
	_, err := decodeCursor(in.Cursor)
	return err
}

// ListMoveTargetsOutput is one page of possible new parents.
type ListMoveTargetsOutput struct {
	Items      []Summary
	NextCursor string
}

// ListMoveTargets is UC-N9.
type ListMoveTargets struct{ d Deps }

// NewListMoveTargets builds the use case.
func NewListMoveTargets(d Deps) *ListMoveTargets { return &ListMoveTargets{d: d} }

// scanPage is how many candidates are read at a time while filtering them by
// the actor's role.
const scanPage = 200

// Execute lists the nodes the given node could be moved under: of the parent
// type, effectively active, where the actor holds node.move, except the
// current parent. The role of every candidate is read from the store, so the
// list never offers a target the move itself would refuse.
func (uc *ListMoveTargets) Execute(ctx context.Context, in ListMoveTargetsInput) (ListMoveTargetsOutput, error) {
	if err := in.Validate(); err != nil {
		return ListMoveTargetsOutput{}, err
	}
	after, _ := decodeCursor(in.Cursor)
	d := uc.d
	out := ListMoveTargetsOutput{Items: []Summary{}}
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.NodeRead); err != nil {
			return err
		}
		n, err := d.Nodes.Get(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		parentType, ok := n.Type.AllowedParent()
		if !ok {
			return nil // the root has nowhere to go
		}
		key := searchKey(in.Query)
		var picked []dnode.Node
		for len(picked) <= in.Limit {
			page, err := d.Nodes.ListActiveByType(ctx, tx, parentType, key, after, scanPage)
			if err != nil {
				return err
			}
			for _, c := range page {
				after = &port.ChildCursor{NameKey: c.Name.Key(), ID: c.ID}
				if n.ParentID != nil && c.ID == *n.ParentID {
					continue
				}
				r, found, err := d.Checker.EffectiveRole(ctx, tx, in.Actor, c.ID)
				if err != nil {
					return err
				}
				if found && role.Allows(r, role.NodeMove) {
					picked = append(picked, c)
					if len(picked) > in.Limit {
						break
					}
				}
			}
			if len(page) < scanPage {
				break
			}
		}
		if len(picked) > in.Limit {
			picked = picked[:in.Limit]
			last := picked[len(picked)-1]
			out.NextCursor = encodeCursor(port.ChildCursor{NameKey: last.Name.Key(), ID: last.ID})
		}
		items, err := d.summarizeAll(ctx, tx, picked)
		if err != nil {
			return err
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return ListMoveTargetsOutput{}, err
	}
	return out, nil
}

// AccessRoot is a branch of the tree the actor can enter.
type AccessRoot struct {
	NodeID dnode.ID
	Name   string
	Type   dnode.Type
	Role   role.Role
}

// ListAccessRootsInput is the request of UC-N10.
type ListAccessRootsInput struct{ Actor user.ActorID }

// Validate checks the input without touching the store.
func (ListAccessRootsInput) Validate() error { return nil }

// ListAccessRootsOutput lists the access roots; several for a user with roles
// in unrelated branches.
type ListAccessRootsOutput struct{ Roots []AccessRoot }

// ListAccessRoots is UC-N10.
type ListAccessRoots struct{ d Deps }

// NewListAccessRoots builds the use case.
func NewListAccessRoots(d Deps) *ListAccessRoots { return &ListAccessRoots{d: d} }

// Execute returns the actor's access roots.
func (uc *ListAccessRoots) Execute(ctx context.Context, in ListAccessRootsInput) (ListAccessRootsOutput, error) {
	d := uc.d
	out := ListAccessRootsOutput{Roots: []AccessRoot{}}
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		roots, err := d.Nodes.AccessRoots(ctx, tx, in.Actor)
		if err != nil {
			return err
		}
		for _, r := range roots {
			out.Roots = append(out.Roots, AccessRoot{NodeID: r.Node.ID, Name: r.Node.Name.String(), Type: r.Node.Type, Role: r.Role})
		}
		return nil
	})
	if err != nil {
		return ListAccessRootsOutput{}, err
	}
	return out, nil
}

// GetPermissionsInput is the request of UC-N11.
type GetPermissionsInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
}

// Validate checks the input without touching the store.
func (in GetPermissionsInput) Validate() error { return requireID("node_id", in.NodeID) }

// GetPermissionsOutput is the actor's effective role on the node and what it allows.
type GetPermissionsOutput struct {
	Role        role.Role
	Permissions []role.Permission
}

// GetPermissions is UC-N11.
type GetPermissions struct{ d Deps }

// NewGetPermissions builds the use case.
func NewGetPermissions(d Deps) *GetPermissions { return &GetPermissions{d: d} }

// Execute returns the effective role and its permissions on the node.
func (uc *GetPermissions) Execute(ctx context.Context, in GetPermissionsInput) (GetPermissionsOutput, error) {
	if err := in.Validate(); err != nil {
		return GetPermissionsOutput{}, err
	}
	d := uc.d
	var out GetPermissionsOutput
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		r, err := authz.Require(ctx, d.Checker, tx, in.Actor, in.NodeID, role.NodeRead)
		if err != nil {
			return err
		}
		out.Role = r
		for _, p := range role.AllPermissions() {
			if role.Allows(r, p) {
				out.Permissions = append(out.Permissions, p)
			}
		}
		return nil
	})
	if err != nil {
		return GetPermissionsOutput{}, err
	}
	return out, nil
}
