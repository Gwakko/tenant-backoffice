//go:build integration

package apitest_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
)

// Test-ID: T-A-14
// EC-94: the limit is 1..200; anything else, including a text or a fraction,
// is 400 validation_failed on every listing, and the bounds themselves work.
func TestAPILimitValidation(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	lists := map[string]string{
		"operations": api + "/operations?scopeNodeId=" + w.A.String(),
		"audit":      api + "/audit?scopeNodeId=" + w.A.String(),
		"children":   nodeURL(w.A, "/children?"),
		"members":    nodeURL(w.A, "/members?"),
	}
	sep := func(base string) string {
		if base[len(base)-1] == '?' {
			return ""
		}
		return "&"
	}
	for name, base := range lists {
		for _, bad := range []string{"0", "-1", "201", "1000", "abc", "1.5", "9999999999999999999", "%20"} {
			res := admin.Get(base + sep(base) + "limit=" + bad)
			require.Equal(t, http.StatusBadRequest, res.Status, "%s limit=%s: %s", name, bad, res.Body)
			assert.Equal(t, "validation_failed", res.Problem().Code, "%s limit=%s", name, bad)
		}
		for _, ok := range []string{"1", "50", "200"} {
			requireStatus(t, admin.Get(base+sep(base)+"limit="+ok), http.StatusOK)
		}
	}
	// One item per page walks the whole list, with a cursor on every page but the last.
	page := operationsPage(t, admin, "scopeNodeId="+w.A.String()+"&limit=1")
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextCursor)
}
