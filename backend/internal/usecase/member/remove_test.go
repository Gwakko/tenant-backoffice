package member_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

func (w *world) remove(actor, node, target string) error {
	return uc.NewRemoveMember(w.deps).Execute(context.Background(), uc.RemoveMemberInput{
		Actor: w.ids[actor], RequestID: "r1", NodeID: w.id(node), UserID: w.ids[target],
	})
}

// Test-ID: T-U-17
func TestLastOwner(t *testing.T) {
	w := newWorld(t)

	// EC-35: the only owner of the root stays.
	requireCode(t, w.remove("owner", "root", "owner"), apperr.LastRootOwner)
	assert.Equal(t, role.Owner, w.roleOf("owner", "root"))
	w.untouched()

	// With a second active owner the first can go.
	w.addUser("owner2", "active")
	w.grant("owner2", "root", role.Owner)
	w.reset()
	require.NoError(t, w.remove("owner", "root", "owner"))
	assert.Equal(t, role.Role(""), w.roleOf("owner", "root"))
	assert.Equal(t, []string{"member.revoked"}, w.audit.actions())
	// and now owner2 is the last one.
	requireCode(t, w.remove("owner2", "root", "owner2"), apperr.LastRootOwner)

	// EC-37: a non-root node may lose its last owner.
	w = newWorld(t)
	require.NoError(t, w.remove("ownerA", "A", "ownerA"))
	assert.Equal(t, []string{"member.revoked"}, w.audit.actions())
	assert.Equal(t, "owner", w.audit.entries[0].Before["role"])
}

// An inactive owner does not count as an owner to keep.
func TestLastOwnerIgnoresInactiveOwners(t *testing.T) {
	w := newWorld(t)
	w.grant("ghost", "root", role.Owner) // inactive, assigned directly
	w.reset()
	requireCode(t, w.remove("owner", "root", "owner"), apperr.LastRootOwner)
	w.untouched()
}

func TestRemoveLocksOwnersOnlyWhenOwnerLeavesRoot(t *testing.T) {
	w := newWorld(t)
	w.grant("viewerA", "root", role.Viewer)
	w.reset()
	require.NoError(t, w.remove("owner", "root", "viewerA"))
	require.NoError(t, w.remove("ownerA", "A", "ownerA"))
	assert.Equal(t, 0, w.s.count("lock-owners"), "no root owner involved, no lock")

	_ = w.remove("owner", "root", "owner")
	assert.Equal(t, 1, w.s.count("lock-owners"))
}

// EC-33: removing is revoking; an admin cannot remove an owner.
func TestRemoveRoleCeiling(t *testing.T) {
	w := newWorld(t)
	requireCode(t, w.remove("adminA", "A", "ownerA"), apperr.RoleNotGrantable)
	w.untouched()

	require.NoError(t, w.remove("adminA", "A", "viewerA"))
	require.NoError(t, w.remove("adminA", "A", "adminA"), "an admin may remove an admin, themself included")
}

func TestRemoveAuthorization(t *testing.T) {
	w := newWorld(t)
	requireCode(t, w.remove("viewerA", "A", "analystA"), apperr.Forbidden)
	requireCode(t, w.remove("analystA", "A", "viewerA"), apperr.Forbidden)
	requireCode(t, w.remove("adminB", "A", "viewerA"), apperr.NotFound)
	requireCode(t, w.remove("nobody", "A", "viewerA"), apperr.NotFound)
	w.untouched()
}

func TestRemoveMissingAssignment(t *testing.T) {
	w := newWorld(t)
	requireCode(t, w.remove("adminA", "A", "nobody"), apperr.NotFound)
	requireCode(t, w.remove("adminA", "A", "member"), apperr.NotFound) // assigned on a descendant only
	w.untouched()
}

// EC-42
// Test-ID: T-U-45
func TestRemoveOwnAssignment(t *testing.T) {
	w := newWorld(t)
	require.NoError(t, w.remove("adminA", "A", "adminA"))
	assert.Equal(t, role.Role(""), w.roleOf("adminA", "A"))
	assert.Equal(t, []string{"member.revoked"}, w.audit.actions())
}

func TestRemoveValidation(t *testing.T) {
	w := newWorld(t)
	err := uc.NewRemoveMember(w.deps).Execute(context.Background(), uc.RemoveMemberInput{Actor: w.ids["adminA"], NodeID: w.id("A")})
	requireCode(t, err, apperr.ValidationFailed)
	err = uc.NewRemoveMember(w.deps).Execute(context.Background(), uc.RemoveMemberInput{Actor: w.ids["adminA"], UserID: w.ids["viewerA"]})
	requireCode(t, err, apperr.ValidationFailed)
}
