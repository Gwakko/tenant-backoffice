//go:build integration

package isolation_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-ISO-01
// EC-46: over HTTP, every read of a node of another branch (and of the nodes
// above the access root) answers byte for byte like a node that does not exist
// (status, all headers, body without requestId); nothing changes and nothing is
// logged as an error.
func TestHTTPGetForeignNodeIs404(t *testing.T) {
	h := newHWorld(t)
	before := h.snap()
	for _, a := range append(h.actors(), h.readers()...) {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, nodeReadOps(), a.foreign)
		})
	}
	h.requireUnchanged(before, "reads changed data")
	requireNoLogErrors(t, h.s)

	t.Run("own node is readable (control)", func(t *testing.T) {
		n := hDecode[gen.Node](t, h.as(h.w.AdminA).Get(hNodePath(h.w.A1, "")))
		assert.Equal(t, "A1", n.Name)
		// The same id read by the admin of the other branch is the 404.
		requireHTTPNotFound(t, "A1 for B", h.as(h.w.AdminB).Get(hNodePath(h.w.A1, "")))
	})
}

// Test-ID: T-ISO-02
// EC-47: no list or access root of an actor holds a node of another branch or
// of the root above the access root: not in /me, not in the children of an own
// node (paged one by one), not in the move targets. The bodies do not even
// mention such an id.
func TestHTTPListsExcludeForeignNodes(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	admin := h.as(w.AdminA)
	// A second company in A, so that the move targets of a unit are not empty
	// and B1 is the trap.
	a2 := hCreate(t, admin, w.A, "company", "A2")
	before := h.snap()
	foreign := idStrings(w.NodesB, []uuid.UUID{w.Root})

	seen := map[uuid.UUID]bool{}
	scan := func(label string, _ any, body []byte) {
		requireNoneOf(t, label, body, foreign)
	}

	me := admin.Get(hAPI + "/me")
	scan("me", nil, me.Body)
	roots := hDecode[gen.Me](t, me).AccessRoots
	require.Len(t, roots, 1)
	require.Equal(t, w.A, uuid.UUID(roots[0].NodeId))

	for _, parent := range []uuid.UUID{w.A, w.A1} {
		cursor := ""
		for {
			path := hNodePath(parent, "/children?limit=1")
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			r := admin.Get(path)
			scan("children of "+parent.String(), nil, r.Body)
			page := hDecode[gen.NodeSummaryPage](t, r)
			for _, it := range page.Items {
				seen[uuid.UUID(it.Id)] = true
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
	}
	assert.Equal(t, map[uuid.UUID]bool{w.A1: true, a2.Id: true, w.A1u1: true, w.A1u2: true}, seen)

	targets := admin.Get(hNodePath(w.A1u1, "/move-targets"))
	scan("move targets", nil, targets.Body)
	got := map[uuid.UUID]bool{}
	for _, it := range hDecode[gen.NodeSummaryPage](t, targets).Items {
		got[uuid.UUID(it.Id)] = true
	}
	assert.True(t, got[a2.Id], "the own company is a target")
	assert.False(t, got[w.B1], "the company of the other branch is not")

	// The root and B are not listable by the admin of A: the answer is the 404 of a missing id.
	missing := admin.Get(hNodePath(uuid.New(), "/children"))
	for _, id := range []uuid.UUID{w.Root, w.B} {
		requireSameAnswer(t, "children of "+id.String(), missing, admin.Get(hNodePath(id, "/children")))
	}

	// A user with a role in each branch: two access roots, each list within its own branch.
	multi := h.as(w.Multi)
	mr := hDecode[gen.Me](t, multi.Get(hAPI+"/me")).AccessRoots
	roleOf := map[uuid.UUID]gen.Role{}
	for _, r := range mr {
		roleOf[uuid.UUID(r.NodeId)] = r.Role
	}
	assert.Equal(t, map[uuid.UUID]gen.Role{w.A1: "admin", w.B1: "viewer"}, roleOf)
	for parent, ancestors := range map[uuid.UUID][]uuid.UUID{
		w.A1: {w.Root, w.A, w.B, w.B1, w.B1u1, w.B1u2},
		w.B1: {w.Root, w.A, w.B, w.A1, w.A1u1, w.A1u2},
	} {
		r := multi.Get(hNodePath(parent, "/children"))
		requireNoneOf(t, "multi children", r.Body, idStrings(ancestors))
	}

	// The owner of the root, the control, does see both branches.
	owner := hDecode[gen.NodeSummaryPage](t, h.as(w.OwnerRoot).Get(hNodePath(w.Root, "/children")))
	require.Len(t, owner.Items, 2)
	h.requireUnchanged(before, "reads changed data")
}

// Test-ID: T-ISO-03
// EC-48: every mutation of a node of another branch (rename, deactivate,
// activate, delete, create child, move, settings PUT and DELETE) is the answer
// of a missing node, in both directions and for the nodes above; no row of any
// table changes. A control shows the same calls do act on an own node.
func TestHTTPMutateForeignNode(t *testing.T) {
	h := newHWorld(t)
	before := h.snap()
	for _, a := range h.actors() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, nodeWriteOps(a), a.foreign)
		})
	}
	h.requireUnchanged(before, "a refused mutation changed data")
	requireNoLogErrors(t, h.s)

	t.Run("control: the same calls on an own node act", func(t *testing.T) {
		admin := h.as(h.w.AdminA)
		hStatus(t, admin.Patch(hNodePath(h.w.A1u2, ""), map[string]string{"name": "Renamed"}), http.StatusOK)
		hStatus(t, admin.Put(hNodePath(h.w.A1, "/settings/operations_enabled"), map[string]any{"value": false}), http.StatusOK)
		assert.NotEqual(t, before, h.snap())
	})
}

