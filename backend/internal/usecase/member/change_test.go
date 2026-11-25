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

func (w *world) change(actor, node, target, r string) (uc.ChangeMemberRoleOutput, error) {
	return uc.NewChangeMemberRole(w.deps).Execute(context.Background(), uc.ChangeMemberRoleInput{
		Actor: w.ids[actor], RequestID: "r1", NodeID: w.id(node), UserID: w.ids[target], Role: r,
	})
}

func TestChangeRole(t *testing.T) {
	w := newWorld(t)
	out, err := w.change("adminA", "A", "viewerA", "analyst")
	require.NoError(t, err)
	assert.Equal(t, role.Analyst, out.Member.Role)
	assert.Equal(t, w.email("viewerA"), out.Member.Email)
	assert.Equal(t, role.Analyst, w.roleOf("viewerA", "A"))
	require.Equal(t, []string{"member.changed"}, w.audit.actions())
	assert.Equal(t, "viewer", w.audit.entries[0].Before["role"])
	assert.Equal(t, "analyst", w.audit.entries[0].After["role"])
}

func TestChangeRoleSameRoleIsNoop(t *testing.T) {
	w := newWorld(t)
	out, err := w.change("adminA", "A", "viewerA", "viewer")
	require.NoError(t, err)
	assert.Equal(t, role.Viewer, out.Member.Role)
	w.untouched()
}

// EC-33: both the old and the new role must be within the actor's rank.
func TestChangeRoleCeiling(t *testing.T) {
	w := newWorld(t)
	_, err := w.change("adminA", "A", "viewerA", "owner")
	requireCode(t, err, apperr.RoleNotGrantable)
	_, err = w.change("adminA", "A", "ownerA", "viewer")
	requireCode(t, err, apperr.RoleNotGrantable)
	_, err = w.change("adminA", "A", "ownerA", "owner")
	requireCode(t, err, apperr.RoleNotGrantable) // even a no-op change of a role above one's own
	w.untouched()
}

func TestChangeRoleAuthorization(t *testing.T) {
	w := newWorld(t)
	_, err := w.change("viewerA", "A", "analystA", "viewer")
	requireCode(t, err, apperr.Forbidden)
	_, err = w.change("adminB", "A", "viewerA", "analyst")
	requireCode(t, err, apperr.NotFound)
	_, err = w.change("adminA", "A", "nobody", "viewer")
	requireCode(t, err, apperr.NotFound)
	w.untouched()
}

// EC-35: demoting the last owner of the root is refused, demoting another is not.
func TestChangeRoleLastRootOwner(t *testing.T) {
	w := newWorld(t)
	_, err := w.change("owner", "root", "owner", "admin")
	requireCode(t, err, apperr.LastRootOwner)
	w.untouched()

	w.addUser("owner2", "active")
	w.grant("owner2", "root", role.Owner)
	w.reset()
	_, err = w.change("owner", "root", "owner2", "admin")
	require.NoError(t, err)
	_, err = w.change("owner", "root", "owner", "admin")
	requireCode(t, err, apperr.LastRootOwner)
}

// EC-37 for demotion: a non-root node loses its last owner without objection.
func TestChangeRoleLastOwnerOfInnerNode(t *testing.T) {
	w := newWorld(t)
	_, err := w.change("ownerA", "A", "ownerA", "admin")
	require.NoError(t, err)
}

func TestChangeRoleValidation(t *testing.T) {
	w := newWorld(t)
	_, err := w.change("adminA", "A", "viewerA", "boss")
	requireCode(t, err, apperr.ValidationFailed)
	w.untouched()
}
