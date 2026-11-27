//go:build integration

package isolation_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-ISO-10
// EC-34, EC-55: members of a node of another branch: the list (both forms),
// adding a new or an existing user, changing a role, removing an assignment all
// answer as a missing node answers; no user and no assignment is created or
// changed; a user of the other branch can still be added to an own node.
func TestHTTPForeignMembers(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()
	ops := append(only(nodeReadOps(), "members"), memberWriteOps(w)...)
	for _, a := range h.actors() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, ops, a.foreign)
		})
	}
	for _, a := range h.readers() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, ops, a.foreign)
		})
	}
	h.requireUnchanged(before, "assignments or users changed")
	requireNoLogErrors(t, h.s)

	t.Run("the answer does not tell a foreign user from a new one", func(t *testing.T) {
		admin := h.as(w.AdminA)
		for _, email := range []string{w.Emails[w.OnlyB], w.Emails[w.AdminB], "nobody@example.test"} {
			add := func(node uuid.UUID) *apitest.Response {
				return admin.Post(hNodePath(node, "/members"), map[string]any{
					"email": email, "role": "viewer", "initialPassword": hNewUserPwd,
				})
			}
			missing := add(uuid.New())
			requireHTTPNotFound(t, email, missing)
			requireSameAnswer(t, email, missing, add(w.B1u2))
		}
	})

	t.Run("a user of the other branch can still be added to an own node", func(t *testing.T) {
		admin := h.as(w.AdminA)
		r := admin.Post(hNodePath(w.A1u2, "/members"), map[string]any{"email": w.Emails[w.OnlyB], "role": "viewer"})
		hStatus(t, r, http.StatusCreated)
		var m gen.Member
		r.JSON(&m)
		assert.Equal(t, w.OnlyB, uuid.UUID(m.UserId))
		assert.Equal(t, w.A1u2, uuid.UUID(m.NodeId))
		var n int
		require.NoError(t, h.s.DB.Owner.QueryRow(t.Context(),
			`SELECT count(*) FROM role_assignments WHERE user_id = $1`, w.OnlyB).Scan(&n))
		assert.Equal(t, 2, n, "the assignment in B is untouched, the one in A is new")
		// Nothing of branch B came along: the admin of A reads the user only at A1u2.
		list := admin.Get(hNodePath(w.A1u2, "/members?inherited=true"))
		requireNoneOf(t, "members of A1u2", list.Body, idStrings(w.NodesB, []uuid.UUID{w.Root}))
	})
}

// Test-ID: T-ISO-16
// EC-61: a user with roles in two branches (Multi: admin of A1, viewer of B1)
// is seen from A only through the assignment in A, and from B only through the
// assignment in B; users who work only in the other branch and the root owner
// are not listed.
func TestHTTPMultiBranchUserSeenFromA(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()

	collect := func(c *apitest.Client, nodes []uuid.UUID) (items []gen.Member, bodies [][]byte) {
		for _, n := range nodes {
			for _, inherited := range []string{"false", "true"} {
				r := c.Get(hNodePath(n, "/members?inherited="+inherited+"&limit=200"))
				hStatus(t, r, http.StatusOK)
				items = append(items, hDecode[gen.MemberPage](t, r).Items...)
				bodies = append(bodies, r.Body)
			}
		}
		return items, bodies
	}

	fromA, bodiesA := collect(h.as(w.AdminA), w.NodesA)
	var multiAt []uuid.UUID
	for _, m := range fromA {
		assert.NotContains(t, []uuid.UUID{w.OnlyB, w.AdminB, w.OwnerRoot}, uuid.UUID(m.UserId), "user %s of another branch listed", m.Email)
		if uuid.UUID(m.UserId) == w.Multi {
			multiAt = append(multiAt, uuid.UUID(m.NodeId))
			assert.Equal(t, w.Emails[w.Multi], string(m.Email))
			assert.Equal(t, gen.Role("admin"), m.Role, "the viewer role in B must not leak")
		}
	}
	require.NotEmpty(t, multiAt)
	for _, n := range multiAt {
		assert.Equal(t, w.A1, n)
	}
	for _, body := range bodiesA {
		requireNoneOf(t, "members seen from A", body, idStrings(w.NodesB, []uuid.UUID{w.Root, w.OnlyB, w.AdminB, w.OwnerRoot}))
		requireNoneOf(t, "members seen from A", body, []string{w.Emails[w.OnlyB], w.Emails[w.AdminB], w.Emails[w.OwnerRoot]})
	}

	fromB, bodiesB := collect(h.as(w.AdminB), w.NodesB)
	var multiB int
	for _, m := range fromB {
		if uuid.UUID(m.UserId) == w.Multi {
			multiB++
			assert.Equal(t, w.B1, uuid.UUID(m.NodeId))
			assert.Equal(t, gen.Role("viewer"), m.Role)
		}
	}
	assert.NotZero(t, multiB)
	for _, body := range bodiesB {
		requireNoneOf(t, "members seen from B", body, idStrings(w.NodesA, []uuid.UUID{w.Root, w.AdminA, w.AnalystA}))
	}

	// Multi itself reads each branch through its own role only: the members of
	// A1 hold nobody of B1 (OnlyB works in B1u2 and is visible to Multi there).
	multi := h.as(w.Multi)
	for _, q := range []string{"", "?inherited=true", "?inherited=true&limit=200"} {
		own := multi.Get(hNodePath(w.A1, "/members"+q))
		requireNoneOf(t, "multi on A1", own.Body, idStrings(w.NodesB, []uuid.UUID{w.OnlyB, w.AdminB}))
		requireNoneOf(t, "multi on A1", own.Body, []string{w.Emails[w.OnlyB], w.Emails[w.AdminB]})
	}
	h.requireUnchanged(before, "data changed")
}