// Test-ID: T-ISO-04
// EC-49: a move across the border of a branch, in either direction, is the
// answer of a missing node or a missing parent; a user who sees both nodes but
// may not move in the target gets forbidden; paths do not change.
func TestHTTPMoveAcrossBranches(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()
	cases := []struct {
		name  string
		actor uuid.UUID
		node  uuid.UUID // the node to move
		to    uuid.UUID // the new parent
		own   bool      // the node is the actor's own
	}{
		{"A moves its own node under a foreign parent", w.AdminA, w.A1u2, w.B1, true},
		{"A moves its own node under the root", w.AdminA, w.A1, w.Root, true},
		{"A moves a foreign node under its own parent", w.AdminA, w.B1u1, w.A1, false},
		{"B moves its own node under a foreign parent", w.AdminB, w.B1u2, w.A1, true},
		{"B moves a foreign node under its own parent", w.AdminB, w.A1u1, w.B1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := h.as(tc.actor)
			got := c.Post(hNodePath(tc.node, "/move"), map[string]any{"newParentId": tc.to})
			want := c.Post(hNodePath(tc.node, "/move"), map[string]any{"newParentId": uuid.New()})
			if !tc.own {
				want = c.Post(hNodePath(uuid.New(), "/move"), map[string]any{"newParentId": tc.to})
			}
			requireHTTPNotFound(t, tc.name, got)
			requireSameAnswer(t, tc.name, want, got)
		})
	}

	t.Run("a user with a role in both branches may not move across them", func(t *testing.T) {
		multi := h.as(w.Multi)
		// Multi administers A1 and only views B1: B1 is visible but may not receive a node.
		hStatus(t, multi.Post(hNodePath(w.A1u2, "/move"), map[string]any{"newParentId": w.B1}), http.StatusForbidden)
		// And a node of B1, visible as a viewer, may not leave.
		hStatus(t, multi.Post(hNodePath(w.B1u1, "/move"), map[string]any{"newParentId": w.A1}), http.StatusForbidden)
	})
	h.requireUnchanged(before, "nothing moved")
	requireNoLogErrors(t, h.s)
}

