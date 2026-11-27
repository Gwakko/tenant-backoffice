package node_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func (w *world) move(actor, node, parent string) (uc.MoveNodeOutput, error) {
	return uc.NewMoveNode(w.deps).Execute(context.Background(), uc.MoveNodeInput{
		Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node), NewParentID: w.id(parent),
	})
}

// Test-ID: T-U-05
func TestMoveToSameParentIsNoop(t *testing.T) {
	w := newWorld(t)
	out, err := w.move("owner", "A1", "A")
	require.NoError(t, err)
	assert.Equal(t, w.id("A"), *out.Node.Node.ParentID)
	assert.Zero(t, w.ev.count("move"))
	assert.Empty(t, w.audit.entries)
}

// Test-ID: T-U-20
func TestMoveRequiresOldParentPermission(t *testing.T) {
	// EC-45: admin on the node and on the new parent, but not on the current
	// parent: the node is visible, so the answer is forbidden, not not_found.
	t.Run("no role on the old parent", func(t *testing.T) {
		w := newWorld(t)
		w.grant("adminA1", "B", role.Admin)
		_, err := w.move("adminA1", "A1", "B")
		requireCode(t, err, apperr.Forbidden)
		w.untouched()
	})
	t.Run("only a read role on the old parent", func(t *testing.T) {
		w := newWorld(t)
		w.grant("viewerA", "B", role.Admin)
		_, err := w.move("viewerA", "A1", "B")
		requireCode(t, err, apperr.Forbidden)
		w.untouched()
	})
	t.Run("new parent outside the subtree is not_found (EC-49)", func(t *testing.T) {
		w := newWorld(t)
		_, err := w.move("adminA", "A1", "B")
		requireCode(t, err, apperr.NotFound)
		w.untouched()
	})
	t.Run("new parent visible without the right", func(t *testing.T) {
		w := newWorld(t)
		w.grant("adminA", "B", role.Viewer)
		_, err := w.move("adminA", "A1", "B")
		requireCode(t, err, apperr.Forbidden)
		w.untouched()
	})
	t.Run("node outside the subtree is not_found", func(t *testing.T) {
		w := newWorld(t)
		_, err := w.move("adminB", "A1", "B")
		requireCode(t, err, apperr.NotFound)
		w.untouched()
	})
}

func TestMoveRewritesSubtreeUnderLockAndAudits(t *testing.T) {
	w := newWorld(t)
	out, err := w.move("owner", "A1", "B")
	require.NoError(t, err)
	assert.Equal(t, w.id("B"), *out.Node.Node.ParentID)
	assert.Equal(t, w.id("B"), w.stored("A1").Path[1])
	assert.Equal(t, w.stored("A1").Path.Append(w.id("u1")), w.stored("u1").Path, "descendants follow")
	w.requireBefore("lock", "move")
	// EC-98: three records, on the node and on both parents.
	require.Equal(t, []string{"node.moved", "node.child_moved_out", "node.child_moved_in"}, w.audit.actions())
	assert.Equal(t, w.id("A1"), w.audit.entries[0].NodeID)
	assert.Equal(t, w.id("A"), w.audit.entries[1].NodeID)
	assert.Equal(t, w.id("B"), w.audit.entries[2].NodeID)
	moved, out2, in2 := w.audit.entries[0], w.audit.entries[1], w.audit.entries[2]
	assert.Equal(t, map[string]string{"parent_id": w.id("A").String()}, moved.Before)
	assert.Equal(t, map[string]string{"parent_id": w.id("B").String()}, moved.After)
	// The old parent's branch learns the child and its name, never the new place.
	assert.Equal(t, map[string]string{"node_id": w.id("A1").String(), "name": "A1"}, out2.Before)
	assert.Empty(t, out2.After)
	assert.Equal(t, map[string]string{"node_id": w.id("A1").String(), "name": "A1"}, in2.After)
	assert.Empty(t, in2.Before)
	for _, e := range w.audit.entries {
		assert.Equal(t, uuid.UUID(w.id("A1")), e.TargetID)
		assert.Equal(t, w.actors["owner"], e.ActorID)
		assert.NotContains(t, e.Before, "new_parent_id")
	}
	assert.NotContains(t, out2.Before, "parent_id")
	assert.NotContains(t, out2.After, "parent_id")
	for _, v := range out2.Before {
		assert.NotEqual(t, w.id("B").String(), v)
	}
}

func TestMoveRefusals(t *testing.T) {
	cases := []struct {
		name         string
		node, parent string
		prepare      func(*world)
		want         apperr.Code
	}{
		{"into a descendant (EC-04)", "A", "A1", nil, apperr.MoveCycle},
		{"into itself (EC-05)", "A", "A", nil, apperr.MoveCycle},
		{"company under company (EC-06)", "A1", "B1", nil, apperr.InvalidParentType},
		{"under the root", "A1", "root", nil, apperr.InvalidParentType},
		{"inactive new parent (EC-13)", "A1", "B", func(w *world) { w.deactivate("B") }, apperr.NodeInactive},
		{"root (EC-08)", "root", "A", nil, apperr.RootImmutable},
		{"children limit (EC-26)", "A1", "B", func(w *world) { w.repo.children[w.id("B")] = dnode.MaxChildren }, apperr.ChildrenLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			if c.prepare != nil {
				c.prepare(w)
			}
			before := w.stored(c.node)
			_, err := w.move("owner", c.node, c.parent)
			requireCode(t, err, c.want)
			assert.Zero(t, w.ev.count("move"), "nothing is written when the plan is refused")
			assert.Empty(t, w.audit.entries)
			assert.Equal(t, before.Path, w.stored(c.node).Path)
		})
	}
}
