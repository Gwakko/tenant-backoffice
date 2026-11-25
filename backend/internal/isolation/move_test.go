//go:build integration

package isolation_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	nodeuc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func (a *app) moveAs(ctx context.Context, actor, node, parent uuid.UUID) error {
	_, err := a.move.Execute(ctx, nodeuc.MoveNodeInput{Actor: actorOf(actor), RequestID: "t", NodeID: nid(node), NewParentID: nid(parent)})
	return err
}

// Test-ID: T-ISO-04
func TestMoveAcrossBranches(t *testing.T) {
	ctx, db, w, a := setup(t)
	notFound := apperr.NotFoundError("node")
	missing := uuid.Must(uuid.NewV7())

	cases := []struct {
		name         string
		actor        uuid.UUID
		node, parent uuid.UUID
		want         error
	}{
		{"own company under a foreign partner", w.AdminA, w.A1, w.B, notFound},
		{"own company under a missing partner (same answer)", w.AdminA, w.A1, missing, notFound},
		{"own unit under a foreign company", w.AdminA, w.A1u1, w.B1, notFound},
		{"foreign company under an own partner", w.AdminA, w.B1, w.A, notFound},
		{"foreign unit under an own company", w.AdminA, w.B1u1, w.A1, notFound},
		{"missing node under an own partner (same answer)", w.AdminA, missing, w.A, notFound},
		{"foreign company under a foreign partner", w.AdminA, w.B1, w.B, notFound},
		{"the other way: own company under the foreign partner", w.AdminB, w.B1, w.A, notFound},
		{"the other way: foreign company under an own partner", w.AdminB, w.A1, w.B, notFound},
		{"under the root, which the actor cannot see", w.AdminA, w.A1, w.Root, notFound},
		// Multi holds admin on A1 and only viewer on B1: B1 is visible, so the
		// answer is the honest "no right", and still nothing moves.
		{"role in both branches, no right on the target", w.Multi, w.A1u1, w.B1, apperr.ForbiddenError("the role does not allow this action")},
	}
	before := isolation.Snapshot(ctx, t, db)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, a.moveAs(ctx, c.actor, c.node, c.parent))
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "a path changed")
		})
	}
	assert.Empty(t, a.audit.all())

	// The answers for a foreign and for a missing counterpart are the same value.
	assert.Equal(t, a.moveAs(ctx, w.AdminA, w.A1, missing), a.moveAs(ctx, w.AdminA, w.A1, w.B))
	assert.Equal(t, a.moveAs(ctx, w.AdminA, missing, w.A), a.moveAs(ctx, w.AdminA, w.B1, w.A))

	t.Run("target list of an own node never offers the foreign branch", func(t *testing.T) {
		out, err := a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1), Limit: 50})
		require.NoError(t, err)
		assert.Empty(t, out.Items)
		_, err = a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.B1), Limit: 50})
		assert.Equal(t, notFound, err)
	})
}

// Test-ID: T-ISO-18
func TestCrossBranchMoveByRootOwner(t *testing.T) {
	ctx, db, w, a := setup(t)
	notFound := apperr.NotFoundError("node")
	_, randomErr := a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(w.AdminA), NodeID: nid(uuid.New())})

	canGet := func(actor, id uuid.UUID) error {
		_, err := a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(actor), NodeID: nid(id)})
		return err
	}
	children := func(actor, parent uuid.UUID) []uuid.UUID {
		out, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(actor), NodeID: nid(parent), Limit: 50})
		require.NoError(t, err)
		var ids []uuid.UUID
		for _, it := range out.Items {
			ids = append(ids, uuid.UUID(it.ID))
		}
		return ids
	}

	// Before: A sees A1 and its units, B does not.
	require.NoError(t, canGet(w.AdminA, w.A1))
	assert.Equal(t, randomErr, canGet(w.AdminB, w.A1))
	assert.Equal(t, []uuid.UUID{w.A1}, children(w.AdminA, w.A))

	// The owner of the common ancestor moves company A1 from A to B.
	require.NoError(t, a.moveAs(ctx, w.OwnerRoot, w.A1, w.B))

	// After the commit: the branch of A lost the company and everything below it.
	for _, id := range []uuid.UUID{w.A1, w.A1u1, w.A1u2} {
		assert.Equal(t, randomErr, canGet(w.AdminA, id), "admin of A must not see the moved node")
		assert.Equal(t, notFound, canGet(w.AdminA, id))
	}
	assert.Empty(t, children(w.AdminA, w.A))
	_, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1), Limit: 50})
	assert.Equal(t, randomErr, err)
	mems, err := a.listMems.Execute(ctx, memberListInput(w.AdminA, w.A1, true))
	assert.Equal(t, notFound, err)
	assert.Empty(t, mems.Items)
	for _, o := range writeOps(w.A) {
		res, err := o.run(ctx, a, w.AdminA, w.A1)
		requireNotFound(t, res, err, "admin of A: "+o.name+" on the moved company")
	}

	// B received it.
	require.NoError(t, canGet(w.AdminB, w.A1))
	require.NoError(t, canGet(w.AdminB, w.A1u1))
	assert.ElementsMatch(t, []uuid.UUID{w.A1, w.B1}, children(w.AdminB, w.B))
	out, err := a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(w.AdminB), NodeID: nid(w.A1u1)})
	require.NoError(t, err)
	var crumbs []uuid.UUID
	for _, c := range out.Node.Breadcrumbs {
		crumbs = append(crumbs, uuid.UUID(c.ID))
	}
	assert.Equal(t, []uuid.UUID{w.B, w.A1, w.A1u1}, crumbs, "the path now runs through B")

	// Roles attached to nodes of the moved subtree travel with them: Multi (admin
	// of A1) and the viewer of A1u1 keep their own assignments.
	require.NoError(t, canGet(w.Multi, w.A1))
	require.NoError(t, canGet(w.ViewerA1u1, w.A1u1))
	assert.Equal(t, notFound, canGet(w.ViewerA1u1, w.A1), "a role on a unit does not reach its parent")

	// The old branch is intact and the stored paths are the new ones.
	var path string
	require.NoError(t, db.Owner.QueryRow(ctx, `SELECT path::text FROM nodes WHERE id = $1`, w.A1u1).Scan(&path))
	assert.Equal(t, strings.Join([]string{isolation.Label(w.Root), isolation.Label(w.B), isolation.Label(w.A1), isolation.Label(w.A1u1)}, "."), path)
	assert.Equal(t, notFound, a.moveAs(ctx, w.AdminA, w.A1, w.A), "A can no longer move it back")
	assert.NoError(t, a.moveAs(ctx, w.AdminB, w.A1, w.B), "B owns it now: moving under its current parent is a no-op, not a refusal")

	// Audit: the entry in A (child_moved_out) carries the moved node, never the new parent.
	var moved []string
	for _, e := range a.audit.all() {
		if e.Action == "node.child_moved_out" {
			assert.Equal(t, w.A, uuid.UUID(e.NodeID))
			text := fmt.Sprintf("%v %v", e.Before, e.After)
			assert.NotContains(t, text, w.B.String(), "the audit of the old parent must not name the new parent")
			assert.NotContains(t, text, isolation.Label(w.B))
			moved = append(moved, text)
		}
	}
	assert.Len(t, moved, 1)
}
