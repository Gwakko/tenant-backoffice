//go:build integration

package apitest_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

func settingsOf(t *testing.T, res *apitest.Response) map[string]gen.SettingItem {
	t.Helper()
	requireStatus(t, res, http.StatusOK)
	out := map[string]gen.SettingItem{}
	for _, it := range decode[gen.SettingList](t, res).Items {
		out[string(it.Key)] = it
	}
	return out
}

// Test-ID: T-A-22
// EC-139: unknown keys, wrong types and invalid values are refused, valid ones stored.
func TestAPISettingsValidation(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	n := w.A1u1
	before := count(t, s, `SELECT count(*) FROM node_settings`)

	admin.Put(nodeURL(n, "/settings/foo"), map[string]any{"value": "x"}).RequireProblem(http.StatusNotFound, "unknown_setting")
	admin.Delete(nodeURL(n, "/settings/foo")).RequireProblem(http.StatusNotFound, "unknown_setting")
	for name, c := range map[string]struct {
		key   string
		value any
	}{
		"unknown zone":         {"display_timezone", "Mars/Base"},
		"Local":                {"display_timezone", "Local"},
		"zero amount":          {"max_operation_amount", "0"},
		"amount as number":     {"max_operation_amount", 5},
		"amount not digits":    {"max_operation_amount", "12.5"},
		"bool as string":       {"operations_enabled", "true"},
		"bool as number":       {"operations_enabled", 1},
		"zone as bool":         {"display_timezone", true},
		"zone as object":       {"display_timezone", map[string]string{"a": "b"}},
		"amount over int64":    {"max_operation_amount", "9223372036854775808"},
		"amount leading zeros": {"max_operation_amount", "007"},
	} {
		res := admin.Put(nodeURL(n, "/settings/"+c.key), map[string]any{"value": c.value})
		require.Equal(t, http.StatusUnprocessableEntity, res.Status, "%s: %s", name, res.Body)
		require.Equal(t, "invalid_setting_value", res.Problem().Code, name)
	}
	require.Equal(t, before, count(t, s, `SELECT count(*) FROM node_settings`), "nothing was stored")
	admin.Put(nodeURL(n, "/settings/operations_enabled"), map[string]any{"value": nil}).RequireProblem(http.StatusUnprocessableEntity, "invalid_setting_value")
	admin.Put(nodeURL(n, "/settings/operations_enabled"), `{}`).RequireProblem(http.StatusUnprocessableEntity, "invalid_setting_value")
	admin.Put(nodeURL(n, "/settings/operations_enabled"), map[string]any{"value": true, "extra": 1}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
}

// Proposed ID (not in spec 08 yet): T-A-42
func TestAPISettingsRoundTrip(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	// A1 sets max_operation_amount 5000 (fixture) and inherits the rest. The
	// root default lies above the access root of AdminA, so its source is hidden.
	got := settingsOf(t, admin.Get(nodeURL(w.A1u1, "/settings")))
	require.Len(t, got, 3)
	amount := got["max_operation_amount"]
	eff, err := amount.Effective.AsSettingValue0()
	require.NoError(t, err)
	require.Equal(t, "5000", eff)
	require.Nil(t, amount.Own)
	require.NotNil(t, amount.SourceNodeId)
	require.Equal(t, w.A1, *amount.SourceNodeId)
	zone := got["display_timezone"]
	require.Nil(t, zone.SourceNodeId, "the id of an ancestor above the access root is not revealed")

	// Set, read back, change, unset.
	set := admin.Put(nodeURL(w.A1u1, "/settings/operations_enabled"), map[string]any{"value": false})
	requireStatus(t, set, http.StatusOK)
	it := decode[gen.SettingItem](t, set)
	require.Equal(t, gen.SettingKey("operations_enabled"), it.Key)
	own, err := it.Own.AsSettingItemOwn1()
	require.NoError(t, err)
	require.False(t, own)
	effBool, err := it.Effective.AsSettingValue1()
	require.NoError(t, err)
	require.False(t, effBool)
	require.Equal(t, w.A1u1, *it.SourceNodeId)

	zoneSet := decode[gen.SettingItem](t, admin.Put(nodeURL(w.A1u1, "/settings/display_timezone"), map[string]any{"value": "Europe/Berlin"}))
	tz, err := zoneSet.Effective.AsSettingValue0()
	require.NoError(t, err)
	require.Equal(t, "Europe/Berlin", tz)
	require.Equal(t, []uint{1, 1}, []uint{uint(count(t, s, `SELECT count(*) FROM audit_log WHERE action = 'settings.set' AND node_id = $1 AND request_id <> ''`, w.A1u1)) / 2, 1})

	un := decode[gen.SettingItem](t, admin.Delete(nodeURL(w.A1u1, "/settings/operations_enabled")))
	require.Nil(t, un.Own)
	again := admin.Delete(nodeURL(w.A1u1, "/settings/operations_enabled"))
	requireStatus(t, again, http.StatusOK)
	require.Nil(t, decode[gen.SettingItem](t, again).Own, "unsetting twice is a no-op")
}
