package node_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-09
func TestEffectiveActive(t *testing.T) {
	tr := newTree(t)
	require.True(t, tr.unit.EffectiveActive())

	// Own status inactive.
	u, _ := tr.unit.Deactivate(testNow)
	require.False(t, u.EffectiveActive())

	// Active unit under an inactive company: EC-20.
	company, _ := tr.company.Deactivate(testNow)
	unit := build(t, &company, node.TypeUnit, "Unit")
	require.False(t, unit.AncestorsActive)
	unit, _ = unit.Deactivate(testNow)
	unit, changed := unit.Activate(testNow)
	require.True(t, changed)
	require.Equal(t, node.StatusActive, unit.Status)
	require.False(t, unit.EffectiveActive())

	// Creating a child under it is refused.
	_, err := node.NewChild(company, newID(), node.TypeUnit, mustName(t, "N"), testNow)
	require.Error(t, err)
}
