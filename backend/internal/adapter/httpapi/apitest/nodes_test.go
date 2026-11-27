//go:build integration

package apitest_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-01
// EC-01: a company under a platform and a partner under a company are refused.
func TestAPICreateInvalidParentType(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	owner := s.As(w.OwnerRoot)
	before := nodeRows(t, s)

	owner.Post(nodeURL(w.Root, "/children"), map[string]string{"type": "company", "name": "X"}).
		RequireProblem(http.StatusUnprocessableEntity, "invalid_parent_type")
	s.As(w.AdminA).Post(nodeURL(w.A1, "/children"), map[string]string{"type": "partner", "name": "X"}).
		RequireProblem(http.StatusUnprocessableEntity, "invalid_parent_type")
	// The use case reports field errors as validation_failed, the reason in errors[].
	p := owner.Post(nodeURL(w.Root, "/children"), map[string]string{"type": "galaxy", "name": "X"}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	require.Equal(t, "type", p.Errors[0].Field)
	require.Equal(t, "invalid_node_type", p.Errors[0].Message)
	require.Equal(t, before, nodeRows(t, s), "no node was created")
}

// Test-ID: T-A-02
// EC-06: a company under a company or under the platform is refused.
func TestAPIMoveInvalidType(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	a2 := createNode(t, admin, w.A, "company", "A2")
	admin.Post(nodeURL(w.A1, "/move"), map[string]any{"newParentId": a2.Id}).
		RequireProblem(http.StatusUnprocessableEntity, "invalid_parent_type")
	s.As(w.OwnerRoot).Post(nodeURL(w.A1, "/move"), map[string]any{"newParentId": w.Root}).
		RequireProblem(http.StatusUnprocessableEntity, "invalid_parent_type")
	n := decode[gen.Node](t, admin.Get(nodeURL(w.A1, "")))
	require.Equal(t, w.A, *n.ParentId, "the node stays where it was")
}

// Test-ID: T-A-03
// EC-14: a node with a child, active or not, cannot be deleted.
func TestAPIDeleteWithChildren(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	owner := s.As(w.OwnerRoot)

	owner.Delete(nodeURL(w.A1, "")).RequireProblem(http.StatusConflict, "node_has_children")
	requireStatus(t, owner.Post(nodeURL(w.A1u1, "/deactivate"), nil), http.StatusOK)
	requireStatus(t, owner.Post(nodeURL(w.A1u2, "/deactivate"), nil), http.StatusOK)
	owner.Delete(nodeURL(w.A1, "")).RequireProblem(http.StatusConflict, "node_has_children")
	requireStatus(t, owner.Get(nodeURL(w.A1, "")), http.StatusOK)
}

// Test-ID: T-A-04
// EC-28: a node without children lists an empty page, not null.
func TestAPIEmptyChildren(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	owner := s.As(w.OwnerRoot)

	res := owner.Get(nodeURL(w.A1u1, "/children"))
	requireStatus(t, res, http.StatusOK)
	require.JSONEq(t, `{"items":[],"nextCursor":null}`, string(res.Body))

	p := createNode(t, owner, w.Root, "partner", "Fresh")
	res = owner.Get(nodeURL(p.Id, "/children"))
	requireStatus(t, res, http.StatusOK)
	require.JSONEq(t, `{"items":[],"nextCursor":null}`, string(res.Body))
}

// Proposed ID (not in spec 08 yet): T-A-36
func TestAPINodeLifecycle(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	owner := s.As(w.OwnerRoot)
	admin := s.As(w.AdminA)

	// Create, read with breadcrumbs from the actor's access root.
	u := createNode(t, admin, w.A1, "unit", "  New   Unit ")
	require.Equal(t, "New Unit", u.Name)
	require.Equal(t, gen.Unit, u.Type)
	require.Equal(t, w.A1, *u.ParentId)
	require.True(t, u.EffectiveActive)
	got := decode[gen.Node](t, admin.Get(nodeURL(u.Id, "")))
	require.Equal(t, u.Id, got.Id)
	require.Len(t, got.Breadcrumbs, 3)
	require.Equal(t, w.A, got.Breadcrumbs[0].Id, "breadcrumbs start at the access root of the actor")
	rootView := decode[gen.Node](t, owner.Get(nodeURL(u.Id, "")))
	require.Len(t, rootView.Breadcrumbs, 4)
	require.Equal(t, w.Root, rootView.Breadcrumbs[0].Id)
	require.Equal(t, 1, decode[gen.Node](t, admin.Get(nodeURL(w.A1u1, ""))).ChildrenCount+1)

	// Name taken, then rename.
	admin.Post(nodeURL(w.A1, "/children"), map[string]string{"type": "unit", "name": "new unit"}).
		RequireProblem(http.StatusConflict, "name_taken")
	renamed := decode[gen.Node](t, admin.Patch(nodeURL(u.Id, ""), map[string]string{"name": "Renamed"}))
	require.Equal(t, "Renamed", renamed.Name)
	p := admin.Patch(nodeURL(u.Id, ""), map[string]string{"name": "  "}).RequireProblem(http.StatusBadRequest, "validation_failed")
	require.Equal(t, "name", p.Errors[0].Field)
	require.Equal(t, "invalid_name", p.Errors[0].Message)

	// Deactivate and activate.
	off := decode[gen.Node](t, admin.Post(nodeURL(u.Id, "/deactivate"), nil))
	require.Equal(t, gen.NodeStatusInactive, off.Status)
	require.False(t, off.EffectiveActive)
	on := decode[gen.Node](t, admin.Post(nodeURL(u.Id, "/activate"), nil))
	require.Equal(t, gen.NodeStatusActive, on.Status)
	owner.Post(nodeURL(w.Root, "/deactivate"), nil).RequireProblem(http.StatusUnprocessableEntity, "root_immutable")

	// Move targets, then move to another company of the same branch.
	a2 := createNode(t, admin, w.A, "company", "A2")
	targets := decode[gen.NodeSummaryPage](t, admin.Get(nodeURL(u.Id, "/move-targets")))
	require.Len(t, targets.Items, 1, "the current parent is not a target")
	require.Equal(t, a2.Id, targets.Items[0].Id)
	moved := decode[gen.Node](t, admin.Post(nodeURL(u.Id, "/move"), map[string]any{"newParentId": a2.Id}))
	require.Equal(t, a2.Id, *moved.ParentId)
	admin.Post(nodeURL(a2.Id, "/move"), map[string]any{"newParentId": u.Id}).
		RequireProblem(http.StatusUnprocessableEntity, "move_cycle")
	owner.Post(nodeURL(w.Root, "/move"), map[string]any{"newParentId": w.A}).
		RequireProblem(http.StatusUnprocessableEntity, "root_immutable")

	// Delete needs owner on the parent: an admin gets 403, the owner 204.
	admin.Delete(nodeURL(u.Id, "")).RequireProblem(http.StatusForbidden, "forbidden")
	requireStatus(t, owner.Delete(nodeURL(u.Id, "")), http.StatusNoContent)
	owner.Get(nodeURL(u.Id, "")).RequireProblem(http.StatusNotFound, "not_found")
	require.NotEmpty(t, auditActors(t, s, "node.created", u.Id))
	require.Equal(t, []uuid.UUID{w.AdminA}, auditActors(t, s, "node.created", u.Id))
}

// Proposed ID (not in spec 08 yet): T-A-37
func TestAPIChildrenPagination(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	for i := range 5 {
		createNode(t, admin, w.A1, "unit", fmt.Sprintf("Extra %d", i))
	}

	var names []string
	cursor := ""
	pages := 0
	for {
		path := nodeURL(w.A1, "/children?limit=3")
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		page := decode[gen.NodeSummaryPage](t, admin.Get(path))
		pages++
		for _, it := range page.Items {
			names = append(names, it.Name)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	require.Equal(t, 3, pages)
	require.Len(t, names, 7)
	require.ElementsMatch(t, names, uniq(names), "no duplicates across pages")

	page := decode[gen.NodeSummaryPage](t, admin.Get(nodeURL(w.A1, "/children?limit=3")))
	tok := *page.NextCursor
	admin.Get(nodeURL(w.A1, "/children?limit=3&cursor="+tok+"x")).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	admin.Get(nodeURL(w.A1, "/children?limit=3&status=inactive&cursor="+tok)).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	admin.Get(nodeURL(w.A1u1, "/children?limit=3&cursor="+tok)).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	admin.Get(nodeURL(w.A1, "/children?limit=0")).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(nodeURL(w.A1, "/children?limit=201")).RequireProblem(http.StatusBadRequest, "validation_failed")

	filtered := decode[gen.NodeSummaryPage](t, admin.Get(nodeURL(w.A1, "/children?q=extra%203")))
	require.Len(t, filtered.Items, 1)
	require.Equal(t, "Extra 3", filtered.Items[0].Name)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Proposed ID (not in spec 08 yet): T-A-38
// The actor is the owner of the token, never a field of the request.
func TestAPIActorIsNeverTakenFromTheRequest(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := nodeRows(t, s)

	// A body that names an actor is refused as an unknown field, and nothing changes.
	for _, field := range []string{"actorId", "userId", "actor_id", "createdBy"} {
		admin.Post(nodeURL(w.A1, "/children"), map[string]any{"type": "unit", "name": "Forged", field: w.OwnerRoot}).
			RequireProblem(http.StatusBadRequest, "validation_failed")
	}
	admin.Post(nodeURL(w.A1, "/members"), map[string]any{"email": "forged@example.test", "role": "viewer",
		"initialPassword": "long-enough-password", "actorId": w.OwnerRoot}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	require.Equal(t, before, nodeRows(t, s))
	require.Zero(t, count(t, s, `SELECT count(*) FROM users WHERE email = 'forged@example.test'`))

	// The same names as query parameters or headers are ignored.
	me := decode[gen.Me](t, admin.Get(api+"/me?actorId="+w.OwnerRoot.String()+"&userId="+w.OwnerRoot.String(),
		apitest.WithHeader("X-Actor-Id", w.OwnerRoot.String()), apitest.WithHeader("X-User-Id", w.OwnerRoot.String())))
	require.Equal(t, w.AdminA, me.Id)
	u := decode[gen.Node](t, admin.Post(nodeURL(w.A1, "/children?actorId="+w.OwnerRoot.String()),
		map[string]string{"type": "unit", "name": "Honest"}, apitest.WithHeader("X-Actor-Id", w.OwnerRoot.String())))
	require.Equal(t, []uuid.UUID{w.AdminA}, auditActors(t, s, "node.created", u.Id))
}
