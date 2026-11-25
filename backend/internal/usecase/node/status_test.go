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

func (w *world) setActive(actor, node string, active bool) (uc.SetStatusOutput, error) {
	in := uc.SetStatusInput{Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node)}
	if active {
		return uc.NewActivateNode(w.deps).Execute(context.Background(), in)
	}
	return uc.NewDeactivateNode(w.deps).Execute(context.Background(), in)
}

func TestDeactivateActivate(t *testing.T) {
	w := newWorld(t)
	out, err := w.setActive("adminA", "A1", false)
	require.NoError(t, err)
	assert.Equal(t, dnode.StatusInactive, out.Node.Node.Status)
	assert.False(t, out.Node.EffectiveActive)
	require.Equal(t, []string{"node.deactivated"}, w.audit.actions())

	// EC-19: repeating changes nothing and writes nothing.
	w.ev.list, w.audit.entries = nil, nil
	_, err = w.setActive("adminA", "A1", false)
	require.NoError(t, err)
	w.untouched()

	out, err = w.setActive("adminA", "A1", true)
	require.NoError(t, err)
	assert.True(t, out.Node.EffectiveActive)
	assert.Equal(t, []string{"node.activated"}, w.audit.actions())
}

// EC-20
func TestActivateUnderInactiveAncestor(t *testing.T) {
	w := newWorld(t)
	w.deactivate("A1")
	w.deactivate("u1")
	out, err := w.setActive("owner", "u1", true)
	require.NoError(t, err)
	assert.Equal(t, dnode.StatusActive, out.Node.Node.Status)
	assert.False(t, out.Node.EffectiveActive)
}

func TestDeactivateRootAndAuthorization(t *testing.T) {
	w := newWorld(t)
	_, err := w.setActive("owner", "root", false)
	requireCode(t, err, apperr.RootImmutable)
	_, err = w.setActive("viewerA", "A1", false)
	requireCode(t, err, apperr.Forbidden)
	_, err = w.setActive("adminB", "A1", false)
	requireCode(t, err, apperr.NotFound)
	assert.Zero(t, w.ev.count("update"))
	assert.Empty(t, w.audit.entries)
}
