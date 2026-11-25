//go:build integration

package isolation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	nodeuc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

// Test-ID: T-ISO-01
func TestGetForeignNodeIs404(t *testing.T) {
	ctx, db, w, a := setup(t)
	sides := []struct {
		name    string
		actor   uuid.UUID
		foreign []uuid.UUID
	}{
		{"admin of A reads B", w.AdminA, concat(w.NodesB, []uuid.UUID{w.Root})},
		{"admin of B reads A", w.AdminB, concat(w.NodesA, []uuid.UUID{w.Root})},
		{"user without roles", w.Orphan, w.AllNodes},
	}
	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			before := isolation.Snapshot(ctx, t, db)
			requireIndistinguishable(ctx, t, a, s.actor, readOps(), s.foreign)
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "reads changed data")
			assert.Empty(t, a.audit.all())
			assert.Empty(t, a.logs.String(), "a foreign id is a normal not_found, not an error to log")
		})
	}

	t.Run("own node is readable (control)", func(t *testing.T) {
		out, err := a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1)})
		require.NoError(t, err)
		assert.Equal(t, "A1", out.Node.Node.Name.String())
	})
}

// Test-ID: T-ISO-02
func TestListsExcludeForeignNodes(t *testing.T) {
	ctx, db, w, a := setup(t)
	// A second company in branch A, so that the move targets of A1u1 are not empty.
	created, err := a.create.Execute(ctx, nodeuc.CreateNodeInput{Actor: actorOf(w.AdminA), RequestID: "t", ParentID: nid(w.A), Type: "company", Name: "A2"})
	require.NoError(t, err)
	a2 := uuid.UUID(created.Node.Node.ID)

	foreignOfA := idSet(concat(w.NodesB, []uuid.UUID{w.Root})...)
	before := isolation.Snapshot(ctx, t, db)

	seen := map[uuid.UUID]bool{}
	note := func(ids ...uuid.UUID) {
		for _, id := range ids {
			seen[id] = true
		}
	}
	summaries := func(items []nodeuc.Summary) (out []uuid.UUID) {
		for _, it := range items {
			out = append(out, uuid.UUID(it.ID))
		}
		return out
	}

	roots, err := a.roots.Execute(ctx, nodeuc.ListAccessRootsInput{Actor: actorOf(w.AdminA)})
	require.NoError(t, err)
	require.Len(t, roots.Roots, 1)
	assert.Equal(t, w.A, uuid.UUID(roots.Roots[0].NodeID))
	note(uuid.UUID(roots.Roots[0].NodeID))

	for _, tc := range []struct {
		parent uuid.UUID
		want   []uuid.UUID
	}{{w.A, []uuid.UUID{w.A1, a2}}, {w.A1, []uuid.UUID{w.A1u1, w.A1u2}}} {
		for _, query := range []string{"", "b", "b1", "1"} {
			out, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.AdminA), NodeID: nid(tc.parent), Limit: 50, Query: query})
			require.NoError(t, err)
			got := summaries(out.Items)
			note(got...)
			if query == "" {
				assert.ElementsMatch(t, tc.want, got)
			}
		}
		// Page by page, one item at a time: the union is the same list.
		var paged []uuid.UUID
		cursor := ""
		for range 10 {
			out, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.AdminA), NodeID: nid(tc.parent), Limit: 1, Cursor: cursor})
			require.NoError(t, err)
			paged = append(paged, summaries(out.Items)...)
			if cursor = out.NextCursor; cursor == "" {
				break
			}
		}
		assert.ElementsMatch(t, tc.want, paged)
		note(paged...)
	}

	// Move targets of a unit: the companies of the branch only (A1 is the current parent).
	targets, err := a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1u1), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{a2}, summaries(targets.Items), "B1 is a company too, but not the actor's")
	note(summaries(targets.Items)...)
	targets, err = a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1), Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, targets.Items, "B is a partner, but not the actor's")

	// The root and the foreign branch have no children for A: not_found, as for a random id.
	for _, id := range concat(w.NodesB, []uuid.UUID{w.Root}) {
		_, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.AdminA), NodeID: nid(id), Limit: 50})
		assert.Equal(t, apperr.NotFoundError("node"), err)
	}

	for id := range seen {
		assert.False(t, foreignOfA[id], "node %s of another branch is in a list answer", id)
	}
	assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "reads changed data")

	t.Run("user with roles in two branches sees only the nodes of those roles", func(t *testing.T) {
		own := idSet(w.A1, w.A1u1, w.A1u2, w.B1, w.B1u1, w.B1u2)
		got, err := a.roots.Execute(ctx, nodeuc.ListAccessRootsInput{Actor: actorOf(w.Multi)})
		require.NoError(t, err)
		var rootIDs []uuid.UUID
		for _, r := range got.Roots {
			rootIDs = append(rootIDs, uuid.UUID(r.NodeID))
		}
		assert.ElementsMatch(t, []uuid.UUID{w.A1, w.B1}, rootIDs)
		for _, parent := range []uuid.UUID{w.A1, w.B1} {
			out, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.Multi), NodeID: nid(parent), Limit: 50})
			require.NoError(t, err)
			for _, id := range summaries(out.Items) {
				assert.True(t, own[id])
			}
		}
		for _, id := range []uuid.UUID{w.A, w.B, w.Root} {
			_, err := a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(w.Multi), NodeID: nid(id), Limit: 50})
			assert.Equal(t, apperr.NotFoundError("node"), err)
		}
	})
}

