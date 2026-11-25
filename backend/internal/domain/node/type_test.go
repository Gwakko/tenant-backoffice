package node_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-01
func TestAllowedParents(t *testing.T) {
	types := []node.Type{node.TypePlatform, node.TypePartner, node.TypeCompany, node.TypeUnit}
	// allowed[parent][child]
	allowed := map[[2]node.Type]bool{
		{node.TypePlatform, node.TypePartner}: true,
		{node.TypePartner, node.TypeCompany}:  true,
		{node.TypeCompany, node.TypeUnit}:     true,
	}
	for _, parent := range types {
		for _, child := range types {
			want := allowed[[2]node.Type{parent, child}]
			t.Run(string(parent)+"/"+string(child), func(t *testing.T) {
				require.Equal(t, want, parent.CanHaveChild(child))

				p := build(t, nil, parent, "P")
				_, err := node.NewChild(p, newID(), child, mustName(t, "C"), testNow)
				if want {
					require.NoError(t, err)
				} else {
					requireCode(t, err, apperr.InvalidParentType) // EC-01, EC-02
				}
			})
		}
	}

	require.False(t, node.TypePlatform.CanHaveChild(node.Type("bogus")))
	require.False(t, node.Type("bogus").CanHaveChild(node.TypePartner))

	for _, c := range []struct {
		t      node.Type
		level  int
		parent node.Type
		hasPar bool
	}{
		{node.TypePlatform, 1, "", false},
		{node.TypePartner, 2, node.TypePlatform, true},
		{node.TypeCompany, 3, node.TypePartner, true},
		{node.TypeUnit, 4, node.TypeCompany, true},
	} {
		require.Equal(t, c.level, c.t.Level())
		p, ok := c.t.AllowedParent()
		require.Equal(t, c.hasPar, ok)
		require.Equal(t, c.parent, p)
	}
}

func TestParseType(t *testing.T) {
	for _, s := range []string{"platform", "partner", "company", "unit"} {
		got, err := node.ParseType(s)
		require.NoError(t, err)
		require.Equal(t, node.Type(s), got)
	}
	for _, s := range []string{"", "Platform", "tenant"} {
		_, err := node.ParseType(s)
		requireCode(t, err, apperr.InvalidNodeType)
	}
}

func TestNewChildBuildsPath(t *testing.T) {
	tr := newTree(t)
	id := newID()
	c, err := node.NewChild(tr.partner, id, node.TypeCompany, mustName(t, "X"), testNow)
	require.NoError(t, err)
	require.Equal(t, node.Path{tr.platform.ID, tr.partner.ID, id}, c.Path)
	require.Equal(t, tr.partner.ID, *c.ParentID)
	require.Equal(t, node.StatusActive, c.Status)
	require.Len(t, c.Path, c.Type.Level())
}

func TestNewChildUnderInactive(t *testing.T) {
	tr := newTree(t)
	p, _ := tr.partner.Deactivate(testNow)
	_, err := node.NewChild(p, newID(), node.TypeCompany, mustName(t, "X"), testNow)
	requireCode(t, err, apperr.NodeInactive)
}

func TestParseID(t *testing.T) {
	id := newID()
	got, err := node.ParseID(id.String())
	require.NoError(t, err)
	require.Equal(t, id, got)
	_, err = node.ParseID("nope")
	requireCode(t, err, apperr.InvalidID)
}
