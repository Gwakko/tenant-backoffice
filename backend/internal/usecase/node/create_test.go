package node_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func (w *world) create(actor, parent, typ, name string) (uc.CreateNodeOutput, error) {
	return uc.NewCreateNode(w.deps).Execute(context.Background(), uc.CreateNodeInput{
		Actor: w.actors[actor], RequestID: "r1", ParentID: w.id(parent), Type: typ, Name: name,
	})
}

// Test-ID: T-U-02
func TestCreatePlatformRejected(t *testing.T) {
	w := newWorld(t)
	for _, parent := range []string{"root", "A", "A1"} {
		_, err := w.create("owner", parent, "platform", "second root")
		requireCode(t, err, apperr.InvalidParentType)
	}
	assert.Zero(t, w.ev.count("insert"))
	assert.Empty(t, w.audit.entries)
}

func TestCreateWritesNodeUnderLockAndAudits(t *testing.T) {
	w := newWorld(t)
	out, err := w.create("adminA", "A", "company", "  New   Co ")
	require.NoError(t, err)
	n := out.Node.Node
	assert.Equal(t, "New Co", n.Name.String())
	assert.Equal(t, dnode.StatusActive, n.Status)
	assert.Equal(t, w.id("A"), *n.ParentID)
	assert.True(t, out.Node.EffectiveActive)
	assert.Equal(t, 0, out.Node.ChildrenCount)
	w.requireBefore("lock", "insert")
	require.Equal(t, []string{"node.created"}, w.audit.actions())
	assert.Equal(t, n.ID, w.audit.entries[0].NodeID)
	assert.Equal(t, "New Co", w.audit.entries[0].After["name"])
	assert.Equal(t, "r1", w.audit.entries[0].RequestID)
}

func TestCreateAuthorization(t *testing.T) {
	cases := []struct {
		actor string
		want  apperr.Code
	}{
		{"viewerA", apperr.Forbidden},
		{"nobody", apperr.NotFound},
		{"adminB", apperr.NotFound},  // outside the subtree: same as a missing parent
		{"adminA1", apperr.NotFound}, // A is above his access root
	}
	for _, c := range cases {
		t.Run(c.actor, func(t *testing.T) {
			w := newWorld(t)
			_, err := w.create(c.actor, "A", "company", "x")
			requireCode(t, err, c.want)
			w.untouched()
		})
	}
}

func TestCreateRules(t *testing.T) {
	t.Run("type not allowed under the parent", func(t *testing.T) {
		w := newWorld(t)
		_, err := w.create("owner", "A", "unit", "x")
		requireCode(t, err, apperr.InvalidParentType)
		assert.Zero(t, w.ev.count("insert"))
	})
	t.Run("inactive parent", func(t *testing.T) {
		w := newWorld(t)
		w.deactivate("A")
		_, err := w.create("owner", "A1", "unit", "x")
		requireCode(t, err, apperr.NodeInactive)
		assert.Zero(t, w.ev.count("insert"))
	})
	// EC-26
	t.Run("children limit", func(t *testing.T) {
		w := newWorld(t)
		w.repo.children[w.id("A1")] = dnode.MaxChildren
		_, err := w.create("owner", "A1", "unit", "x")
		requireCode(t, err, apperr.ChildrenLimit)
		assert.Zero(t, w.ev.count("insert"))
	})
	t.Run("invalid input is refused before any store access", func(t *testing.T) {
		w := newWorld(t)
		_, err := w.create("owner", "A", "company", "   ")
		requireCode(t, err, apperr.ValidationFailed)
		_, err = w.create("owner", "A", "galaxy", "x")
		requireCode(t, err, apperr.ValidationFailed)
		w.untouched()
	})
}
