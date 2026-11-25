package postgres

import (
	"context"
	"fmt"

	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// TreeLock implements port.TreeLock with one transaction-scoped advisory lock
// for the whole tree. Structural changes (create, move, delete) take it first,
// so they run one at a time; the lock is released by COMMIT or ROLLBACK.
type TreeLock struct{}

var _ port.TreeLock = TreeLock{}

// NewTreeLock returns the tree lock.
func NewTreeLock() TreeLock { return TreeLock{} }

// Lock blocks until the lock is free, then holds it until the transaction ends.
func (TreeLock) Lock(ctx context.Context, tx port.Tx) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	if _, err := t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('tb.tree', 0))`); err != nil {
		return fmt.Errorf("postgres: lock tree: %w", err)
	}
	return nil
}
