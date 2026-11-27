package settings_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

func TestSetWritesAndAudits(t *testing.T) {
	w := newWorld(t)
	out, err := w.set("adminA", "A1", "display_timezone", str("Europe/Kyiv"))
	require.NoError(t, err)
	require.NotNil(t, out.Item.Own)
	assert.Equal(t, "Europe/Kyiv", *out.Item.Own)
	assert.Equal(t, "Europe/Kyiv", out.Item.Effective)
	assert.Equal(t, w.id("A1"), *out.Item.SourceNodeID)

	_, err = w.set("adminA", "A1", "display_timezone", str("Asia/Tokyo"))
	require.NoError(t, err)
	require.Equal(t, []string{"settings.set", "settings.set"}, w.audit.actions())
	first, second := w.audit.entries[0], w.audit.entries[1]
	assert.Equal(t, w.id("A1"), first.NodeID)
	assert.Equal(t, w.actors["adminA"], first.ActorID)
	assert.Equal(t, "r1", first.RequestID)
	assert.NotContains(t, first.Before, "value")
	assert.Equal(t, "Europe/Kyiv", first.After["value"])
	assert.Equal(t, "Europe/Kyiv", second.Before["value"])
	assert.Equal(t, "Asia/Tokyo", second.After["value"])
	assert.Equal(t, "display_timezone", second.After["key"])
}

// Test-ID: T-U-63
// EC-101: setting the value a node already has changes nothing.
func TestSetSameValueIsNoop(t *testing.T) {
	w := newWorld(t)
	_, err := w.set("adminA", "A1", "operations_enabled", boolean(false))
	require.NoError(t, err)
	_, err = w.set("adminA", "A1", "max_operation_amount", str("0500"))
	assert.Equal(t, apperr.InvalidSettingValue, code(t, err), "no leading zeros")
	_, err = w.set("adminA", "A1", "max_operation_amount", str("500"))
	require.NoError(t, err)
	require.Len(t, w.audit.entries, 2)
	writes := w.repo.writes

	for range 2 {
		_, err = w.set("adminA", "A1", "operations_enabled", boolean(false))
		require.NoError(t, err)
		_, err = w.set("adminA", "A1", "max_operation_amount", str("500"))
		require.NoError(t, err)
	}
	assert.Equal(t, writes, w.repo.writes)
	assert.Len(t, w.audit.entries, 2)
}

// EC-139
func TestSetRejectsInvalidValues(t *testing.T) {
	w := newWorld(t)
	_, err := w.set("adminA", "A1", "foo", str("x"))
	assert.Equal(t, apperr.UnknownSetting, code(t, err))

	for name, tc := range map[string]struct {
		key string
		v   uc.RawValue
	}{
		"unknown zone":        {"display_timezone", str("Mars/Base")},
		"local zone":          {"display_timezone", str("Local")},
		"empty zone":          {"display_timezone", str("")},
		"zone as bool":        {"display_timezone", boolean(true)},
		"zero limit":          {"max_operation_amount", str("0")},
		"limit as number":     {"max_operation_amount", uc.RawValue{Kind: uc.KindOther}},
		"limit as bool":       {"max_operation_amount", boolean(true)},
		"limit not a number":  {"max_operation_amount", str("ten")},
		"bool as string":      {"operations_enabled", str("true")},
		"bool as other":       {"operations_enabled", uc.RawValue{Kind: uc.KindOther}},
		"zero value is other": {"operations_enabled", uc.RawValue{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.set("adminA", "A1", tc.key, tc.v)
			assert.Equal(t, apperr.InvalidSettingValue, code(t, err))
		})
	}
	assert.Empty(t, w.repo.values)
	assert.Empty(t, w.audit.entries)
}

func TestSetRights(t *testing.T) {
	w := newWorld(t)
	_, err := w.set("viewerA", "A1", "operations_enabled", boolean(false))
	assert.Equal(t, apperr.Forbidden, code(t, err), "viewer holds settings.read only")
	_, err = w.set("adminB", "A1", "operations_enabled", boolean(false))
	assert.Equal(t, apperr.NotFound, code(t, err))
	_, err = w.set("nobody", "A1", "operations_enabled", boolean(false))
	assert.Equal(t, apperr.NotFound, code(t, err))
	assert.Empty(t, w.repo.values)
	assert.Empty(t, w.audit.entries)
	assert.Zero(t, w.repo.writes)
}

// Test-ID: T-U-63
// A descendant cannot relax what an ancestor restricts (EC-140, EC-141): the
// write is accepted, the effective value stays that of the ancestor.
func TestSetDescendantCannotRelax(t *testing.T) {
	w := newWorld(t)
	w.seed("A", dset.OperationsEnabled, "false")
	w.seed("A", dset.MaxOperationAmount, "1000")

	out, err := w.set("owner", "A1", "operations_enabled", boolean(true))
	require.NoError(t, err)
	require.NotNil(t, out.Item.Own)
	assert.Equal(t, "true", *out.Item.Own)
	assert.Equal(t, "false", out.Item.Effective)
	assert.Equal(t, w.id("A"), *out.Item.SourceNodeID)

	out, err = w.set("owner", "A1", "max_operation_amount", str("5000"))
	require.NoError(t, err)
	assert.Equal(t, "1000", out.Item.Effective)
	assert.Equal(t, w.id("A"), *out.Item.SourceNodeID)

	// A stricter value does take effect.
	out, err = w.set("owner", "A1", "max_operation_amount", str("200"))
	require.NoError(t, err)
	assert.Equal(t, "200", out.Item.Effective)
	assert.Equal(t, w.id("A1"), *out.Item.SourceNodeID)
}

func TestSetReportsHiddenSource(t *testing.T) {
	w := newWorld(t)
	w.seed("A", dset.OperationsEnabled, "false")
	out, err := w.set("adminA1", "A1", "operations_enabled", boolean(true))
	require.NoError(t, err)
	assert.Equal(t, "false", out.Item.Effective)
	assert.Nil(t, out.Item.SourceNodeID)
}
