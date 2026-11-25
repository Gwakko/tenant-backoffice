//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func requireApp(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

// Test-ID: T-I-02
func TestMoveCycleRejectedOnDB(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	before := w.snapshot()

	requireApp(t, w.moveAs(c, "ownerRoot", "A", "A1"), apperr.MoveCycle) // into a descendant
	requireApp(t, w.moveAs(c, "ownerRoot", "A", "A"), apperr.MoveCycle)  // into itself

	assert.Equal(t, before, w.snapshot(), "paths are unchanged")
	assert.Empty(t, w.violations())
	assert.Empty(t, c.audit.actions())
}

// Test-ID: T-I-03
func TestMoveRewritesSubtreePaths(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	require.NoError(t, w.moveAs(c, "ownerRoot", "A1", "B"))

	bPath, _ := w.pathOf(w.id("B"))
	a1Path, a1Parent := w.pathOf(w.id("A1"))
	require.NotNil(t, a1Parent)
	assert.Equal(t, w.id("B").String(), *a1Parent)
	assert.Equal(t, bPath+"."+strings.ReplaceAll(w.id("A1").String(), "-", ""), a1Path)
	for _, u := range []string{"A1u1", "A1u2"} {
		p, parent := w.pathOf(w.id(u))
		assert.Equal(t, a1Path+"."+strings.ReplaceAll(w.id(u).String(), "-", ""), p, u)
		assert.Equal(t, w.id("A1").String(), *parent, u)
	}
	assert.Empty(t, w.violations(), "nlevel matches the type level everywhere")
	assert.Equal(t, []string{"node.moved", "node.child_moved_out", "node.child_moved_in"}, c.audit.actions())

	// The new view: breadcrumbs go through the new parent.
	out, err := c.get.Execute(context.Background(), uc.GetNodeInput{Actor: w.users["ownerRoot"], NodeID: w.id("A1")})
	require.NoError(t, err)
	require.Len(t, out.Node.Breadcrumbs, 3)
	assert.Equal(t, w.id("B"), out.Node.Breadcrumbs[1].ID)
	assert.Equal(t, 2, out.Node.ChildrenCount)
}

// Test-ID: T-I-10
func TestMoveNameConflict(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	// B already has a company with the same name key as A1 (different case).
	_, err := c.create.Execute(context.Background(), uc.CreateNodeInput{
		Actor: w.users["ownerRoot"], ParentID: w.id("B"), Type: "company", Name: "a1",
	})
	require.NoError(t, err)
	c.audit.reset()
	before := w.snapshot()

	requireApp(t, w.moveAs(c, "ownerRoot", "A1", "B"), apperr.NameTaken)
	assert.Equal(t, before, w.snapshot())
	assert.Empty(t, c.audit.actions())
	assert.Empty(t, w.violations())
}

// EC-45 with real visibility: "multi" is admin on A1, which is their access
// root, so its parent A is invisible; the node is visible, the answer is 403.
// Test-ID: T-I-60
func TestMoveByAccessRootIsForbidden(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	w.grant(context.Background(), "multi", "B", "admin")
	before := w.snapshot()
	requireApp(t, w.moveAs(c, "multi", "A1", "B"), apperr.Forbidden)
	assert.Equal(t, before, w.snapshot())

	// A node of the other branch is not_found, the same as a missing one.
	requireApp(t, w.moveAs(c, "adminB", "A1", "B"), apperr.NotFound)
	_, err := c.get.Execute(context.Background(), uc.GetNodeInput{Actor: w.users["adminB"], NodeID: node.ID(uuid.New())})
	requireApp(t, err, apperr.NotFound)
}

// The other half of the open question of M2.3: an access root whose hidden
// ancestor is inactive is not effectively active after being activated.
// Test-ID: T-I-61
func TestActivateAccessRootUnderInactiveAncestor(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	ctx := context.Background()
	owner, multi := w.users["ownerRoot"], w.users["multi"]

	_, err := c.deact.Execute(ctx, uc.SetStatusInput{Actor: multi, NodeID: w.id("A1")})
	require.NoError(t, err)
	_, err = c.deact.Execute(ctx, uc.SetStatusInput{Actor: owner, NodeID: w.id("A")})
	require.NoError(t, err)

	out, err := c.act.Execute(ctx, uc.SetStatusInput{Actor: multi, NodeID: w.id("A1")})
	require.NoError(t, err)
	assert.Equal(t, node.StatusActive, out.Node.Node.Status)
	assert.False(t, out.Node.EffectiveActive, "the hidden ancestor A is inactive")
	require.Len(t, out.Node.Breadcrumbs, 1, "breadcrumbs start at the access root")

	_, err = c.act.Execute(ctx, uc.SetStatusInput{Actor: owner, NodeID: w.id("A")})
	require.NoError(t, err)
	got, err := c.get.Execute(ctx, uc.GetNodeInput{Actor: multi, NodeID: w.id("A1")})
	require.NoError(t, err)
	assert.True(t, got.Node.EffectiveActive)
}

// Test-ID: T-I-62
func TestNodeReadsOnDB(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	ctx := context.Background()

	roots, err := c.roots.Execute(ctx, uc.ListAccessRootsInput{Actor: w.users["multi"]})
	require.NoError(t, err)
	require.Len(t, roots.Roots, 2) // EC-32
	assert.Equal(t, w.id("A1"), roots.Roots[0].NodeID)
	assert.Equal(t, w.id("B1"), roots.Roots[1].NodeID)

	none, err := c.roots.Execute(ctx, uc.ListAccessRootsInput{Actor: w.users["orphan"]})
	require.NoError(t, err)
	assert.Empty(t, none.Roots)

	kids, err := c.list.Execute(ctx, uc.ListChildrenInput{Actor: w.users["adminA"], NodeID: w.id("A1"), Limit: 1})
	require.NoError(t, err)
	require.Len(t, kids.Items, 1)
	assert.NotEmpty(t, kids.NextCursor)
	rest, err := c.list.Execute(ctx, uc.ListChildrenInput{Actor: w.users["adminA"], NodeID: w.id("A1"), Limit: 5, Cursor: kids.NextCursor})
	require.NoError(t, err)
	require.Len(t, rest.Items, 1)
	assert.Empty(t, rest.NextCursor)

	// Move targets of A1 for adminA: only partners where adminA may move; B is
	// invisible to them and A is the current parent, so none.
	tg, err := c.target.Execute(ctx, uc.ListMoveTargetsInput{Actor: w.users["adminA"], NodeID: w.id("A1"), Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, tg.Items)
	tg, err = c.target.Execute(ctx, uc.ListMoveTargetsInput{Actor: w.users["ownerRoot"], NodeID: w.id("A1"), Limit: 10})
	require.NoError(t, err)
	require.Len(t, tg.Items, 1)
	assert.Equal(t, w.id("B"), tg.Items[0].ID)
}
