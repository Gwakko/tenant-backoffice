// Package balance is the ledger model: operation kinds, the per-node balance
// aggregate and the pure application of credits, debits and transfers to it.
// It never touches a store and never reads the clock.
package balance

import "github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"

// Kind is the type of a ledger operation.
type Kind string

// Operation kinds. transfer_out and transfer_in always come in pairs that share
// a transfer id.
const (
	Credit      Kind = "credit"
	Debit       Kind = "debit"
	TransferOut Kind = "transfer_out"
	TransferIn  Kind = "transfer_in"
)

// Kinds lists every kind.
func Kinds() []Kind { return []Kind{Credit, Debit, TransferOut, TransferIn} }

// ParseKind validates a raw kind.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if !k.Valid() {
		return "", apperr.New(apperr.ValidationFailed, "unknown operation kind")
	}
	return k, nil
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case Credit, Debit, TransferOut, TransferIn:
		return true
	default:
		return false
	}
}

// Increases reports whether the kind adds to the node balance; the others
// subtract. It is false for an unknown kind.
func (k Kind) Increases() bool {
	switch k {
	case Credit, TransferIn:
		return true
	case Debit, TransferOut:
		return false
	default:
		return false
	}
}

// IsTransfer reports whether the kind is one half of a transfer.
func (k Kind) IsTransfer() bool {
	switch k {
	case TransferOut, TransferIn:
		return true
	case Credit, Debit:
		return false
	default:
		return false
	}
}