// Test-ID: T-ISO-18
// EC-63: the owner of the root moves A1 from A to B. Afterwards the admin of A
// gets the answer of a missing node for A1 and everything below it and the
// children of A no longer list it; the admin of B sees the subtree with
// breadcrumbs through B; the audit entry of A names the move out without the
// new parent.
func TestHTTPCrossBranchMoveByRootOwner(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	owner, adminA, adminB := h.as(w.OwnerRoot), h.as(w.AdminA), h.as(w.AdminB)

	hStatus(t, owner.Post(hNodePath(w.A1, "/move"), map[string]any{"newParentId": w.B}), http.StatusOK)

	for _, id := range []uuid.UUID{w.A1, w.A1u1, w.A1u2} {
		for _, suffix := range []string{"", "/children", "/members", "/balance", "/settings", "/move-targets"} {
			requireSameAnswer(t, "after the move: "+suffix, adminA.Get(hNodePath(uuid.New(), suffix)), adminA.Get(hNodePath(id, suffix)))
		}
	}
	children := adminA.Get(hNodePath(w.A, "/children"))
	hStatus(t, children, http.StatusOK)
	assert.Empty(t, hDecode[gen.NodeSummaryPage](t, children).Items)
	ops := adminA.Get(hAPI + "/operations?scopeNodeId=" + w.A.String() + "&limit=200")
	assert.Empty(t, hDecode[gen.OperationPage](t, ops).Items, "the operations left with the nodes")

	// The admin of B sees the moved subtree, below B.
	n := hDecode[gen.Node](t, adminB.Get(hNodePath(w.A1u1, "")))
	require.NotEmpty(t, n.Breadcrumbs)
	assert.Equal(t, w.B, uuid.UUID(n.Breadcrumbs[0].Id), "breadcrumbs start at the access root of B")
	var chain []uuid.UUID
	for _, b := range n.Breadcrumbs {
		chain = append(chain, uuid.UUID(b.Id))
	}
	assert.Equal(t, []uuid.UUID{w.B, w.A1, w.A1u1}, chain, "through B, down to the node itself")
	assert.NotContains(t, string(adminB.Get(hNodePath(w.A1u1, "")).Body), w.Root.String())
	got := hDecode[gen.OperationPage](t, adminB.Get(hAPI+"/operations?scopeNodeId="+w.A1.String()+"&limit=200"))
	assert.Len(t, got.Items, 4, "B now reads the operations of the moved company")

	// The roles on the moved nodes went along with them.
	assert.Equal(t, http.StatusOK, h.as(w.Multi).Get(hNodePath(w.A1, "")).Status)
	assert.Equal(t, http.StatusOK, h.as(w.ViewerA1u1).Get(hNodePath(w.A1u1, "")).Status)
	// The old branch no longer holds a role-less path to it: AnalystA is on A1 and travels too.
	assert.Equal(t, http.StatusOK, h.as(w.AnalystA).Get(hNodePath(w.A1, "")).Status)

	// The audit of A tells that a child moved out, and not where to.
	r := adminA.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&limit=200")
	page := hDecode[gen.AuditPage](t, r)
	var movedOut int
	for _, e := range page.Items {
		if e.Action == "node.child_moved_out" {
			movedOut++
		}
	}
	assert.Equal(t, 1, movedOut)
	requireNoneOf(t, "audit of A", r.Body, []string{w.B.String(), w.B1.String(), "\"B\""})
}

// Test-ID: T-ISO-19
// EC-30: a role on a descendant does not act upward: for a viewer of a unit, an
// analyst of a company and the admin of a company the ancestors and the root
// are missing nodes in reads, writes and member operations; breadcrumbs start
// at the access root.
func TestHTTPDescendantRoleDoesNotSeeAncestor(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()

	type roleCase struct {
		a         hActor
		ancestors []uuid.UUID
		own       uuid.UUID
		crumb0    uuid.UUID
	}
	pick := func(list []hActor, name string) hActor {
		for _, a := range list {
			if a.name == name {
				return a
			}
		}
		t.Fatalf("no actor %s", name)
		return hActor{}
	}
	viewer, analyst := pick(h.readers(), "viewer of a unit"), pick(h.readers(), "analyst of a company")
	multi := pick(h.actors(), "multi")
	for _, tc := range []roleCase{
		{viewer, []uuid.UUID{w.A1, w.A, w.Root}, w.A1u1, w.A1u1},
		{analyst, []uuid.UUID{w.A, w.Root}, w.A1u1, w.A1},
		{multi, []uuid.UUID{w.A, w.Root}, w.A1u1, w.A1},
	} {
		t.Run(tc.a.name, func(t *testing.T) {
			a := tc.a
			a.unit2 = w.A1u2
			ops := append(append(nodeReadOps(), ledgerReadOps(a)...), nodeWriteOps(a)...)
			ops = append(ops, memberWriteOps(w)...)
			ops = append(ops, moneyWriteOps(a)...)
			requireAllLikeMissing(t, a, ops, tc.ancestors)

			// The own side answers, with breadcrumbs from the access root.
			n := hDecode[gen.Node](t, a.c.Get(hNodePath(tc.own, "")))
			require.NotEmpty(t, n.Breadcrumbs)
			assert.Equal(t, tc.crumb0, uuid.UUID(n.Breadcrumbs[0].Id))
			requireNoneOf(t, "node body", mustBody(t, a, tc.own), idStrings(tc.ancestors))
		})
	}
	h.requireUnchanged(before, "a refused request changed data")
}

func mustBody(t *testing.T, a hActor, id uuid.UUID) []byte {
	t.Helper()
	r := a.c.Get(hNodePath(id, ""))
	hStatus(t, r, http.StatusOK)
	return r.Body
}
