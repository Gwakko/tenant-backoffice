package config_test

import (
	"testing"

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
