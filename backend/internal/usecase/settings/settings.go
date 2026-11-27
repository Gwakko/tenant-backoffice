// Package settings holds the use cases of per-node settings (UC-S1..S3). Every
// use case checks the actor's right on the node on every call: a node outside
// the actor's subtrees is not_found, one inside them without the right is
// forbidden. Effective values are resolved by the domain over the chain of
// ancestors; the id of an ancestor the actor cannot see is never returned.
package settings

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Deps are the ports every settings use case works through.
type Deps struct {
	Runner   port.TxRunner
	Checker  authz.Checker
	Settings port.SettingsRepo
	Audit    port.AuditWriter
	Clock    port.Clock
}

var (
	readTx  = port.TxOptions{ReadOnly: true}
	writeTx = port.TxOptions{}
)

// Item is one setting of a node as shown to a client.
type Item struct {
	Key dset.Key
	// Own is the raw value set on the node itself; nil when it inherits.
	Own *string
	// Effective is the raw value in force on the node.
	Effective string
	// SourceNodeID is the node the effective value comes from. It is nil when
	// the root default applies and when the source is an ancestor the actor
	// cannot see (EC-143).
	SourceNodeID *dnode.ID
}

func (d Deps) require(ctx context.Context, tx port.Tx, actor user.ActorID, id dnode.ID, perm role.Permission) error {
	_, err := authz.Require(ctx, d.Checker, tx, actor, id, perm)
	return err
}

func requireID(id dnode.ID) error {
	if id.IsZero() {
		return apperr.New(apperr.ValidationFailed, "node_id is required").WithField("node_id", "required")
	}
	return nil
}

// resolve reads the chain of the node and resolves every key. Entries of
// hidden ancestors carry the zero node id, which Resolve reports as "no source".
func (d Deps) resolve(ctx context.Context, tx port.Tx, id dnode.ID) ([]Item, error) {
	entries, err := d.Settings.Chain(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return itemsOf(id, entries)
}

func itemsOf(id dnode.ID, entries []port.SettingEntry) ([]Item, error) {
	chain := make([]dset.NodeSettings, 0, len(entries))
	var own dset.NodeSettings
	for i, e := range entries {
		var holder dnode.ID
		if e.NodeID != nil {
			holder = *e.NodeID
		}
		// Entries come in depth order; consecutive entries of the same holder
		// form one NodeSettings. Hidden holders are told apart by depth.
		if i == 0 || entries[i-1].Depth != e.Depth {
			chain = append(chain, dset.NodeSettings{NodeID: holder})
		}
		ns, err := chain[len(chain)-1].Set(e.Key, e.Value)
		if err != nil {
			return nil, err
		}
		chain[len(chain)-1] = ns
		if e.NodeID != nil && *e.NodeID == id {
			own = ns
		}
	}
	eff := dset.Resolve(chain)
	items := make([]Item, 0, len(dset.Keys()))
	for _, k := range dset.Keys() {
		it := Item{Key: k}
		if v, ok := own.Value(k); ok {
			it.Own = &v
		}
		it.Effective, it.SourceNodeID = effectiveOf(eff, k)
		items = append(items, it)
	}
	return items, nil
}

func effectiveOf(eff dset.Effective, k dset.Key) (string, *dnode.ID) {
	var val string
	var src dnode.ID
	switch k {
	case dset.DisplayTimezone:
		val, src = eff.DisplayTimezone.Value, eff.DisplayTimezone.SourceNodeID
	case dset.OperationsEnabled:
		val, src = boolString(eff.OperationsEnabled.Value), eff.OperationsEnabled.SourceNodeID
	case dset.MaxOperationAmount:
		val, src = eff.MaxOperationAmount.Value.String(), eff.MaxOperationAmount.SourceNodeID
	}
	if src.IsZero() {
		return val, nil
	}
	return val, &src
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func pick(items []Item, k dset.Key) Item {
	for _, it := range items {
		if it.Key == k {
			return it
		}
	}
	return Item{Key: k}
}
