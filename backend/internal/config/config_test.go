package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

// Test-ID: T-CFG-01
func TestLoadMissingRequiredNamesVariable(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", "")
	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "TB_HTTP_ADDR")
}

// Test-ID: T-CFG-02
func TestLoadDefaults(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, ":0", cfg.HTTPAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
}

// Test-ID: T-CFG-03
func TestLoadRejectsBadLogSettings(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	t.Setenv("TB_LOG_FORMAT", "xml")
	_, err := config.Load()
	require.ErrorContains(t, err, "TB_LOG_FORMAT")

	t.Setenv("TB_LOG_FORMAT", "text")
	t.Setenv("TB_LOG_LEVEL", "loud")
	_, err = config.Load()
	require.ErrorContains(t, err, "TB_LOG_LEVEL")
}

func TestLoadDatabaseDefaultsAndOverrides(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, cfg.DB.URL)
	require.EqualValues(t, 10, cfg.DB.MaxConns)
	require.Equal(t, 5*time.Second, cfg.DB.StatementTimeout)
	require.Equal(t, 10*time.Second, cfg.DB.IdleInTxTimeout)

	t.Setenv("TB_DB_URL", "postgres://tb_app@db:5432/x")
	t.Setenv("TB_DB_MAX_CONNS", "3")
	t.Setenv("TB_DB_STATEMENT_TIMEOUT", "2s")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://tb_app@db:5432/x", cfg.DB.URL)
	require.EqualValues(t, 3, cfg.DB.MaxConns)
	require.Equal(t, 2*time.Second, cfg.DB.StatementTimeout)
}

func TestLoadHTTPDefaultsAreSafe(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.EqualValues(t, 65536, cfg.HTTP.MaxBodyBytes)
	require.False(t, cfg.HTTP.TrustForwardedFor)
	require.Empty(t, cfg.HTTP.CursorKey)
	require.Equal(t, time.Hour, cfg.HTTP.CursorTTL)
	require.Equal(t, 5, cfg.Rate.LoginPairPerMin)
	require.Equal(t, 20, cfg.Rate.LoginIPPerMin)
	require.Equal(t, 60, cfg.Rate.MutationPerMin)
	keys, err := cfg.HTTP.CursorKeys()
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestLoadCursorKeyValidation(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	good := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes
	t.Setenv("TB_CURSOR_KEY", good)
	t.Setenv("TB_CURSOR_KEY_PREVIOUS", good)
	cfg, err := config.Load()
	require.NoError(t, err)
	keys, err := cfg.HTTP.CursorKeys()
	require.NoError(t, err)
	require.Len(t, keys, 2)

	t.Setenv("TB_CURSOR_KEY", "c2hvcnQ=")
	_, err = config.Load()
	require.ErrorContains(t, err, "TB_CURSOR_KEY")

	t.Setenv("TB_CURSOR_KEY", "!!not base64!!")
	_, err = config.Load()
	require.ErrorContains(t, err, "TB_CURSOR_KEY")

	t.Setenv("TB_CURSOR_KEY", "")
	_, err = config.Load()
	require.ErrorContains(t, err, "TB_CURSOR_KEY_PREVIOUS")
}

func TestLoadRejectsBadHTTPAndRateSettings(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	t.Setenv("TB_HTTP_MAX_BODY_BYTES", "0")
	_, err := config.Load()
	require.ErrorContains(t, err, "TB_HTTP_MAX_BODY_BYTES")

	t.Setenv("TB_HTTP_MAX_BODY_BYTES", "65536")
	t.Setenv("TB_RATE_MUTATION_PER_MIN", "0")
	_, err = config.Load()
	require.ErrorContains(t, err, "TB_RATE_MUTATION_PER_MIN")
}
