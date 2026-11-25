package role_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
)

// Test-ID: T-U-19
func TestPermissionMatrix(t *testing.T) {
	// Columns: viewer, analyst, admin, owner. Literal copy of spec 01 3.3.
	matrix := []struct {
		perm role.Permission
		want [4]bool
	}{
		{role.NodeRead, [4]bool{true, true, true, true}},
		{role.SettingsRead, [4]bool{true, true, true, true}},
		{role.BalanceRead, [4]bool{true, true, true, true}},
		{role.OperationRead, [4]bool{true, true, true, true}},
		{role.ReportRead, [4]bool{false, true, true, true}},
		{role.AuditRead, [4]bool{false, true, true, true}},
		{role.MemberRead, [4]bool{false, true, true, true}},
		{role.NodeCreate, [4]bool{false, false, true, true}},
		{role.NodeRename, [4]bool{false, false, true, true}},
		{role.NodeMove, [4]bool{false, false, true, true}},
		{role.NodeDeactivate, [4]bool{false, false, true, true}},
		{role.SettingsWrite, [4]bool{false, false, true, true}},
		{role.BalanceCredit, [4]bool{false, false, true, true}},
		{role.BalanceDebit, [4]bool{false, false, true, true}},
		{role.BalanceTransfer, [4]bool{false, false, true, true}},
		{role.MemberManage, [4]bool{false, false, true, true}},
		{role.NodeDelete, [4]bool{false, false, false, true}},
	}
	roles := []role.Role{role.Viewer, role.Analyst, role.Admin, role.Owner}

	require.Len(t, matrix, 17)
	require.Len(t, role.AllPermissions(), 17)
	for i, row := range matrix {
		require.Equal(t, role.AllPermissions()[i], row.perm, "matrix order")
		for j, r := range roles {
			require.Equal(t, row.want[j], role.Allows(r, row.perm), "%s x %s", r, row.perm)
		}
	}
}

func TestAllowsUnknown(t *testing.T) {
	require.False(t, role.Allows(role.Owner, role.Permission("nope")))
	require.False(t, role.Allows(role.Role(""), role.NodeRead))
	require.False(t, role.Allows(role.Role("root"), role.NodeRead))
}

func TestParseRole(t *testing.T) {
	for _, r := range role.All() {
		got, err := role.Parse(string(r))
		require.NoError(t, err)
		require.Equal(t, r, got)
	}
	_, err := role.Parse("Admin")
	require.Error(t, err)
}
