package settings_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
)

func TestGetDefaultsAndOwn(t *testing.T) {
	w := newWorld(t)
	w.seed("A1", dset.DisplayTimezone, "Europe/Kyiv")
	out, err := w.get("adminA", "A1")
	require.NoError(t, err)
	require.Len(t, out.Items, 3)

	tz := item(out.Items, dset.DisplayTimezone)
	require.NotNil(t, tz.Own)
	assert.Equal(t, "Europe/Kyiv", *tz.Own)
	assert.Equal(t, "Europe/Kyiv", tz.Effective)
	assert.Equal(t, w.id("A1"), *tz.SourceNodeID)

	en := item(out.Items, dset.OperationsEnabled)
	assert.Nil(t, en.Own)
	assert.Equal(t, "true", en.Effective)
	assert.Nil(t, en.SourceNodeID)
	assert.Equal(t, "100000000", item(out.Items, dset.MaxOperationAmount).Effective)
}

func TestGetVisibleSourceIsReported(t *testing.T) {
	w := newWorld(t)
	w.seed("A", dset.DisplayTimezone, "Europe/Kyiv")
	out, err := w.get("adminA", "u1")
	require.NoError(t, err)
	tz := item(out.Items, dset.DisplayTimezone)
	assert.Nil(t, tz.Own)
	assert.Equal(t, "Europe/Kyiv", tz.Effective)
	require.NotNil(t, tz.SourceNodeID)
	assert.Equal(t, w.id("A"), *tz.SourceNodeID)
}

// EC-140, EC-141: restrictions of an ancestor win.
func TestGetChainRestrictions(t *testing.T) {
	w := newWorld(t)
	w.seed("A", dset.OperationsEnabled, "false")
	w.seed("A1", dset.OperationsEnabled, "true")
	w.seed("A", dset.MaxOperationAmount, "1000")
	w.seed("A1", dset.MaxOperationAmount, "5000")
	out, err := w.get("owner", "A1")
	require.NoError(t, err)

	en := item(out.Items, dset.OperationsEnabled)
	assert.Equal(t, "false", en.Effective)
	assert.Equal(t, w.id("A"), *en.SourceNodeID)
	require.NotNil(t, en.Own)
	assert.Equal(t, "true", *en.Own)
	lim := item(out.Items, dset.MaxOperationAmount)
	assert.Equal(t, "1000", lim.Effective)
	assert.Equal(t, w.id("A"), *lim.SourceNodeID)
}

// Test-ID: T-U-64
// EC-143: the value from above the access root is right, its source is hidden.
func TestGetHidesSourceAboveAccessRoot(t *testing.T) {
	w := newWorld(t)
	w.seed("root", dset.DisplayTimezone, "Asia/Tokyo")
	w.seed("A", dset.OperationsEnabled, "false")
	out, err := w.get("adminA1", "u1")
	require.NoError(t, err)

	tz := item(out.Items, dset.DisplayTimezone)
	assert.Equal(t, "Asia/Tokyo", tz.Effective)
	assert.Nil(t, tz.SourceNodeID, "the root is above the access root of adminA1")
	en := item(out.Items, dset.OperationsEnabled)
	assert.Equal(t, "false", en.Effective)
	assert.Nil(t, en.SourceNodeID, "A is above the access root of adminA1")

	// The same data seen from above shows the sources.
	out, err = w.get("owner", "u1")
	require.NoError(t, err)
	assert.Equal(t, w.id("root"), *item(out.Items, dset.DisplayTimezone).SourceNodeID)
}

func TestGetRights(t *testing.T) {
	w := newWorld(t)
	_, err := w.get("viewerA", "A1")
	require.NoError(t, err, "viewer holds settings.read")
	_, err = w.get("adminB", "A1")
	assert.Equal(t, apperr.NotFound, code(t, err))
	_, err = w.get("nobody", "A1")
	assert.Equal(t, apperr.NotFound, code(t, err))
	_, err = w.get("adminA1", "A")
	assert.Equal(t, apperr.NotFound, code(t, err), "an ancestor of the access root is invisible")
}
