//go:build integration

package apitest_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-05
// EC-32: a user with roles in unrelated branches has one access root per branch.
func TestAPIAccessRootsMultiBranch(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	multi := s.As(w.Multi)

	me := decode[gen.Me](t, multi.Get(api+"/me"))
	require.Equal(t, w.Multi, me.Id)
	require.Equal(t, w.Emails[w.Multi], string(me.Email))
	got := map[string]gen.Role{}
	for _, r := range me.AccessRoots {
		got[r.NodeId.String()] = r.Role
	}
	require.Equal(t, map[string]gen.Role{w.A1.String(): gen.Role("admin"), w.B1.String(): gen.Role("viewer")}, got)

	inA := decode[gen.NodePermissions](t, multi.Get(api+"/me/permissions?nodeId="+w.A1u1.String()))
	require.Equal(t, gen.Role("admin"), inA.Role)
	require.Contains(t, inA.Permissions, gen.Permission("node.create"))
	inB := decode[gen.NodePermissions](t, multi.Get(api+"/me/permissions?nodeId="+w.B1u1.String()))
	require.Equal(t, gen.Role("viewer"), inB.Role)
	require.NotContains(t, inB.Permissions, gen.Permission("node.create"))
	require.Contains(t, inB.Permissions, gen.Permission("node.read"))

	// Neither branch reveals what lies above it or beside it.
	multi.Get(nodeURL(w.B, "")).RequireProblem(http.StatusNotFound, "not_found")
	multi.Get(nodeURL(w.A, "")).RequireProblem(http.StatusNotFound, "not_found")
	multi.Get(api+"/me/permissions?nodeId="+w.B1u1.String()+"x").RequireProblem(http.StatusBadRequest, "invalid_id")

	// A user without a role has no access roots, and every node is not found.
	orphan := s.As(w.Orphan)
	require.Empty(t, decode[gen.Me](t, orphan.Get(api+"/me")).AccessRoots)
	orphan.Get(nodeURL(w.A1, "")).RequireProblem(http.StatusNotFound, "not_found")
}

// Proposed ID (not in spec 08 yet): T-A-39
// The 503 of the unwired authenticator is gone: a signed token reaches the use cases.
func TestAPIAuthenticatedRequestsAreServed(t *testing.T) {
	s := apitest.NewStack(t)
	u := s.As(s.World.AdminA)
	requireStatus(t, u.Get(api+"/me"), http.StatusOK)
	requireStatus(t, u.Get(nodeURL(s.World.A, "")), http.StatusOK)
	s.Anon.Get(api+"/me").RequireProblem(http.StatusUnauthorized, "token_missing")
	s.Anon.WithToken("garbage").Get(api+"/me").RequireProblem(http.StatusUnauthorized, "token_invalid")
	ready := s.Anon.Get("/readyz")
	requireStatus(t, ready, http.StatusOK)
}