// Test-ID: T-ISO-17
// EC-62: the cursors of the node and member lists carry only a position: the
// scope is decided again on every page, so a cursor of branch B opens nothing in
// A (a foreign list stays the answer of a missing node whatever the cursor says;
// an own list gives own data); the strict 400 invalid_cursor of the operations
// and audit lists is the same answer for a foreign scope and for a missing one.
func TestHTTPCursorBoundToScope(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	adminA, adminB := h.as(w.AdminA), h.as(w.AdminB)
	for i := range 4 {
		hCreate(t, adminA, w.A1, "unit", "Ax"+string(rune('a'+i)))
		hCreate(t, adminB, w.B1, "unit", "Bx"+string(rune('a'+i)))
	}
	nodesB := idStrings(w.NodesB, []uuid.UUID{w.Root})

	with := func(path, cursor string) string { return path + "&cursor=" + url.QueryEscape(cursor) }

	t.Run("lists of nodes and members: the cursor holds a position, the scope is decided again", func(t *testing.T) {
		type list struct {
			name   string
			node   uuid.UUID
			suffix string
		}
		lists := []list{
			{"children of A1", w.A1, "/children?limit=2"},
			{"members of A1", w.A1, "/members?inherited=true&limit=1"},
			{"move targets of A1u1", w.A1u1, "/move-targets?limit=1"},
		}
		hCreate(t, adminA, w.A, "company", "A2")
		hCreate(t, adminA, w.A, "company", "A3")
		before := h.snap()
		cursorOfList := func(c *apitest.Client, l list) string {
			r := c.Get(hNodePath(l.node, l.suffix))
			hStatus(t, r, http.StatusOK)
			var page struct {
				NextCursor *string `json:"nextCursor"`
			}
			r.JSON(&page)
			require.NotNil(t, page.NextCursor, l.name)
			return *page.NextCursor
		}
		for _, l := range lists {
			cur := cursorOfList(adminA, l)
			// The same list and the same reader: the next page.
			hStatus(t, adminA.Get(with(hNodePath(l.node, l.suffix), cur)), http.StatusOK)
			// The cursor holds no reader: another reader with access gets the page, one without access gets the 404 of a missing node.
			if l.node == w.A1 {
				hStatus(t, h.as(w.Multi).Get(with(hNodePath(l.node, l.suffix), cur)), http.StatusOK)
			}
			missing := adminB.Get(hNodePath(uuid.New(), l.suffix))
			requireHTTPNotFound(t, l.name, missing)
			requireSameAnswer(t, l.name+": a cursor does not open the list to a reader without access", missing,
				adminB.Get(with(hNodePath(l.node, l.suffix), cur)))
			// Any other list or node: invalid_cursor, the same for a foreign node and a missing one.
			for _, other := range lists {
				if other.name == l.name {
					continue
				}
				got := adminA.Get(with(hNodePath(other.node, other.suffix), cur))
				require.Equal(t, http.StatusBadRequest, got.Status, "%s on %s: %s", l.name, other.name, got.Body)
				assert.Equal(t, "invalid_cursor", got.Problem().Code)
			}
		}
		// The cursor of B (from the admin of B) on any list the admin of A can reach is no cursor of that list;
		// on a foreign or a missing node of A's view the answer is one and the same.
		curB := cursorOfList(adminB, list{"children of B1", w.B1, "/children?limit=2"})
		for _, suffix := range []string{"/children?limit=2", "/members?limit=1"} {
			missing := adminA.Get(with(hNodePath(uuid.New(), suffix), curB))
			require.Equal(t, http.StatusBadRequest, missing.Status)
			requireSameAnswer(t, "cursor of B on "+suffix, missing, adminA.Get(with(hNodePath(w.B, suffix), curB)))
			own := adminA.Get(with(hNodePath(w.A1, suffix), curB))
			require.Equal(t, http.StatusBadRequest, own.Status)
			requireNoneOf(t, "own list, cursor of B", own.Body, nodesB)
		}
		// The cursor of the very list of B, used by the admin of A on that list: the 404 of a missing node.
		same := adminA.Get(with(hNodePath(w.B1, "/children?limit=2"), curB))
		requireHTTPNotFound(t, "cursor of B on B1", same)
		requireSameAnswer(t, "cursor of B on B1", adminA.Get(hNodePath(uuid.New(), "/children")), same)
		// A damaged cursor on a foreign node and on a missing one: one answer.
		requireSameAnswer(t, "garbage cursor", adminA.Get(with(hNodePath(uuid.New(), "/children?limit=2"), "garbage")),
			adminA.Get(with(hNodePath(w.B1, "/children?limit=2"), "garbage")))
		h.requireUnchanged(before, "data changed")
	})

	t.Run("lists of operations and audit: strict", func(t *testing.T) {
		opsA := adminA.Get(hAPI + "/operations?scopeNodeId=" + w.A.String() + "&limit=1")
		curOps := *hDecode[gen.OperationPage](t, opsA).NextCursor
		auditA := adminA.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&limit=1")
		curAudit := *hDecode[gen.AuditPage](t, auditA).NextCursor
		for name, pair := range map[string][2]string{
			"operations": {"/operations?scopeNodeId=%s&limit=1", curOps},
			"audit":      {"/audit?scopeNodeId=%s&limit=1", curAudit},
		} {
			path := func(scope uuid.UUID) string {
				return with(hAPI+replaceScope(pair[0], scope), pair[1])
			}
			foreign, missing := adminA.Get(path(w.B)), adminA.Get(path(uuid.New()))
			require.Equal(t, http.StatusBadRequest, foreign.Status, "%s: %s", name, foreign.Body)
			assert.Equal(t, "invalid_cursor", foreign.Problem().Code)
			requireSameAnswer(t, name+": foreign scope vs missing scope", missing, foreign)
			// The cursor of B read by A: the same refusal, it is bound to the reader and the scope.
			curB := adminB.Get(hAPI + replaceScope(pair[0], w.B))
			var next string
			if name == "operations" {
				next = *hDecode[gen.OperationPage](t, curB).NextCursor
			} else {
				next = *hDecode[gen.AuditPage](t, curB).NextCursor
			}
			use := adminA.Get(with(hAPI+replaceScope(pair[0], w.B), next))
			assert.Equal(t, http.StatusBadRequest, use.Status, "%s: %s", name, use.Body)
			requireSameAnswer(t, name+": cursor of B in B vs cursor of B in a missing scope",
				adminA.Get(with(hAPI+replaceScope(pair[0], uuid.New()), next)), use)
		}
	})
}

func replaceScope(format string, scope uuid.UUID) string {
	return fmt.Sprintf(format, scope)
}
