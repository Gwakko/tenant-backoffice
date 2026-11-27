package settings_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
)

// Test-ID: T-U-39
func TestUnsetFallsBack(t *testing.T) {
	w := newWorld(t)
	w.seed("A", dset.DisplayTimezone, "Europe/Kyiv")
	w.seed("A1", dset.DisplayTimezone, "Asia/Tokyo")

	out, err := w.unset("adminA", "A1", "display_timezone")
	require.NoError(t, err)
	assert.Nil(t, out.Item.Own)
	assert.Equal(t, "Europe/Kyiv", out.Item.Effective)
	require.NotNil(t, out.Item.SourceNodeID)
	assert.Equal(t, w.id("A"), *out.Item.SourceNodeID)
	require.Equal(t, []string{"settings.unset"}, w.audit.actions())
	e := w.audit.entries[0]
	assert.Equal(t, w.id("A1"), e.NodeID)
	assert.Equal(t, "Asia/Tokyo", e.Before["value"])
	assert.Equal(t, "display_timezone", e.After["key"])

	// A second unset finds no override: nothing written, nothing audited.
	writes := w.repo.writes
	out, err = w.unset("adminA", "A1", "display_timezone")
	require.NoError(t, err)
	assert.Equal(t, "Europe/Kyiv", out.Item.Effective)
	assert.Equal(t, writes, w.repo.writes)
	assert.Len(t, w.audit.entries, 1)
}

func TestUnsetOnRootFallsBackToDefault(t *testing.T) {
	w := newWorld(t)
	w.seed("root", dset.DisplayTimezone, "Asia/Tokyo")
	out, err := w.unset("owner", "root", "display_timezone")
	require.NoError(t, err)
	assert.Equal(t, "UTC", out.Item.Effective)
	assert.Nil(t, out.Item.SourceNodeID)
}

func TestUnsetRights(t *testing.T) {
	w := newWorld(t)
	w.seed("A1", dset.OperationsEnabled, "false")

	_, err := w.unset("viewerA", "A1", "operations_enabled")
	assert.Equal(t, apperr.Forbidden, code(t, err))
	_, err = w.unset("adminB", "A1", "operations_enabled")
	assert.Equal(t, apperr.NotFound, code(t, err))
	_, err = w.unset("nobody", "A1", "operations_enabled")
	assert.Equal(t, apperr.NotFound, code(t, err))
	_, err = w.unset("adminA", "A1", "foo")
	assert.Equal(t, apperr.UnknownSetting, code(t, err))

	assert.Equal(t, "false", w.repo.values[key{w.id("A1"), dset.OperationsEnabled}])
	assert.Empty(t, w.audit.entries)
}
