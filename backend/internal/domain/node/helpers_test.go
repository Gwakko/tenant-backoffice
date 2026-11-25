package node_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newID() node.ID { return node.ID(uuid.New()) }

func mustName(t *testing.T, s string) node.Name {
	t.Helper()
	n, err := node.ParseName(s)
	require.NoError(t, err)
	return n
}

// build creates a node of type typ under parent (zero Node for the root).
func build(t *testing.T, parent *node.Node, typ node.Type, name string) node.Node {
	t.Helper()
	n := node.Node{
		ID:              newID(),
		Type:            typ,
		Name:            mustName(t, name),
		Status:          node.StatusActive,
		AncestorsActive: true,
		CreatedAt:       testNow,
		UpdatedAt:       testNow,
	}
	if parent == nil {
		n.Path = node.Path{n.ID}
		return n
	}
	pid := parent.ID
	n.ParentID = &pid
	n.Path = parent.Path.Append(n.ID)
	n.AncestorsActive = parent.EffectiveActive()
	return n
}

// tree is a full four-level chain.
type tree struct{ platform, partner, company, unit node.Node }

func newTree(t *testing.T) tree {
	t.Helper()
	var tr tree
	tr.platform = build(t, nil, node.TypePlatform, "Root")
	tr.partner = build(t, &tr.platform, node.TypePartner, "Partner")
	tr.company = build(t, &tr.partner, node.TypeCompany, "Company")
	tr.unit = build(t, &tr.company, node.TypeUnit, "Unit")
	return tr
}

func requireCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "expected apperr.Error, got %v", err)
	require.Equal(t, want, got)
}
