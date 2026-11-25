package role_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
)

func nid() node.ID { return node.ID(uuid.New()) }

func assign(n node.ID, r role.Role) role.Assignment { return role.Assignment{NodeID: n, Role: r} }

// Test-ID: T-U-14
func TestMaxRankWins(t *testing.T) {
	root, p, c := nid(), nid(), nid()
	path := node.Path{root, p, c}
	tests := []struct {
		name string
		in   []role.Assignment
		want role.Role
	}{
		{"viewer on node, admin on ancestor (EC-29)", []role.Assignment{assign(c, role.Viewer), assign(p, role.Admin)}, role.Admin},
		{"admin on node, viewer on ancestor", []role.Assignment{assign(c, role.Admin), assign(p, role.Viewer)}, role.Admin},
		{"order independent", []role.Assignment{assign(p, role.Admin), assign(c, role.Viewer)}, role.Admin},
		{"owner on root inherited", []role.Assignment{assign(root, role.Owner)}, role.Owner},
		{"equal ranks", []role.Assignment{assign(root, role.Analyst), assign(c, role.Analyst)}, role.Analyst},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := role.Effective(path, tc.in)
			require.True(t, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

// Test-ID: T-U-15
func TestNoUpwardNoSideways(t *testing.T) {
	root, p, c1, c2, gc := nid(), nid(), nid(), nid(), nid()
	pathP := node.Path{root, p}
	pathC2 := node.Path{root, p, c2}

	// Admin on a descendant gives nothing on the parent (EC-30).
	_, ok := role.Effective(pathP, []role.Assignment{assign(c1, role.Admin)})
	require.False(t, ok)
	_, ok = role.Effective(pathP, []role.Assignment{assign(gc, role.Owner)})
	require.False(t, ok)

	// Admin on a sibling gives nothing on the other sibling (EC-31).
	_, ok = role.Effective(pathC2, []role.Assignment{assign(c1, role.Admin)})
	require.False(t, ok)

	// No assignments at all: not visible.
	_, ok = role.Effective(pathC2, nil)
	require.False(t, ok)

	// Unknown role values never count.
	_, ok = role.Effective(pathC2, []role.Assignment{assign(c2, role.Role("god"))})
	require.False(t, ok)
}
