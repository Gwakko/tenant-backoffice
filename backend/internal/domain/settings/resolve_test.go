package settings_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
)

func newID() node.ID { return node.ID(uuid.New()) }

func withOps(id node.ID, v bool) settings.NodeSettings {
	return settings.NodeSettings{NodeID: id, OperationsEnabled: &v}
}

func withMax(id node.ID, v money.Amount) settings.NodeSettings {
	return settings.NodeSettings{NodeID: id, MaxOperationAmount: &v}
}

func withTZ(id node.ID, v string) settings.NodeSettings {
	return settings.NodeSettings{NodeID: id, DisplayTimezone: &v}
}

func TestResolveDefaults(t *testing.T) {
	for _, chain := range [][]settings.NodeSettings{nil, {}, {{NodeID: newID()}, {NodeID: newID()}}} {
		eff := settings.Resolve(chain)
		require.Equal(t, "UTC", eff.DisplayTimezone.Value)
		require.True(t, eff.OperationsEnabled.Value)
		require.Equal(t, money.Amount(100000000), eff.MaxOperationAmount.Value)
		require.True(t, eff.DisplayTimezone.SourceNodeID.IsZero())
		require.True(t, eff.OperationsEnabled.SourceNodeID.IsZero())
		require.True(t, eff.MaxOperationAmount.SourceNodeID.IsZero())
	}
}

func TestResolveTimezoneNearestWins(t *testing.T) {
	root, p, c := newID(), newID(), newID()
	eff := settings.Resolve([]settings.NodeSettings{withTZ(root, "Europe/Kyiv"), {NodeID: p}, {NodeID: c}})
	require.Equal(t, "Europe/Kyiv", eff.DisplayTimezone.Value)
	require.Equal(t, root, eff.DisplayTimezone.SourceNodeID)

	eff = settings.Resolve([]settings.NodeSettings{withTZ(root, "Europe/Kyiv"), withTZ(p, "Asia/Tokyo"), {NodeID: c}})
	require.Equal(t, "Asia/Tokyo", eff.DisplayTimezone.Value)
	require.Equal(t, p, eff.DisplayTimezone.SourceNodeID)

	eff = settings.Resolve([]settings.NodeSettings{withTZ(root, "Europe/Kyiv"), withTZ(p, "Asia/Tokyo"), withTZ(c, "UTC")})
	require.Equal(t, "UTC", eff.DisplayTimezone.Value)
	require.Equal(t, c, eff.DisplayTimezone.SourceNodeID)
}

// Test-ID: T-U-38
func TestConstraintsAcrossChain(t *testing.T) {
	root, p, c := newID(), newID(), newID()

	// EC-140: false on P, true on C -> false on C, sourced from P.
	eff := settings.Resolve([]settings.NodeSettings{{NodeID: root}, withOps(p, false), withOps(c, true)})
	require.False(t, eff.OperationsEnabled.Value)
	require.Equal(t, p, eff.OperationsEnabled.SourceNodeID)

	// The topmost prohibition is the source; a deeper false does not replace it.
	eff = settings.Resolve([]settings.NodeSettings{withOps(root, false), withOps(p, false), {NodeID: c}})
	require.False(t, eff.OperationsEnabled.Value)
	require.Equal(t, root, eff.OperationsEnabled.SourceNodeID)

	// false only on the leaf does not affect ancestors' view of the chain.
	eff = settings.Resolve([]settings.NodeSettings{withOps(root, true), {NodeID: p}})
	require.True(t, eff.OperationsEnabled.Value)
	eff = settings.Resolve([]settings.NodeSettings{withOps(root, true), withOps(p, true), withOps(c, false)})
	require.False(t, eff.OperationsEnabled.Value)
	require.Equal(t, c, eff.OperationsEnabled.SourceNodeID)

	// All true: allowed, sourced from the nearest explicit true.
	eff = settings.Resolve([]settings.NodeSettings{withOps(root, true), withOps(p, true), {NodeID: c}})
	require.True(t, eff.OperationsEnabled.Value)
	require.Equal(t, p, eff.OperationsEnabled.SourceNodeID)

	// EC-141: limit 1000 on P, 5000 on C -> 1000.
	eff = settings.Resolve([]settings.NodeSettings{{NodeID: root}, withMax(p, 1000), withMax(c, 5000)})
	require.Equal(t, money.Amount(1000), eff.MaxOperationAmount.Value)
	require.Equal(t, p, eff.MaxOperationAmount.SourceNodeID)

	// A lower limit deeper in the chain wins; a tie keeps the topmost source.
	eff = settings.Resolve([]settings.NodeSettings{withMax(root, 5000), withMax(p, 1000), withMax(c, 700)})
	require.Equal(t, money.Amount(700), eff.MaxOperationAmount.Value)
	require.Equal(t, c, eff.MaxOperationAmount.SourceNodeID)
	eff = settings.Resolve([]settings.NodeSettings{withMax(root, 1000), withMax(p, 1000)})
	require.Equal(t, root, eff.MaxOperationAmount.SourceNodeID)

	// An override above the default replaces the default (the root's value).
	eff = settings.Resolve([]settings.NodeSettings{withMax(root, 900000000), {NodeID: p}})
	require.Equal(t, money.Amount(900000000), eff.MaxOperationAmount.Value)
	require.Equal(t, root, eff.MaxOperationAmount.SourceNodeID)
}

// Property: the effective constraints do not depend on which node in the chain
// holds them, and a descendant can never relax an ancestor.
func TestDescendantCannotRelax(t *testing.T) {
	ids := []node.ID{newID(), newID(), newID(), newID()}
	limits := []money.Amount{10, 500, 1 << 40, money.Max}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			for _, lo := range limits {
				for _, hi := range limits {
					chain := make([]settings.NodeSettings, len(ids))
					for k := range ids {
						chain[k] = settings.NodeSettings{NodeID: ids[k]}
					}
					chain[i] = withMax(ids[i], lo)
					chain[j] = withMax(ids[j], hi)
					eff := settings.Resolve(chain)
					require.Equal(t, money.MinOf(lo, hi), eff.MaxOperationAmount.Value)

					chain[i] = withOps(ids[i], false)
					chain[j] = withOps(ids[j], true)
					require.False(t, settings.Resolve(chain).OperationsEnabled.Value)
				}
			}
		}
	}
}
