package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// BeforeCommit installs a hook that runs after the transaction function
// succeeded and before COMMIT; an error from it rolls the transaction back.
// This file is compiled only into tests, so production code cannot reach it.
func (r *TxRunner) BeforeCommit(f func(ctx context.Context, tx pgx.Tx) error) {
	r.beforeCommit = f
}
