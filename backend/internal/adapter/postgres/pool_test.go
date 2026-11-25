//go:build integration

package postgres_test

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

func TestNewPoolAppliesSessionLimits(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL: db.URL(t), MaxConns: 3,
		StatementTimeout: 2 * time.Second, IdleInTxTimeout: 7 * time.Second, ConnectTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	assert.EqualValues(t, 3, pool.Config().MaxConns)
	var st, idle string
	require.NoError(t, pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&st))
	require.NoError(t, pool.QueryRow(ctx, `SHOW idle_in_transaction_session_timeout`).Scan(&idle))
	assert.Equal(t, "2s", st)
	assert.Equal(t, "7s", idle)
}

func TestNewPoolRejectsBadConfigAndUnreachableServer(t *testing.T) {
	ctx := context.Background()
	_, err := postgres.NewPool(ctx, postgres.Config{})
	require.Error(t, err)

	// The server host comes from the environment; only the port is replaced by
	// one nobody listens on.
	adminURL, ok := config.TestDBAdminURL()
	require.True(t, ok, "TB_TEST_DB_ADMIN_URL is required")
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	require.NoError(t, l.Close())
	u.Host = net.JoinHostPort(u.Hostname(), port)
	u.User = url.User("tb_app")
	_, err = postgres.NewPool(ctx, postgres.Config{
		URL: u.String(), MaxConns: 1,
		StatementTimeout: time.Second, IdleInTxTimeout: time.Second, ConnectTimeout: time.Second,
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "password")
}
