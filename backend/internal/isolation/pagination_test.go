//go:build integration

package isolation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	nodeuc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

// Test-ID: T-ISO-17
// A cursor of one branch must open nothing in another. The cursors of the node
// and member lists carry only a sort position, no scope, so the scope is
// decided again on every page: a cursor taken from a list of branch B, used on
// a list of branch A (or on B by someone who cannot see it), shows nothing of B
// and tells nothing. The strict form of EC-62 (400 invalid_cursor for a cursor
// of another scope) belongs to the operation and audit lists of M4; for nodes
// and members the isolation property holds without it and is what is checked.
func TestCursorBoundToScope(t *testing.T) {
	ctx, db, w, a := setup(t)
	var ownCompanies []uuid.UUID
	for _, name := range []string{"A2", "A3"} {
		ownCompanies = append(ownCompanies, w.AddNode(ctx, t, "company", &w.A, name))
	}
	// Named to sort before the names of A, so a foreign position lands inside A's list.
	for _, name := range []string{"a-b2", "a-b3"} {
		w.AddNode(ctx, t, "company", &w.B, name)
	}
	before := isolation.Snapshot(ctx, t, db)

	childrenPage := func(actor, parent uuid.UUID, cursor string) (nodeuc.ListChildrenOutput, error) {
		return a.list.Execute(ctx, nodeuc.ListChildrenInput{Actor: actorOf(actor), NodeID: nid(parent), Limit: 1, Cursor: cursor})
	}
	notFound := apperr.NotFoundError("node")

	// Cursors of both branches (the first page of each, with more behind it).
	cursorA, cursorB := "", ""
	for _, c := range []struct {
		actor, parent uuid.UUID
		into          *string
	}{{w.AdminA, w.A, &cursorA}, {w.AdminB, w.B, &cursorB}} {
		out, err := childrenPage(c.actor, c.parent, "")
		require.NoError(t, err)
		require.NotEmpty(t, out.NextCursor)
		*c.into = out.NextCursor
	}

	t.Run("a cursor of A on the children of B", func(t *testing.T) {
		_, err := childrenPage(w.AdminA, w.B, cursorA)
		assert.Equal(t, notFound, err)
		_, base := childrenPage(w.AdminA, uuid.New(), cursorA)
		assert.Equal(t, base, err, "a foreign parent with a cursor must answer like a missing one")
		_, err = childrenPage(w.AdminA, w.Root, cursorA)
		assert.Equal(t, notFound, err)
	})

	t.Run("a cursor of B on the children of A", func(t *testing.T) {
		own := idSet(concat(w.NodesA, ownCompanies)...)
		var got []uuid.UUID
		cursor := cursorB
		for range 10 {
			out, err := childrenPage(w.AdminA, w.A, cursor)
			require.NoError(t, err)
			for _, it := range out.Items {
				got = append(got, uuid.UUID(it.ID))
			}
			if cursor = out.NextCursor; cursor == "" {
				break
			}
		}
		assert.NotEmpty(t, got, "the foreign position lies inside the own list, so the test is not vacuous")
		for _, id := range got {
			assert.True(t, own[id], "node %s outside the actor's branch came with a foreign cursor", id)
		}
		// And the other way: B's admin on A with a cursor of A.
		_, err := childrenPage(w.AdminB, w.A, cursorA)
		assert.Equal(t, notFound, err)
	})

	t.Run("a cursor does not turn a foreign node into a visible one on every list", func(t *testing.T) {
		out, err := a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.B1u1), Limit: 1, Cursor: cursorA})
		assert.Equal(t, notFound, err)
		assert.Empty(t, out.Items)
		// A target list of an own node, started from a cursor of B: own nodes only.
		mine, err := a.targets.Execute(ctx, nodeuc.ListMoveTargetsInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1u1), Limit: 50, Cursor: cursorB})
		require.NoError(t, err)
		assert.NotEmpty(t, mine.Items)
		for _, it := range mine.Items {
			assert.Contains(t, ownCompanies, uuid.UUID(it.ID), "only companies of the actor's branch may be offered")
		}
	})

	t.Run("members: a cursor of one node on a node of another branch", func(t *testing.T) {
		pageA, err := a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1), Inherited: true, Limit: 1})
		require.NoError(t, err)
		require.NotEmpty(t, pageA.NextCursor)
		pageB, err := a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(w.AdminB), NodeID: nid(w.B1), Inherited: true, Limit: 1})
		require.NoError(t, err)
		require.NotEmpty(t, pageB.NextCursor)

		_, err = a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(w.AdminA), NodeID: nid(w.B1), Inherited: true, Limit: 1, Cursor: pageA.NextCursor})
		assert.Equal(t, notFound, err)
		_, base := a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(w.AdminA), NodeID: nid(uuid.New()), Inherited: true, Limit: 1, Cursor: pageA.NextCursor})
		assert.Equal(t, base, err)

		nodesB := idSet(concat(w.NodesB, []uuid.UUID{w.Root})...)
		cursor := pageB.NextCursor
		for range 20 {
			out, err := a.listMems.Execute(ctx, member.ListMembersInput{Actor: actorOf(w.AdminA), NodeID: nid(w.A1), Inherited: true, Limit: 1, Cursor: cursor})
			require.NoError(t, err)
			for _, it := range out.Items {
				assert.False(t, nodesB[uuid.UUID(it.NodeID)], "assignment on %s of another branch came with a foreign cursor", it.NodeID)
			}
			if cursor = out.NextCursor; cursor == "" {
				break
			}
		}
	})

	t.Run("a malformed cursor is the same refusal for every actor", func(t *testing.T) {
		var first error
		for _, actor := range []uuid.UUID{w.AdminA, w.AdminB, w.Orphan} {
			_, err := childrenPage(actor, w.A, "not-a-cursor!")
			code, ok := apperr.CodeOf(err)
			require.True(t, ok)
			assert.Equal(t, apperr.ValidationFailed, code)
			if first == nil {
				first = err
			}
			assert.Equal(t, first, err)
		}
	})

	assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
}
