//go:build integration

package isolation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

// Test-ID: T-ISO-10
// Members of a node of another branch: reading, adding (an existing and a new
// user), changing and removing an assignment. The HTTP form of the same calls
// is checked in M4.6.
func TestForeignMembers(t *testing.T) {
	ctx, db, w, a := setup(t)
	sides := []struct {
		name     string
		actor    uuid.UUID
		existing uuid.UUID // a user who really holds a role in the foreign branch
		foreign  []uuid.UUID
	}{
		{"admin of A on branch B", w.AdminA, w.AdminB, concat(w.NodesB, []uuid.UUID{w.Root})},
		{"admin of B on branch A", w.AdminB, w.AdminA, concat(w.NodesA, []uuid.UUID{w.Root})},
	}
	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			before := isolation.Snapshot(ctx, t, db)
			ops := concat(
				[]op{readOps()[5], readOps()[6]}, // members, members inherited
				memberOps(s.existing, w.Emails[s.existing]),
			)
			requireIndistinguishable(ctx, t, a, s.actor, ops, s.foreign)
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "assignments or users changed")
			assert.Empty(t, a.audit.all())
		})
	}

	t.Run("a foreign user cannot be told from a new one by the answer", func(t *testing.T) {
		// On an own node both succeed with the same shape (T-U-43); on a
		// foreign node both fail the same way, whatever the email.
		for _, email := range []string{w.Emails[w.OnlyB], "nobody@example.test"} {
			_, err := a.add.Execute(ctx, member.AddMemberInput{Actor: actorOf(w.AdminA), RequestID: "t", NodeID: nid(w.B1u2), Email: email, Role: "viewer", InitialPassword: "correct horse battery"})
			_, base := a.add.Execute(ctx, member.AddMemberInput{Actor: actorOf(w.AdminA), RequestID: "t", NodeID: nid(uuid.New()), Email: email, Role: "viewer", InitialPassword: "correct horse battery"})
			require.Error(t, err)
			assert.Equal(t, base, err)
		}
	})

	t.Run("a user of a foreign branch can still be added to an own node", func(t *testing.T) {
		out, err := a.add.Execute(ctx, member.AddMemberInput{Actor: actorOf(w.AdminA), RequestID: "t", NodeID: nid(w.A1u2), Email: w.Emails[w.OnlyB], Role: "viewer"})
		require.NoError(t, err)
		assert.Equal(t, user.ID(w.OnlyB), out.Member.UserID)
		assert.Equal(t, w.A1u2, uuid.UUID(out.Member.NodeID))
		// Nothing of the other branch came along: B still holds only its own assignment of that user.
		var n int
		require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, w.OnlyB).Scan(&n))
		assert.Equal(t, 2, n)
	})
}

// Test-ID: T-ISO-16
// A user with roles in unrelated branches (Multi: admin of A1, viewer of B1)
// is seen from A only through the assignments on nodes A can see.
func TestMultiBranchUserSeenFromA(t *testing.T) {
	ctx, db, w, a := setup(t)
	before := isolation.Snapshot(ctx, t, db)

	// Every assignment the use case shows to the admin of A, over every node of A.
	var rows []member.Item
	for _, n := range w.NodesA {
		for _, inherited := range []bool{false, true} {
			out, err := a.listMems.Execute(ctx, memberListInput(w.AdminA, n, inherited))
			require.NoError(t, err)
			rows = append(rows, out.Items...)
		}
	}
	nodesB := idSet(concat(w.NodesB, []uuid.UUID{w.Root})...)
	var multiAt []uuid.UUID
	for _, r := range rows {
		assert.False(t, nodesB[uuid.UUID(r.NodeID)], "assignment on %s of another branch is visible", r.NodeID)
		assert.NotEqual(t, user.ID(w.OnlyB), r.UserID, "a user who only works in B is visible")
		assert.NotEqual(t, user.ID(w.AdminB), r.UserID)
		assert.NotEqual(t, user.ID(w.OwnerRoot), r.UserID, "the root owner's assignment is above the access root")
		if r.UserID == user.ID(w.Multi) {
			multiAt = append(multiAt, uuid.UUID(r.NodeID))
			assert.Equal(t, w.Emails[w.Multi], r.Email.String(), "the email of an assigned user is shown")
			assert.Equal(t, "admin", string(r.Role), "the role in B (viewer) must not leak")
		}
	}
	assert.NotEmpty(t, multiAt)
	for _, id := range multiAt {
		assert.Equal(t, w.A1, id)
	}

	// The same user from B: only the assignment in B.
	var fromB []member.Item
	for _, n := range w.NodesB {
		out, err := a.listMems.Execute(ctx, memberListInput(w.AdminB, n, true))
		require.NoError(t, err)
		fromB = append(fromB, out.Items...)
	}
	var multiB int
	for _, r := range fromB {
		if r.UserID == user.ID(w.Multi) {
			multiB++
			assert.Equal(t, w.B1, uuid.UUID(r.NodeID))
			assert.Equal(t, "viewer", string(r.Role))
		}
	}
	assert.NotZero(t, multiB, "listed on B1 itself and inherited by its units, always as the one assignment on B1")

	// The database layer on its own, as the admin of A: the assignments of Multi it returns.
	inTx(ctx, t, db.App, w.AdminA.String(), func(tx pgx.Tx) {
		assert.Equal(t, []uuid.UUID{w.A1},
			ids(ctx, t, tx, `SELECT node_id FROM role_assignments WHERE user_id = $1`, w.Multi))
		assert.ElementsMatch(t, []uuid.UUID{w.AdminA, w.AnalystA, w.Multi, w.ViewerA1u1, w.Inactive},
			ids(ctx, t, tx, `SELECT DISTINCT user_id FROM role_assignments`))
	})
	assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
}
