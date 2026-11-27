package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
)

// SettingEntry is one explicit override on the chain root -> node.
type SettingEntry struct {
	// NodeID is the node that holds the override. It is nil when that node is
	// an ancestor above the actor's access root: its identity stays hidden, only
	// the value is known.
	NodeID *node.ID
	// Depth is the level of the holder in the tree (the root is 1).
	Depth int
	Key   settings.Key
	// Value is the raw string form of the value (see settings.NodeSettings.Value).
	Value string
}

// SettingWrite is the outcome of SettingsRepo.Set.
type SettingWrite struct {
	// Before is the previous raw value; HadBefore is false for a new override.
	Before    string
	HadBefore bool
	// Changed is false when the stored value already equalled the new one: then
	// nothing was written.
	Changed bool
}

// SettingsRepo stores per-node setting overrides. Every method works inside the
// transaction of the acting user, so row-level security limits it to the
// actor's subtrees. RLS does not check roles: the use case does.
type SettingsRepo interface {
	// Chain returns the overrides of the node and of all its ancestors ordered
	// from the root down. Ancestors above the actor's access root are included
	// with a nil NodeID. A node the actor cannot see gives an empty chain.
	Chain(ctx context.Context, tx Tx, nodeID node.ID) ([]SettingEntry, error)
	// Set stores value (already validated and in canonical form) as the
	// override of key on the node and locks the row. The change is attributed to
	// the actor of the transaction.
	// An equal stored value is left untouched (Changed is false).
	Set(ctx context.Context, tx Tx, nodeID node.ID, key settings.Key, value string, at time.Time) (SettingWrite, error)
	// Unset removes the override and returns its value; found is false when
	// there was none.
	Unset(ctx context.Context, tx Tx, nodeID node.ID, key settings.Key) (before string, found bool, err error)
}
