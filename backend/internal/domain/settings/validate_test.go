package settings_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
)

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got)
}

func nodeSettings() settings.NodeSettings {
	return settings.NodeSettings{NodeID: node.ID(uuid.New())}
}

// Test-ID: T-U-37
func TestInvalidValues(t *testing.T) {
	// EC-139: unknown key.
	_, err := settings.ParseKey("foo")
	requireCode(t, err, apperr.UnknownSetting)
	_, err = nodeSettings().Set("foo", "x")
	requireCode(t, err, apperr.UnknownSetting)
	_, err = nodeSettings().Unset("foo")
	requireCode(t, err, apperr.UnknownSetting)

	bad := []struct {
		key settings.Key
		raw string
	}{
		{settings.DisplayTimezone, "Mars/Base"},
		{settings.DisplayTimezone, "Local"},
		{settings.DisplayTimezone, ""},
		{settings.OperationsEnabled, "yes"},
		{settings.OperationsEnabled, "1"},
		{settings.OperationsEnabled, "True"},
		{settings.OperationsEnabled, ""},
		{settings.MaxOperationAmount, "0"},
		{settings.MaxOperationAmount, "-1"},
		{settings.MaxOperationAmount, "10.5"},
		{settings.MaxOperationAmount, "abc"},
		{settings.MaxOperationAmount, "9223372036854775808"},
		{settings.MaxOperationAmount, ""},
	}
	for _, c := range bad {
		s, err := nodeSettings().Set(c.key, c.raw)
		requireCode(t, err, apperr.InvalidSettingValue)
		_, isSet := s.Value(c.key)
		require.False(t, isSet, "%s=%q must not be stored", c.key, c.raw)
	}
}

func TestValidValues(t *testing.T) {
	for _, k := range settings.Keys() {
		parsed, err := settings.ParseKey(string(k))
		require.NoError(t, err)
		require.Equal(t, k, parsed)
	}
	s := nodeSettings()
	var err error
	for _, c := range []struct {
		key settings.Key
		raw string
	}{
		{settings.DisplayTimezone, "Europe/Kyiv"},
		{settings.DisplayTimezone, "UTC"},
		{settings.OperationsEnabled, "false"},
		{settings.MaxOperationAmount, "1"},
		{settings.MaxOperationAmount, "9223372036854775807"},
	} {
		s, err = s.Set(c.key, c.raw)
		require.NoError(t, err, "%s=%s", c.key, c.raw)
		got, ok := s.Value(c.key)
		require.True(t, ok)
		require.Equal(t, c.raw, got)
	}

	// Unset returns to inheritance and is non-mutating.
	orig := s
	for _, k := range settings.Keys() {
		s, err = s.Unset(k)
		require.NoError(t, err)
		_, ok := s.Value(k)
		require.False(t, ok)
		_, ok = orig.Value(k)
		require.True(t, ok)
	}
	require.Equal(t, money.Amount(1), *mustMax(t, "1"))
}

func mustMax(t *testing.T, raw string) *money.Amount {
	t.Helper()
	v, err := settings.ParseMaxOperation(raw)
	require.NoError(t, err)
	return &v
}
