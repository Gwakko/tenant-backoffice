package role_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
)

// Test-ID: T-U-16
func TestCanGrantCanRevoke(t *testing.T) {
	roles := []role.Role{role.Viewer, role.Analyst, role.Admin, role.Owner}
	// Rows: actor role; columns: target role. Only admin and owner hold
	// member.manage; nobody may grant or revoke a role above their own (EC-33).
	want := map[role.Role][4]bool{
		role.Viewer:  {false, false, false, false},
		role.Analyst: {false, false, false, false},
		role.Admin:   {true, true, true, false},
		role.Owner:   {true, true, true, true},
	}
	for _, actor := range roles {
		for j, target := range roles {
			require.Equal(t, want[actor][j], role.CanGrant(actor, target), "grant %s -> %s", actor, target)
			require.Equal(t, want[actor][j], role.CanRevoke(actor, target), "revoke %s by %s", target, actor)
		}
	}
	require.False(t, role.CanGrant(role.Owner, role.Role("")))
	require.False(t, role.CanGrant(role.Role(""), role.Viewer))
}

func TestRoleNotGrantableCode(t *testing.T) {
	err := apperr.New(apperr.RoleNotGrantable, "x")
	require.True(t, errors.Is(err, apperr.New(apperr.RoleNotGrantable, "")))
}
