package settings

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Effective is the resolved value of every key for one node. SourceNodeID is
// the node the value comes from; it is the zero ID when the root default applies.
type Effective struct {
	DisplayTimezone    Resolved[string]
	OperationsEnabled  Resolved[bool]
	MaxOperationAmount Resolved[money.Amount]
}

// Resolved is a value together with the node it was taken from.
type Resolved[T any] struct {
	Value        T
	SourceNodeID node.ID
}

// Resolve computes the effective settings from the chain of node settings,
// ordered from the root down to the node itself (nodes without overrides may be
// present or omitted). It is a pure function.
//
//   - display_timezone: the nearest override going up from the node wins.
//   - operations_enabled: logical AND over the chain; the source is the topmost
//     node that sets false, or, when nothing forbids, the nearest node that
//     sets true.
//   - max_operation_amount: the minimum over the chain; on a tie the topmost
//     node is the source.
func Resolve(chain []NodeSettings) Effective {
	eff := Effective{
		DisplayTimezone:    Resolved[string]{Value: DefaultTimezone},
		OperationsEnabled:  Resolved[bool]{Value: DefaultOperationsEnabled},
		MaxOperationAmount: Resolved[money.Amount]{Value: DefaultMaxOperation},
	}
	var limitSet bool
	for _, s := range chain {
		if s.DisplayTimezone != nil {
			eff.DisplayTimezone = Resolved[string]{Value: *s.DisplayTimezone, SourceNodeID: s.NodeID}
		}
		if s.OperationsEnabled != nil {
			switch {
			case !*s.OperationsEnabled && eff.OperationsEnabled.Value:
				eff.OperationsEnabled = Resolved[bool]{Value: false, SourceNodeID: s.NodeID}
			case eff.OperationsEnabled.Value:
				eff.OperationsEnabled = Resolved[bool]{Value: true, SourceNodeID: s.NodeID}
			}
		}
		if s.MaxOperationAmount != nil {
			if !limitSet || *s.MaxOperationAmount < eff.MaxOperationAmount.Value {
				eff.MaxOperationAmount = Resolved[money.Amount]{Value: *s.MaxOperationAmount, SourceNodeID: s.NodeID}
			}
			limitSet = true
		}
	}
	return eff
}
