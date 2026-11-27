package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

func TestAuthConfigDefaultsAndMapping(t *testing.T) {
	testkit.ClearConfigEnv(t)
	t.Setenv("TB_HTTP_ADDR", ":0")
	t.Setenv("TB_JWT_KEYS_DIR", "/run/keys")
	cfg, err := config.Load()
	require.NoError(t, err)

	ac := app.AuthnConfig(cfg)
	require.Equal(t, "/run/keys", ac.KeysDir)
	require.Equal(t, "k2026a", ac.ActiveKID)
	require.Equal(t, 10*time.Minute, ac.AccessTTL)
	require.Equal(t, 30*time.Second, ac.Leeway)
	require.EqualValues(t, 19456, ac.Argon2MemoryKiB)
	require.EqualValues(t, 2, ac.Argon2Time)
	require.EqualValues(t, 1, ac.Argon2Threads)
	require.NoError(t, ac.Validate())
	require.Equal(t, 168*time.Hour, app.AuthConfig(cfg).RefreshTTL)
}
