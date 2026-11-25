package node_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-10
func TestDeactivateTwice(t *testing.T) {
	tr := newTree(t)
	later := testNow.Add(time.Hour)

	n, changed := tr.company.Deactivate(later)
	require.True(t, changed)
	require.Equal(t, node.StatusInactive, n.Status)
	require.Equal(t, later, n.UpdatedAt)

	again, changed := n.Deactivate(later.Add(time.Hour))
	require.False(t, changed) // EC-19
	require.Equal(t, n, again)

	act, changed := n.Activate(later.Add(time.Hour))
	require.True(t, changed)
	require.Equal(t, node.StatusActive, act.Status)

	same, changed := act.Activate(later.Add(2 * time.Hour))
	require.False(t, changed) // EC-19
	require.Equal(t, act, same)

	// The original value is not mutated.
	require.Equal(t, node.StatusActive, tr.company.Status)
}

// EC-24: renaming to the same normalized name changes nothing.
func TestRenameSameNameIsUnchanged(t *testing.T) {
	tr := newTree(t)
	same, changed := tr.unit.Rename(mustName(t, "  Unit "), testNow.Add(time.Hour))
	require.False(t, changed)
	require.Equal(t, tr.unit, same)

	renamed, changed := tr.unit.Rename(mustName(t, "Other"), testNow.Add(time.Hour))
	require.True(t, changed)
	require.Equal(t, "Other", renamed.Name.String())
	require.Equal(t, testNow.Add(time.Hour), renamed.UpdatedAt)
}
