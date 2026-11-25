//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

func requireErrCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got, err.Error())
}

// Test-ID: T-I-14
func TestLastRootOwner(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)

	// EC-35: the only owner of the root, by removal, demotion and re-adding.
	requireErrCode(t, w.removeMember(c, "ownerRoot", "root", "ownerRoot"), apperr.LastRootOwner)
	requireErrCode(t, w.changeMember(c, "ownerRoot", "root", "ownerRoot", "admin"), apperr.LastRootOwner)
	_, err := w.addMember(c, "ownerRoot", "root", "ownerroot@example.test", "viewer", "")
	requireErrCode(t, err, apperr.LastRootOwner)
	assert.Equal(t, "owner", w.assignments("root")["ownerroot@example.test"])
	assert.Empty(t, c.audit.actions(), "refusals leave no audit entry")

	// A second owner (a new user) makes the first removable, then the second is last.
	out, err := w.addMember(c, "ownerRoot", "root", "second@example.test", "owner", "twelve chars ok")
	require.NoError(t, err)
	w.users["second"] = out.Member.UserID
	require.NoError(t, w.removeMember(c, "ownerRoot", "root", "ownerRoot"))
	requireErrCode(t, w.removeMember(c, "second", "root", "second"), apperr.LastRootOwner)
	assert.Equal(t, map[string]string{"second@example.test": "owner"}, w.assignments("root"))

	// An inactive owner does not count: with the only other owner inactive the
	// remaining active one is the last.
	w2 := newWorld(t)
	c2 := w2.memberCases(nil)
	out2, err := w2.addMember(c2, "ownerRoot", "root", "second@example.test", "owner", "twelve chars ok")
	require.NoError(t, err)
	w2.users["second"] = out2.Member.UserID
	_, err = w2.db.Owner.Exec(context.Background(), `UPDATE users SET status = 'inactive' WHERE id = $1`, uuid.UUID(out2.Member.UserID))
	require.NoError(t, err)
	requireErrCode(t, w2.removeMember(c2, "ownerRoot", "root", "ownerRoot"), apperr.LastRootOwner)

	// EC-37: a non-root node may lose its last owner; the audit entry is written.
	w3 := newWorld(t)
	c3 := w3.memberCases(nil)
	_, err = w3.addMember(c3, "ownerRoot", "A", "orphan@example.test", "owner", "")
	require.NoError(t, err)
	require.NoError(t, w3.removeMember(c3, "ownerRoot", "A", "orphan"))
	assert.Equal(t, []string{"member.granted", "member.revoked"}, c3.audit.actions())
}

// Test-ID: T-I-17
func TestDuplicateAssignmentNoop(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)

	first, err := w.addMember(c, "adminA", "A1", "orphan@example.test", "viewer", "")
	require.NoError(t, err)
	require.Equal(t, []string{"member.granted"}, c.audit.actions())

	again, err := w.addMember(c, "adminA", "A1", "ORPHAN@example.test", "viewer", "")
	require.NoError(t, err)
	assert.Equal(t, first.Member, again.Member, "EC-40: the same assignment comes back")
	assert.Equal(t, []string{"member.granted"}, c.audit.actions(), "no audit entry for the repeat")
	assert.Equal(t, 1, countRows(t, w, "role_assignments", "node_id = $1 AND user_id = $2", uuid.UUID(w.id("A1")), uuid.UUID(w.users["orphan"])))

	// Another role replaces; there is still one row.
	changed, err := w.addMember(c, "adminA", "A1", "orphan@example.test", "analyst", "")
	require.NoError(t, err)
	assert.Equal(t, "analyst", string(changed.Member.Role))
	assert.Equal(t, "analyst", w.assignments("A1")["orphan@example.test"])
	assert.Len(t, w.assignments("A1"), 2, "next to multi, who was there before")
	assert.Equal(t, []string{"member.granted", "member.changed"}, c.audit.actions())
}

func countRows(t *testing.T, w *world, table, where string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, w.db.Owner.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&n))
	return n
}

// A new user is created and assigned in one transaction without reading the
// row back (RLS hides it until the assignment exists); an existing user of
// another branch is found through tb_user_id_by_email (EC-109).
// Test-ID: T-I-63
func TestAddMemberUsersOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)

	fresh, err := w.addMember(c, "adminA", "A1", "Fresh@Example.test", "viewer", "twelve chars ok")
	require.NoError(t, err)
	assert.Equal(t, "fresh@example.test", fresh.Member.Email.String())
	var hash, status string
	require.NoError(t, w.db.Owner.QueryRow(context.Background(),
		`SELECT password_hash, status FROM users WHERE id = $1`, uuid.UUID(fresh.Member.UserID)).Scan(&hash, &status))
	assert.Equal(t, "test-hash:twelve chars ok", hash)
	assert.Equal(t, "active", status)
	assert.NotContains(t, w.logs.String(), "twelve chars ok", "the password is never logged")

	// adminB's user is invisible to adminA, yet can be given a role in A's branch.
	other, err := w.addMember(c, "adminA", "A1", "adminb@example.test", "analyst", "ignored password!")
	require.NoError(t, err)
	assert.Equal(t, w.users["adminB"].String(), other.Member.UserID.String())
	var oldHash string
	require.NoError(t, w.db.Owner.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, uuid.UUID(w.users["adminB"])).Scan(&oldHash))
	assert.Equal(t, "hash", oldHash, "the password of an existing user is not replaced")

	// A new user without a password is refused and nothing is created.
	_, err = w.addMember(c, "adminA", "A1", "nopass@example.test", "viewer", "")
	requireErrCode(t, err, apperr.ValidationFailed)
	assert.Equal(t, 0, w.countUsers("nopass@example.test"))
}

