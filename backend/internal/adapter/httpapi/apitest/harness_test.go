//go:build integration

package apitest_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// Test-ID: T-A-44
// The pool of the API runs as tb_app (row-level security applies), not as the admin of the test server.
func TestAPIHarnessRunsAsApplicationRole(t *testing.T) {
	db := testkit.NewDB(t)
	conn, err := pgx.Connect(context.Background(), db.AppURL(t))
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var user string
	var bypass bool
	require.NoError(t, conn.QueryRow(context.Background(),
		`SELECT current_user, (SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user)`).Scan(&user, &bypass))
	require.Equal(t, "tb_app", user)
	require.False(t, bypass)
}
