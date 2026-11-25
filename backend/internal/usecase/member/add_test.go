package member_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

const goodPassword = "correct horse battery"

func (w *world) add(actor, node, email, r, password string) (uc.AddMemberOutput, error) {
	return uc.NewAddMember(w.deps).Execute(context.Background(), uc.AddMemberInput{
		Actor: w.ids[actor], RequestID: "r1", NodeID: w.id(node), Email: email, Role: r, InitialPassword: password,
	})
}

func TestAddMemberCreatesUser(t *testing.T) {
	w := newWorld(t)
	out, err := w.add("adminA", "A1", " New.User@Example.TEST ", "viewer", goodPassword)
	require.NoError(t, err)

	assert.Equal(t, "new.user@example.test", out.Member.Email.String())
	assert.Equal(t, role.Viewer, out.Member.Role)
	assert.Equal(t, w.id("A1"), out.Member.NodeID)
	assert.False(t, out.Member.UserID.IsZero())
	assert.False(t, out.Member.GrantedAt.IsZero())
	assert.Equal(t, role.Viewer, w.s.assign[akey{w.id("A1"), out.Member.UserID}].Role)
	assert.Equal(t, "hash("+goodPassword+")", string(w.s.users[out.Member.UserID].hash), "the stored value is the hash")
	assert.Equal(t, []string{"user.created", "member.granted"}, w.audit.actions())
	assert.Equal(t, "new.user@example.test", w.audit.entries[0].After["email"])
	assert.Equal(t, "viewer", w.audit.entries[1].After["role"])
	for _, e := range w.audit.entries {
		for _, v := range e.After {
			assert.NotContains(t, v, goodPassword)
			assert.NotContains(t, v, "hash(")
		}
	}
}

func TestAddMemberHashesOutsideTransaction(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A1", "new@example.test", "viewer", goodPassword)
	require.NoError(t, err)
	require.Equal(t, []bool{false}, w.s.hashing, "one hash, computed before the transaction opened")

	w.reset()
	_, err = w.add("adminA", "A1", "viewerA@example.test", "analyst", "")
	require.NoError(t, err)
	assert.Empty(t, w.s.hashing, "no password, no hash")
}

func TestAddMemberNewUserNeedsPassword(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A1", "new@example.test", "viewer", "")
	requireCode(t, err, apperr.ValidationFailed)
	e, ok := errors.AsType[apperr.Error](err)
	require.True(t, ok)
	assert.Equal(t, "required", e.Fields["initialPassword"])
	w.untouched()
}

// EC-109 (mitigation): a new and an existing user get an answer of the same
// shape; the initial password of an existing user is ignored.
// Test-ID: T-U-43
func TestAddMemberSameShapeForExistingUser(t *testing.T) {
	w := newWorld(t)
	fresh, err := w.add("adminA", "A1", "fresh@example.test", "analyst", goodPassword)
	require.NoError(t, err)
	existing, err := w.add("adminA", "A1", "nobody@example.test", "analyst", goodPassword)
	require.NoError(t, err)

	assert.Equal(t, w.ids["nobody"], existing.Member.UserID)
	assert.Equal(t, "hash(original)", string(w.s.users[w.ids["nobody"]].hash), "the password of an existing user is untouched")
	for _, m := range []uc.Member{fresh.Member, existing.Member} {
		assert.False(t, m.UserID.IsZero())
		assert.NotEmpty(t, m.Email)
		assert.Equal(t, role.Analyst, m.Role)
		assert.Equal(t, w.id("A1"), m.NodeID)
		assert.False(t, m.GrantedAt.IsZero())
	}
	assert.Equal(t, []bool{false, false}, w.s.hashing, "the hash cost is paid in both cases")
}

// Test-ID: T-U-18
func TestInactiveUser(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A1", "ghost@example.test", "viewer", "")
	requireCode(t, err, apperr.UserInactive)
	w.untouched() // the assignment written before the check was rolled back
}

func TestAddMemberInactiveUserWithExistingAssignment(t *testing.T) {
	w := newWorld(t)
	w.s.users[w.ids["member"]] = fakeUser{email: w.email("member"), status: "inactive"}
	w.reset()
	_, err := w.add("adminA", "A1", "member@example.test", "viewer", "")
	requireCode(t, err, apperr.UserInactive)
	w.untouched()
}