// EC-41: the assignment is written first (the user is invisible without it),
// the status check then rolls it back.
// Test-ID: T-I-64
func TestAddInactiveUserRollsBack(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)
	_, err := w.db.Owner.Exec(context.Background(), `UPDATE users SET status = 'inactive' WHERE id = $1`, uuid.UUID(w.users["orphan"]))
	require.NoError(t, err)

	_, err = w.addMember(c, "adminA", "A1", "orphan@example.test", "viewer", "")
	requireErrCode(t, err, apperr.UserInactive)
	assert.NotContains(t, w.assignments("A1"), "orphan@example.test")
	assert.Empty(t, c.audit.actions())
}

// The role rules on the real database, where RLS alone would let all of this through.
// Test-ID: T-I-65
func TestMemberRulesOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)
	before := w.assignments("A")

	// EC-33: an admin cannot hand out, take away or change owner.
	_, err := w.addMember(c, "adminA", "A", "orphan@example.test", "owner", "")
	requireErrCode(t, err, apperr.RoleNotGrantable)
	_, err = w.addMember(c, "ownerRoot", "A", "orphan@example.test", "owner", "")
	require.NoError(t, err)
	requireErrCode(t, w.removeMember(c, "adminA", "A", "orphan"), apperr.RoleNotGrantable)
	requireErrCode(t, w.changeMember(c, "adminA", "A", "orphan", "viewer"), apperr.RoleNotGrantable)
	assert.Equal(t, "owner", w.assignments("A")["orphan@example.test"])

	// A viewer on a descendant has no right to manage members there.
	_, err = w.addMember(c, "viewerA1u1", "A1u1", "multi@example.test", "viewer", "")
	requireErrCode(t, err, apperr.Forbidden)

	// EC-34: another branch sees not_found and nothing changes.
	_, err = w.addMember(c, "adminB", "A", "multi@example.test", "viewer", "")
	requireErrCode(t, err, apperr.NotFound)
	requireErrCode(t, w.removeMember(c, "adminB", "A", "adminA"), apperr.NotFound)
	requireErrCode(t, w.changeMember(c, "adminB", "A", "adminA", "viewer"), apperr.NotFound)
	_, err = c.list.Execute(context.Background(), uc.ListMembersInput{Actor: w.users["adminB"], NodeID: w.id("A"), Limit: 10})
	requireErrCode(t, err, apperr.NotFound)

	delete(before, "orphan@example.test")
	got := w.assignments("A")
	delete(got, "orphan@example.test")
	assert.Equal(t, before, got)
}

// EC-42: the actor removes their own assignment and loses sight of the node.
// Test-ID: T-I-66
func TestRemoveOwnAssignmentOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)
	require.NoError(t, w.removeMember(c, "adminA", "A", "adminA"))
	_, err := c.list.Execute(context.Background(), uc.ListMembersInput{Actor: w.users["adminA"], NodeID: w.id("A"), Limit: 10})
	requireErrCode(t, err, apperr.NotFound)
}

// EC-61: inherited assignments show only while the ancestor is visible.
// Test-ID: T-I-67
func TestListMembersOnDatabase(t *testing.T) {
	w := newWorld(t)
	c := w.memberCases(nil)
	list := func(actor, node string, inherited bool, limit int, cursor string) uc.ListMembersOutput {
		out, err := c.list.Execute(context.Background(), uc.ListMembersInput{
			Actor: w.users[actor], NodeID: w.id(node), Inherited: inherited, Limit: limit, Cursor: cursor,
		})
		require.NoError(t, err)
		return out
	}

	got := list("adminA", "A1", true, 50, "")
	require.Len(t, got.Items, 2, "the owner of the root is above adminA's access root and stays hidden")
	assert.Equal(t, "admina@example.test", got.Items[0].Email.String())
	require.NotNil(t, got.Items[0].InheritedFrom)
	assert.Equal(t, w.id("A"), *got.Items[0].InheritedFrom)
	assert.Equal(t, "multi@example.test", got.Items[1].Email.String())
	assert.Nil(t, got.Items[1].InheritedFrom)

	direct := list("adminA", "A1", false, 50, "")
	require.Len(t, direct.Items, 1)
	assert.Equal(t, "multi@example.test", direct.Items[0].Email.String())

	// The owner of the root sees everything, in pages.
	page := list("ownerRoot", "A1", true, 2, "")
	require.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)
	rest := list("ownerRoot", "A1", true, 2, page.NextCursor)
	require.Len(t, rest.Items, 1)
	assert.Empty(t, rest.NextCursor)
	assert.Equal(t, "ownerroot@example.test", rest.Items[0].Email.String())
}
