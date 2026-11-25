// Package port holds the interfaces use cases depend on. Adapters implement
// them; use cases never see a driver type.
package port

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Tx is an open transaction. It is opaque to use cases: they only pass it to
// repositories, which hand it back to the adapter that created it.
type Tx interface {
	// TxMarker has no behaviour; it keeps arbitrary values from satisfying Tx.
	TxMarker()
}

// Isolation is the transaction isolation level.
type Isolation int

// Isolation levels. The zero value is READ COMMITTED, the default.
const (
	ReadCommitted Isolation = iota
	RepeatableRead
)

// TxOptions configures one transaction.
type TxOptions struct {
	// ReadOnly makes the transaction reject writes. Every read of tenant data
	// still goes through a transaction, because the actor lives in it.
	ReadOnly  bool
	Isolation Isolation
}

// TxRunner runs fn in a transaction on behalf of actor. Tenant tables show
// nothing and accept nothing outside such a transaction.
//
// fn returning an error or panicking rolls back; a nil result commits.
type TxRunner interface {
	InTx(ctx context.Context, actor user.ActorID, opts TxOptions, fn func(ctx context.Context, tx Tx) error) error
}

// TreeLock serialises structural changes of the tree (create, move, delete):
// one lock for the whole tree, held until the transaction ends. After taking it
// a use case rereads the paths it works with.
type TreeLock interface {
	Lock(ctx context.Context, tx Tx) error
}
