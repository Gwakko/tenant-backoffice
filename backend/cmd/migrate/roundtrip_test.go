//go:build integration

package main_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/migrations"
)

// Test-ID: T-CI-01
func TestMigrationsRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewEmptyDB(t)
	sqlDB := stdlib.OpenDBFromPool(db.Owner)
	defer func() { _ = sqlDB.Close() }()
	m, err := app.NewMigrator(sqlDB, migrations.FS)
	require.NoError(t, err)

	require.NoError(t, m.Up(ctx))
	up, err := m.Version(ctx)
	require.NoError(t, err)
	require.Positive(t, up)

	require.NoError(t, m.DownTo(ctx, 0))
	left, err := app.LeftoverObjects(ctx, sqlDB)
	require.NoError(t, err)
	require.Empty(t, left, "objects left after down-to 0")

	require.NoError(t, m.Up(ctx))
	again, err := m.Version(ctx)
	require.NoError(t, err)
	require.Equal(t, up, again)
	// Seed data after the second up arrives with the seed command (M2+).
}
