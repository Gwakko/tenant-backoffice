package node_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func (w *world) rename(actor, node, name string) (uc.RenameNodeOutput, error) {
	return uc.NewRenameNode(w.deps).Execute(context.Background(), uc.RenameNodeInput{
		Actor: w.actors[actor], RequestID: "r1", NodeID: w.id(node), Name: name,
	})
}

// Test-ID: T-U-13
func TestRenameSameNameNoop(t *testing.T) {
	w := newWorld(t)
	out, err := w.rename("owner", "A1", "  a1 ") // differs only in case: a different name
	require.NoError(t, err)
	assert.Equal(t, "a1", out.Node.Node.Name.String())
	w.ev.list, w.audit.entries = nil, nil

	out, err = w.rename("owner", "A1", "a1\t")
	require.NoError(t, err)
	assert.Equal(t, "a1", out.Node.Node.Name.String())
	w.untouched()
}

func TestRenameWritesAudit(t *testing.T) {
	w := newWorld(t)
	_, err := w.rename("adminA", "A1", "Renamed")
	require.NoError(t, err)
	assert.Equal(t, "Renamed", w.stored("A1").Name.String())
	require.Equal(t, []string{"node.renamed"}, w.audit.actions())
	assert.Equal(t, "A1", w.audit.entries[0].Before["name"])
	assert.Equal(t, "Renamed", w.audit.entries[0].After["name"])
}

func TestRenameAuthorization(t *testing.T) {
	w := newWorld(t)
	_, err := w.rename("viewerA", "A1", "x")
	requireCode(t, err, apperr.Forbidden)
	_, err = w.rename("adminB", "A1", "x")
	requireCode(t, err, apperr.NotFound)
	w.untouched()
}
