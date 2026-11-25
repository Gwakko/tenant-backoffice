package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

func TestPostgresConfigMapsDatabaseSettings(t *testing.T) {
	cfg := config.Config{DB: config.Database{
		URL: "postgres://tb_app@db/x", MaxConns: 4,
		StatementTimeout: 3 * time.Second, IdleInTxTimeout: 6 * time.Second, ConnectTimeout: 9 * time.Second,
	}}
	pc := app.PostgresConfig(cfg)
	require.Equal(t, cfg.DB.URL, pc.URL)
	require.EqualValues(t, 4, pc.MaxConns)
	require.Equal(t, 3*time.Second, pc.StatementTimeout)
	require.Equal(t, 6*time.Second, pc.IdleInTxTimeout)
	require.Equal(t, 9*time.Second, pc.ConnectTimeout)
	require.NoError(t, pc.Validate())
}
