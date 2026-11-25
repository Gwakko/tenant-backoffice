//go:build integration

package testkit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// Test-ID: T-CI-06
func TestParallelDatabases(t *testing.T) {
	ctx := context.Background()
	var names [2]string
	t.Run("pair", func(t *testing.T) {
		for i := range names {
			t.Run("db", func(t *testing.T) {
				t.Parallel()
				db := testkit.NewDB(t)
				names[i] = db.Name
				_, err := db.Owner.Exec(ctx,
					`CREATE TABLE marker (v int)`)
				require.NoError(t, err)
				_, err = db.Owner.Exec(ctx, `INSERT INTO marker VALUES ($1)`, i)
				require.NoError(t, err)

				var n, v int
				require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*), min(v) FROM marker`).Scan(&n, &v))
				require.Equal(t, 1, n, "rows from the other database must not be visible")
				require.Equal(t, i, v)

				var user string
				require.NoError(t, db.App.QueryRow(ctx, `SELECT current_user`).Scan(&user))
				require.Equal(t, "tb_app", user)
				var tbl bool
				require.NoError(t, db.Owner.QueryRow(ctx, `SELECT to_regclass('public.nodes') IS NOT NULL`).Scan(&tbl))
				require.True(t, tbl, "template migrations must be applied")
			})
		}
	})
	require.NotEmpty(t, names[0])
	require.NotEmpty(t, names[1])
	require.NotEqual(t, names[0], names[1])
	for _, n := range names {
		require.False(t, testkit.Exists(ctx, t, n), "database %s must be dropped after the test", n)
	}
}
