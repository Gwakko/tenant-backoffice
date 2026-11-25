package node_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// TestOutsideSubtreeIsNotFound runs every use case that takes a node id as an
// actor with no role on it: the answer is the one for a missing node.
func TestOutsideSubtreeIsNotFound(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(w *world, actor user.ActorID, id dnode.ID) error{
		"create": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewCreateNode(w.deps).Execute(ctx, uc.CreateNodeInput{Actor: a, ParentID: id, Type: "unit", Name: "x"})
			return err
		},
		"rename": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewRenameNode(w.deps).Execute(ctx, uc.RenameNodeInput{Actor: a, NodeID: id, Name: "x"})
			return err
		},
		"move": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewMoveNode(w.deps).Execute(ctx, uc.MoveNodeInput{Actor: a, NodeID: id, NewParentID: w.id("B")})
			return err
		},
		"deactivate": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewDeactivateNode(w.deps).Execute(ctx, uc.SetStatusInput{Actor: a, NodeID: id})
			return err
		},
		"activate": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewActivateNode(w.deps).Execute(ctx, uc.SetStatusInput{Actor: a, NodeID: id})
			return err
		},
		"delete": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewDeleteNode(w.deps).Execute(ctx, uc.DeleteNodeInput{Actor: a, NodeID: id})
			return err
		},
		"get": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewGetNode(w.deps).Execute(ctx, uc.GetNodeInput{Actor: a, NodeID: id})
			return err
		},
		"list children": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewListChildren(w.deps).Execute(ctx, uc.ListChildrenInput{Actor: a, NodeID: id, Limit: 10})
			return err
		},
		"list move targets": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewListMoveTargets(w.deps).Execute(ctx, uc.ListMoveTargetsInput{Actor: a, NodeID: id, Limit: 10})
			return err
		},
		"permissions": func(w *world, a user.ActorID, id dnode.ID) error {
			_, err := uc.NewGetPermissions(w.deps).Execute(ctx, uc.GetPermissionsInput{Actor: a, NodeID: id})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			// A1 exists but "nobody" has no role; the random id does not exist.
			hidden := call(w, w.actors["nobody"], w.id("A1"))
			missing := call(w, w.actors["owner"], dnode.ID{1})
			requireCode(t, hidden, apperr.NotFound)
			requireCode(t, missing, apperr.NotFound)
			assert.Equal(t, missing.Error(), hidden.Error(), "indistinguishable")
			w.untouched()
		})
	}
}

func TestGetNodeBreadcrumbsStartAtAccessRoot(t *testing.T) {
	w := newWorld(t)
	// The fake lists the whole path; the real repository starts at the access
	// root. Here only the shape of the view is checked.
	out, err := uc.NewGetNode(w.deps).Execute(context.Background(), uc.GetNodeInput{Actor: w.actors["owner"], NodeID: w.id("A1")})
	require.NoError(t, err)
	assert.Equal(t, 2, out.Node.ChildrenCount)
	require.Len(t, out.Node.Breadcrumbs, 3)
	assert.Equal(t, w.id("A1"), out.Node.Breadcrumbs[2].ID)
	assert.True(t, out.Node.EffectiveActive)
}

