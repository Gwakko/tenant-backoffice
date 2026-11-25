package node_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-08
func TestCanDelete(t *testing.T) {
	tr := newTree(t)
	cases := []struct {
		name  string
		stats node.DeleteStats
		want  apperr.Code // empty: deletable
	}{
		{"empty", node.DeleteStats{}, ""},
		{"children", node.DeleteStats{Children: 1}, apperr.NodeHasChildren},       // EC-14
		{"balance", node.DeleteStats{Balance: 1}, apperr.NodeHasBalance},          // EC-15
		{"history", node.DeleteStats{HasOperations: true}, apperr.NodeHasHistory}, // EC-16
		{"children beat balance", node.DeleteStats{Children: 2, Balance: 5}, apperr.NodeHasChildren},
		{"balance beats history", node.DeleteStats{Balance: 5, HasOperations: true}, apperr.NodeHasBalance},
		{"zero balance with history", node.DeleteStats{Balance: 0, HasOperations: true}, apperr.NodeHasHistory},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := tr.unit.CanDelete(c.stats)
			if c.want == "" {
				require.NoError(t, err)
				return
			}
			requireCode(t, err, c.want)
		})
	}
}
