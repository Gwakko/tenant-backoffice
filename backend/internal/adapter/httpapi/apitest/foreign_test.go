//go:build integration

package apitest_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
)

// Proposed ID (not in spec 08 yet): T-A-43
// EC-54: for every operation on nodes, members and settings, a node of another
// branch answers byte for byte like a node that does not exist.
func TestAPIForeignNodeIsIndistinguishableFromMissing(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	foreignUser := w.AdminB

	type call func(id uuid.UUID) *apitest.Response
	ops := map[string]call{
		"get":      func(id uuid.UUID) *apitest.Response { return admin.Get(nodeURL(id, "")) },
		"children": func(id uuid.UUID) *apitest.Response { return admin.Get(nodeURL(id, "/children")) },
		"create": func(id uuid.UUID) *apitest.Response {
			return admin.Post(nodeURL(id, "/children"), map[string]string{"type": "unit", "name": "X"})
		},
		"rename": func(id uuid.UUID) *apitest.Response {
			return admin.Patch(nodeURL(id, ""), map[string]string{"name": "X"})
		},
		"move": func(id uuid.UUID) *apitest.Response {
			return admin.Post(nodeURL(id, "/move"), map[string]any{"newParentId": w.A})
		},
		"move target": func(id uuid.UUID) *apitest.Response {
			return admin.Post(nodeURL(w.A1u1, "/move"), map[string]any{"newParentId": id})
		},
		"move-targets": func(id uuid.UUID) *apitest.Response { return admin.Get(nodeURL(id, "/move-targets")) },
		"deactivate":   func(id uuid.UUID) *apitest.Response { return admin.Post(nodeURL(id, "/deactivate"), nil) },
		"activate":     func(id uuid.UUID) *apitest.Response { return admin.Post(nodeURL(id, "/activate"), nil) },
		"delete":       func(id uuid.UUID) *apitest.Response { return admin.Delete(nodeURL(id, "")) },
		"members list": func(id uuid.UUID) *apitest.Response { return admin.Get(nodeURL(id, "/members")) },
		"members add": func(id uuid.UUID) *apitest.Response {
			return admin.Post(nodeURL(id, "/members"), map[string]any{"email": "x@example.test", "role": "viewer", "initialPassword": newPassword12})
		},
		"members change": func(id uuid.UUID) *apitest.Response {
			return admin.Put(nodeURL(id, "/members/"+foreignUser.String()), map[string]string{"role": "viewer"})
		},
		"members change 2": func(id uuid.UUID) *apitest.Response {
			return admin.Put(nodeURL(id, "/members/"+uuid.NewString()), map[string]string{"role": "viewer"})
		},
		"members remove": func(id uuid.UUID) *apitest.Response {
			return admin.Delete(nodeURL(id, "/members/"+foreignUser.String()))
		},
		"members remove 2": func(id uuid.UUID) *apitest.Response { return admin.Delete(nodeURL(id, "/members/"+uuid.NewString())) },
		"settings list":    func(id uuid.UUID) *apitest.Response { return admin.Get(nodeURL(id, "/settings")) },
		"settings put": func(id uuid.UUID) *apitest.Response {
			return admin.Put(nodeURL(id, "/settings/operations_enabled"), map[string]any{"value": false})
		},
		"settings delete": func(id uuid.UUID) *apitest.Response { return admin.Delete(nodeURL(id, "/settings/operations_enabled")) },
		"permissions":     func(id uuid.UUID) *apitest.Response { return admin.Get(api + "/me/permissions?nodeId=" + id.String()) },
	}
	snapshot := func() int {
		return count(t, s, `SELECT (SELECT count(*) FROM nodes) + (SELECT count(*) FROM role_assignments) * 7
			+ (SELECT count(*) FROM node_settings) * 13 + (SELECT count(*) FROM audit_log) * 17 + (SELECT count(*) FROM users) * 19`)
	}
	before := snapshot()
	for name, op := range ops {
		for _, foreign := range []uuid.UUID{w.B, w.B1, w.B1u1} {
			missing := op(uuid.New())
			got := op(foreign)
			require.Equal(t, http.StatusNotFound, missing.Status, "%s missing: %s", name, missing.Body)
			require.Equal(t, "not_found", missing.Problem().Code, name)
			require.Equal(t, http.StatusNotFound, got.Status, "%s foreign: %s", name, got.Body)
			require.Equal(t, missing.BodyWithoutRequestID(), got.BodyWithoutRequestID(), name)
		}
	}
	require.Equal(t, before, snapshot(), "no foreign state was touched")
}
