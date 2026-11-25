package node_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func (w *world) del(actor, node string) error {
	_, err := uc.NewDeleteNode(w.deps).Execute(context.Background(), uc.DeleteNodeInput{
		Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node),
	})
	return err
}

// Test-ID: T-U-06
func TestDeleteRoot(t *testing.T) {
	w := newWorld(t)
	requireCode(t, w.del("owner", "root"), apperr.RootImmutable)
	assert.Zero(t, w.ev.count("delete"))
	assert.Empty(t, w.audit.entries)
}

// Test-ID: T-U-20
func TestDeleteRequiresParentPermission(t *testing.T) {
	// The owner of u1 only: the unit is visible, its parent is not deletable-from.
	t.Run("owner on the node only", func(t *testing.T) {
		w := newWorld(t)
		w.grant("nobody", "u1", role.Owner)
		requireCode(t, w.del("nobody", "u1"), apperr.Forbidden)
		w.untouched()
	})
	t.Run("admin on the parent lacks node.delete", func(t *testing.T) {
		w := newWorld(t)
		requireCode(t, w.del("adminA", "u1"), apperr.Forbidden)
		w.untouched()
	})
	t.Run("outside the subtree", func(t *testing.T) {
		w := newWorld(t)
		requireCode(t, w.del("adminB", "u1"), apperr.NotFound)
		w.untouched()
	})
}

func TestDeleteBlockers(t *testing.T) {
	cases := []struct {
		name  string
		node  string
		stats dnode.DeleteStats
		want  apperr.Code
	}{
		{"children (EC-14)", "A1", dnode.DeleteStats{}, apperr.NodeHasChildren},
		{"balance (EC-15)", "u1", dnode.DeleteStats{Balance: 1}, apperr.NodeHasBalance},
		{"history (EC-16)", "u1", dnode.DeleteStats{HasOperations: true}, apperr.NodeHasHistory},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.repo.stats[w.id(c.node)] = c.stats
			requireCode(t, w.del("owner", c.node), c.want)
			assert.Zero(t, w.ev.count("delete"))
			assert.Empty(t, w.audit.entries)
		})
	}
}

func TestDeleteEmptyNodeAuditsOnParent(t *testing.T) {
	w := newWorld(t)
	require.NoError(t, w.del("owner", "u1"))
	assert.NotContains(t, w.repo.nodes, w.id("u1"))
	w.requireBefore("lock", "delete")
	require.Equal(t, []string{"node.deleted"}, w.audit.actions())
	e := w.audit.entries[0]
	assert.Equal(t, w.id("A1"), e.NodeID, "the record goes on the parent")
	assert.Equal(t, w.id("u1").String(), e.Before["node_id"])
	assert.Equal(t, "unit", e.Before["type"])
}
