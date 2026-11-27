package member_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-ID: T-U-29
//
// EC-101: giving a member the role they already have, by either use case,
// leaves no audit entry; a real change does.
func TestNoopWritesNoAudit(t *testing.T) {
	w := newWorld(t)

	_, err := w.add("adminA", "A1", "member@example.test", "viewer", "")
	require.NoError(t, err)
	_, err = w.change("adminA", "A", "viewerA", "viewer")
	require.NoError(t, err)
	assert.Empty(t, w.audit.entries, "same role wrote an entry")

	_, err = w.change("adminA", "A", "viewerA", "analyst")
	require.NoError(t, err)
	assert.Equal(t, []string{"member.changed"}, w.audit.actions())
}
