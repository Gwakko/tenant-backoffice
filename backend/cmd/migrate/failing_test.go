//go:build integration

package main_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// Test-ID: T-CI-02
func TestFailingMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewEmptyDB(t)
	sqlDB := stdlib.OpenDBFromPool(db.Owner)
	defer func() { _ = sqlDB.Close() }()
	m, err := app.NewMigrator(sqlDB, os.DirFS("testdata/failing"))
	require.NoError(t, err)

	require.Error(t, m.Up(ctx))

	v, err := m.Version(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, v, "version must stay at the last good migration")

	var okTable, firstTable bool
	require.NoError(t, db.Owner.QueryRow(ctx,
		`SELECT to_regclass('public.fail_ok') IS NOT NULL, to_regclass('public.fail_first') IS NOT NULL`).
		Scan(&okTable, &firstTable))
	require.True(t, okTable)
	require.False(t, firstTable, "table from the first statement of the failed migration must not exist")
}