// Test-ID: T-ISO-03
// Mutations of a node of another branch, one row per operation the use cases
// offer: every answer is the not_found of a missing node and nothing changes.
// The settings endpoints of the table arrive in M3.5.
func TestMutateForeignNode(t *testing.T) {
	ctx, db, w, a := setup(t)
	sides := []struct {
		name    string
		actor   uuid.UUID
		parent  uuid.UUID
		foreign []uuid.UUID
	}{
		{"admin of A mutates B", w.AdminA, w.A, concat(w.NodesB, []uuid.UUID{w.Root})},
		{"admin of B mutates A", w.AdminB, w.B, concat(w.NodesA, []uuid.UUID{w.Root})},
	}
	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			before := isolation.Snapshot(ctx, t, db)
			requireIndistinguishable(ctx, t, a, s.actor, writeOps(s.parent), s.foreign)
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "foreign data changed")
			assert.Empty(t, a.audit.all(), "a refused call must leave no audit record")
		})
	}

	t.Run("the same calls on an own node work (control)", func(t *testing.T) {
		out, err := a.rename.Execute(ctx, nodeuc.RenameNodeInput{Actor: actorOf(w.AdminA), RequestID: "t", NodeID: nid(w.A1u2), Name: "renamed"})
		require.NoError(t, err)
		assert.Equal(t, "renamed", out.Node.Node.Name.String())
		_, err = a.deact.Execute(ctx, nodeuc.SetStatusInput{Actor: actorOf(w.AdminA), RequestID: "t", NodeID: nid(w.A1u2)})
		require.NoError(t, err)
	})
}

// Test-ID: T-ISO-19
func TestDescendantRoleDoesNotSeeAncestor(t *testing.T) {
	ctx, db, w, a := setup(t)
	// ViewerA1u1 holds a role on a unit only; AnalystA on a company; Multi on
	// two companies of different branches. Nobody sees what lies above.
	cases := []struct {
		name    string
		actor   uuid.UUID
		visible []uuid.UUID // subtree of the role
		above   []uuid.UUID
	}{
		{"viewer of a unit", w.ViewerA1u1, []uuid.UUID{w.A1u1}, []uuid.UUID{w.A1, w.A, w.Root, w.A1u2}},
		{"analyst of a company", w.AnalystA, []uuid.UUID{w.A1, w.A1u1, w.A1u2}, []uuid.UUID{w.A, w.Root}},
		{"admin of a company", w.Multi, []uuid.UUID{w.A1, w.A1u1, w.A1u2}, []uuid.UUID{w.A, w.Root}},
	}
	ro := readOps()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := isolation.Snapshot(ctx, t, db)
			requireIndistinguishable(ctx, t, a, c.actor, concat(ro, writeOps(c.visible[0]), memberOps(w.OnlyB, w.Emails[w.OnlyB])), c.above)
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
			assert.Empty(t, a.audit.all())

			// The role still works downward: the top of the subtree is the access root.
			out, err := a.get.Execute(ctx, nodeuc.GetNodeInput{Actor: actorOf(c.actor), NodeID: nid(c.visible[0])})
			require.NoError(t, err)
			require.NotEmpty(t, out.Node.Breadcrumbs)
			assert.Equal(t, c.visible[0], uuid.UUID(out.Node.Breadcrumbs[0].ID), "breadcrumbs start at the access root, not above it")
		})
	}
}