func TestListChildren(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	for i := 0; i < 5; i++ {
		w.add(fmt.Sprintf("k%d", i), dnode.TypeUnit, "B1")
	}
	w.deactivate("k1")
	list := uc.NewListChildren(w.deps)

	// EC-28: no children, empty list, no cursor.
	empty, err := list.Execute(ctx, uc.ListChildrenInput{Actor: w.actors["owner"], NodeID: w.id("u1"), Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, empty.Items)
	assert.NotNil(t, empty.Items)
	assert.Empty(t, empty.NextCursor)

	var seen []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		out, err := list.Execute(ctx, uc.ListChildrenInput{Actor: w.actors["owner"], NodeID: w.id("B1"), Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, it := range out.Items {
			seen = append(seen, it.Name)
		}
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	assert.Equal(t, []string{"k0", "k1", "k2", "k3", "k4"}, seen)

	inactive, err := list.Execute(ctx, uc.ListChildrenInput{Actor: w.actors["owner"], NodeID: w.id("B1"), Limit: 10, Status: "inactive"})
	require.NoError(t, err)
	require.Len(t, inactive.Items, 1)
	assert.Equal(t, "k1", inactive.Items[0].Name)
	assert.False(t, inactive.Items[0].EffectiveActive)

	byName, err := list.Execute(ctx, uc.ListChildrenInput{Actor: w.actors["owner"], NodeID: w.id("B1"), Limit: 10, Query: " K3 "})
	require.NoError(t, err)
	require.Len(t, byName.Items, 1)
}

func TestListChildrenValidation(t *testing.T) {
	w := newWorld(t)
	list := uc.NewListChildren(w.deps)
	bad := []uc.ListChildrenInput{
		{Actor: w.actors["owner"], NodeID: w.id("A"), Limit: 0},
		{Actor: w.actors["owner"], NodeID: w.id("A"), Limit: 201},
		{Actor: w.actors["owner"], NodeID: w.id("A"), Limit: 10, Status: "deleted"},
		{Actor: w.actors["owner"], NodeID: w.id("A"), Limit: 10, Cursor: "!!!"},
		{Actor: w.actors["owner"], NodeID: w.id("A"), Limit: 10, Query: string(make([]rune, 101))},
		{Actor: w.actors["owner"], Limit: 10},
	}
	for i, in := range bad {
		_, err := list.Execute(context.Background(), in)
		requireCode(t, err, apperr.ValidationFailed)
		assert.Error(t, in.Validate(), i)
	}
	w.untouched()
}

func TestListMoveTargets(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.add("C", dnode.TypePartner, "root")
	w.add("D", dnode.TypePartner, "root")
	w.deactivate("D")
	w.grant("adminA", "C", role.Viewer) // visible, but no node.move
	w.grant("adminA", "B", role.Admin)

	out, err := uc.NewListMoveTargets(w.deps).Execute(ctx, uc.ListMoveTargetsInput{Actor: w.actors["adminA"], NodeID: w.id("A1"), Limit: 10})
	require.NoError(t, err)
	// A is the current parent, C gives no node.move, D is inactive: only B stays.
	require.Len(t, out.Items, 1)
	assert.Equal(t, w.id("B"), out.Items[0].ID)

	root, err := uc.NewListMoveTargets(w.deps).Execute(ctx, uc.ListMoveTargetsInput{Actor: w.actors["owner"], NodeID: w.id("root"), Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, root.Items)
}

func TestListAccessRoots(t *testing.T) {
	w := newWorld(t)
	a := w.actors["adminA1"]
	w.repo.roots[a] = []port.AccessRoot{
		{Node: w.nodes["A1"], Role: role.Admin},
		{Node: w.nodes["B1"], Role: role.Viewer},
	}
	out, err := uc.NewListAccessRoots(w.deps).Execute(context.Background(), uc.ListAccessRootsInput{Actor: a})
	require.NoError(t, err)
	require.Len(t, out.Roots, 2) // EC-32
	assert.Equal(t, role.Viewer, out.Roots[1].Role)

	none, err := uc.NewListAccessRoots(w.deps).Execute(context.Background(), uc.ListAccessRootsInput{Actor: w.actors["nobody"]})
	require.NoError(t, err)
	assert.Empty(t, none.Roots)
}

func TestGetPermissions(t *testing.T) {
	w := newWorld(t)
	out, err := uc.NewGetPermissions(w.deps).Execute(context.Background(), uc.GetPermissionsInput{Actor: w.actors["adminA"], NodeID: w.id("u1")})
	require.NoError(t, err)
	assert.Equal(t, role.Admin, out.Role, "inherited from A")
	assert.Contains(t, out.Permissions, role.NodeMove)
	assert.NotContains(t, out.Permissions, role.NodeDelete)
}
