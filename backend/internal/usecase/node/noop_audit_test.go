package node_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-ID: T-U-29
//
// EC-101: a change that changes nothing leaves no audit entry: rename to the
// same name, repeated deactivation and repeated activation, move under the
// current parent. The first real change does write one.
func TestNoopWritesNoAudit(t *testing.T) {
	w := newWorld(t)

	_, err := w.rename("adminA", "A1", "Renamed")
	require.NoError(t, err)
	_, err = w.setActive("adminA", "A1", false)
	require.NoError(t, err)
	require.Equal(t, []string{"node.renamed", "node.deactivated"}, w.audit.actions())

	w.audit.entries = nil
	_, err = w.rename("adminA", "A1", "Renamed")
	require.NoError(t, err)
	_, err = w.rename("adminA", "A1", "  Renamed\t")
	require.NoError(t, err)
	_, err = w.setActive("adminA", "A1", false)
	require.NoError(t, err)
	assert.Empty(t, w.audit.entries, "no-op rename or deactivation wrote an entry")

	_, err = w.setActive("adminA", "A1", true)
	require.NoError(t, err)
	w.audit.entries = nil
	_, err = w.setActive("adminA", "A1", true)
	require.NoError(t, err)
	assert.Empty(t, w.audit.entries, "repeated activation wrote an entry")

	_, err = w.move("owner", "A1", "A")
	require.NoError(t, err)
	assert.Empty(t, w.audit.entries, "move under the same parent wrote an entry")
}
