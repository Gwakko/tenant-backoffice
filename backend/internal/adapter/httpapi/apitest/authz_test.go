//go:build integration

package apitest_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-06
// EC-38: a downgrade applies to the next request with the same token.
func TestAPIDowngradeAppliesNextRequest(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	owner := s.As(w.OwnerRoot)

	requireStatus(t, admin.Post(nodeURL(w.A1, "/children"), map[string]string{"type": "unit", "name": "Before"}), http.StatusCreated)

	res := owner.Put(nodeURL(w.A, "/members/"+w.AdminA.String()), map[string]string{"role": "viewer"})
	requireStatus(t, res, http.StatusOK)
	require.Equal(t, gen.Role("viewer"), decode[gen.Member](t, res).Role)

	// The token was not reissued: reads still work, writes are forbidden.
	requireStatus(t, admin.Get(nodeURL(w.A1, "")), http.StatusOK)
	admin.Post(nodeURL(w.A1, "/children"), map[string]string{"type": "unit", "name": "After"}).
		RequireProblem(http.StatusForbidden, "forbidden")
	require.Equal(t, 1, count(t, s, `SELECT count(*) FROM nodes WHERE name = 'Before'`))
	require.Zero(t, count(t, s, `SELECT count(*) FROM nodes WHERE name = 'After'`))
}

// Test-ID: T-A-08
// EC-43: a viewer sees the node and is refused every write on it with 403.
func TestAPIViewerCannotWrite(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	viewer := s.As(w.ViewerA1u1)
	n := w.A1u1
	snapshot := func() int {
		return count(t, s, `SELECT (SELECT count(*) FROM nodes) + (SELECT count(*) FROM role_assignments)
			+ (SELECT count(*) FROM node_settings) + (SELECT count(*) FROM audit_log)`)
	}
	before := snapshot()

	requireStatus(t, viewer.Get(nodeURL(n, "")), http.StatusOK)
	for name, res := range map[string]*apitest.Response{
		"create":     viewer.Post(nodeURL(n, "/children"), map[string]string{"type": "unit", "name": "X"}),
		"rename":     viewer.Patch(nodeURL(n, ""), map[string]string{"name": "X"}),
		"deactivate": viewer.Post(nodeURL(n, "/deactivate"), nil),
		"activate":   viewer.Post(nodeURL(n, "/activate"), nil),
		"move":       viewer.Post(nodeURL(n, "/move"), map[string]any{"newParentId": w.A1u2}),
		"delete":     viewer.Delete(nodeURL(n, "")),
		"add member": viewer.Post(nodeURL(n, "/members"), map[string]string{"email": w.Emails[w.Orphan], "role": "viewer"}),
		"set":        viewer.Put(nodeURL(n, "/settings/operations_enabled"), map[string]any{"value": false}),
		"unset":      viewer.Delete(nodeURL(n, "/settings/operations_enabled")),
	} {
		require.Equal(t, http.StatusForbidden, res.Status, "%s: %s", name, res.Body)
		require.Equal(t, "forbidden", res.Problem().Code, name)
	}
	require.Equal(t, before, snapshot(), "nothing changed")
}