// EC-33
// Test-ID: T-U-42
func TestAddMemberRoleCeiling(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A1", "nobody@example.test", "owner", "")
	requireCode(t, err, apperr.RoleNotGrantable)
	w.untouched()

	_, err = w.add("adminA", "A1", "fresh@example.test", "owner", goodPassword)
	requireCode(t, err, apperr.RoleNotGrantable)
	w.untouched() // and no user was created for the refused request

	_, err = w.add("adminA", "A1", "nobody@example.test", "admin", "")
	require.NoError(t, err)
	_, err = w.add("ownerA", "A1", "viewerA@example.test", "owner", "")
	require.NoError(t, err)
	assert.Equal(t, role.Owner, w.roleOf("viewerA", "A1"))
}

func TestAddMemberRequiresManagePermission(t *testing.T) {
	w := newWorld(t)
	for _, actor := range []string{"viewerA", "analystA"} {
		_, err := w.add(actor, "A1", "nobody@example.test", "viewer", "")
		requireCode(t, err, apperr.Forbidden)
	}
	w.untouched()
}

// EC-34
func TestAddMemberOutsideSubtreeIsNotFound(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminB", "A1", "nobody@example.test", "viewer", "")
	requireCode(t, err, apperr.NotFound)
	_, err = w.add("nobody", "A1", "nobody@example.test", "viewer", "")
	requireCode(t, err, apperr.NotFound)
	w.untouched()
}

// An assignment on a descendant gives no right on its ancestor (EC-30).
func TestAddMemberNeedsRightOnTheNodeItself(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("member", "A", "nobody@example.test", "viewer", "")
	requireCode(t, err, apperr.NotFound)
	w.untouched()
}

// EC-40
func TestAddMemberSameRoleIsNoop(t *testing.T) {
	w := newWorld(t)
	before := w.s.assign[akey{w.id("A1"), w.ids["member"]}]
	out, err := w.add("adminA", "A1", "member@example.test", "viewer", "")
	require.NoError(t, err)
	assert.Equal(t, before.GrantedAt, out.Member.GrantedAt)
	assert.Equal(t, role.Viewer, out.Member.Role)
	w.untouched()
}

func TestAddMemberReplacesRole(t *testing.T) {
	w := newWorld(t)
	out, err := w.add("adminA", "A1", "member@example.test", "analyst", "")
	require.NoError(t, err)
	assert.Equal(t, role.Analyst, out.Member.Role)
	assert.Equal(t, role.Analyst, w.roleOf("member", "A1"))
	assert.Equal(t, []string{"member.changed"}, w.audit.actions())
	assert.Equal(t, "viewer", w.audit.entries[0].Before["role"])
	assert.Equal(t, "analyst", w.audit.entries[0].After["role"])
}

// Replacing a role is revoking the old one: an admin cannot do it to an owner.
func TestAddMemberCannotReplaceHigherRole(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A", "ownera@example.test", "viewer", "")
	requireCode(t, err, apperr.RoleNotGrantable)
	assert.Equal(t, role.Owner, w.roleOf("ownerA", "A"))
	w.untouched()
}

// EC-35 through the upsert path: replacing the last root owner by a lower role.
// Test-ID: T-U-44
func TestAddMemberCannotDemoteLastRootOwner(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("owner", "root", "owner@example.test", "admin", "")
	requireCode(t, err, apperr.LastRootOwner)
	assert.Equal(t, role.Owner, w.roleOf("owner", "root"))
	w.untouched()
}

func TestAddMemberValidation(t *testing.T) {
	w := newWorld(t)
	for name, in := range map[string]uc.AddMemberInput{
		"bad email":      {Email: "nope", Role: "viewer"},
		"bad role":       {Email: "a@example.test", Role: "root"},
		"empty role":     {Email: "a@example.test"},
		"short password": {Email: "a@example.test", Role: "viewer", InitialPassword: "short"},
		"no node":        {Email: "a@example.test", Role: "viewer", InitialPassword: goodPassword},
	} {
		in.Actor = w.ids["adminA"]
		if name != "no node" {
			in.NodeID = w.id("A1")
		}
		_, err := uc.NewAddMember(w.deps).Execute(context.Background(), in)
		requireCode(t, err, apperr.ValidationFailed)
		if in.InitialPassword != "" {
			assert.NotContains(t, err.Error(), in.InitialPassword, name)
		}
	}
	assert.Empty(t, w.s.hashing)
	w.untouched()
}

func TestAddMemberHasherFailureWritesNothing(t *testing.T) {
	w := newWorld(t)
	_, err := w.add("adminA", "A1", "fresh@example.test", "viewer", "fail-the-hasher")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fail-the-hasher")
	w.untouched()
}
