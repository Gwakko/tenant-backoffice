//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	bal "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// ledgerClock is a settable clock whose readings strictly increase, so the
// order of created_at on a node is the order of the calls that stamped it.
type ledgerClock struct {
	mu   sync.Mutex
	last time.Time
	skew time.Duration
}

func (c *ledgerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC().Add(c.skew).Truncate(time.Microsecond)
	if !now.After(c.last) {
		now = c.last.Add(time.Microsecond)
	}
	c.last = now
	return now
}

// advance moves the clock forward for everything that follows.
func (c *ledgerClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.skew += d
}

// ledgerCases are the balance use cases wired to the real adapters.
type ledgerCases struct {
	w        *world
	audit    *recAudit
	clock    *ledgerClock
	deps     bal.Deps
	credit   *bal.Credit
	debit    *bal.Debit
	transfer *bal.Transfer
	get      *bal.GetBalance
}

func (w *world) ledgerCases() *ledgerCases {
	log := w.runnerLog()
	a := &recAudit{}
	clk := &ledgerClock{}
	d := bal.Deps{
		Runner: w.runner, Checker: w.checker, Nodes: w.repo, Balances: postgres.NewBalanceRepo(log),
		Ops: postgres.NewOperationRepo(log), Keys: postgres.NewIdempotencyRepo(log),
		Limits: postgres.NewLimitReader(log), Audit: a, Clock: clk, IDs: v7IDs{},
	}
	return &ledgerCases{
		w: w, audit: a, clock: clk, deps: d, credit: bal.NewCredit(d), debit: bal.NewDebit(d),
		transfer: bal.NewTransfer(d), get: bal.NewGetBalance(d),
	}
}

func ledgerHash(parts ...string) []byte {
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return h[:]
}

func (c *ledgerCases) operate(uc interface {
	Execute(context.Context, bal.OperateInput) (bal.OperateOutput, error)
}, route, who, nodeKey, amount, key string) (bal.OperateOutput, error) {
	return uc.Execute(context.Background(), bal.OperateInput{
		Actor: c.w.users[who], RequestID: "t", NodeID: c.w.id(nodeKey), Amount: amount, IdempotencyKey: key,
		RequestHash: ledgerHash(route, c.w.id(nodeKey).String(), amount),
	})
}

func (c *ledgerCases) creditAs(who, nodeKey, amount, key string) (bal.OperateOutput, error) {
	return c.operate(c.credit, "credits", who, nodeKey, amount, key)
}

func (c *ledgerCases) debitAs(who, nodeKey, amount, key string) (bal.OperateOutput, error) {
	return c.operate(c.debit, "debits", who, nodeKey, amount, key)
}

func (c *ledgerCases) transferAs(who, from, to, amount, key string) (bal.TransferOutput, error) {
	return c.transfer.Execute(context.Background(), bal.TransferInput{
		Actor: c.w.users[who], RequestID: "t", FromNodeID: c.w.id(from), ToNodeID: c.w.id(to),
		Amount: amount, IdempotencyKey: key,
		RequestHash: ledgerHash("transfers", c.w.id(from).String(), c.w.id(to).String(), amount),
	})
}

// balanceOf reads the stored balance as the owner; a node without a row is 0.
func (w *world) balanceOf(nodeKey string) int64 {
	w.t.Helper()
	var v int64
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
		`SELECT coalesce((SELECT amount FROM balances WHERE node_id = $1), 0)`, uuid.UUID(w.id(nodeKey))).Scan(&v))
	return v
}

func (w *world) countRows(table string) int {
	w.t.Helper()
	var n int
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// seedSettingRaw stores a setting as the owner (jsonValue is a JSON literal).
func (w *world) seedLimit(nodeKey, key, jsonValue string) {
	w.t.Helper()
	_, err := w.db.Owner.Exec(context.Background(),
		`INSERT INTO node_settings (node_id, key, value, updated_at, updated_by)
		 VALUES ($1, $2, $3::jsonb, now(), $4)
		 ON CONFLICT (node_id, key) DO UPDATE SET value = EXCLUDED.value`,
		uuid.UUID(w.id(nodeKey)), key, jsonValue, uuid.UUID(w.users["ownerRoot"]))
	require.NoError(w.t, err)
}

func (w *world) setStatus(nodeKey, status string) {
	w.t.Helper()
	_, err := w.db.Owner.Exec(context.Background(),
		`UPDATE nodes SET status = $2 WHERE id = $1`, uuid.UUID(w.id(nodeKey)), status)
	require.NoError(w.t, err)
}

// requireLedgerInvariant checks INV-L1 and the shape of the ledger for every
// node (read as the owner): the stored balance equals the signed sum of its
// operations, the chain of balance_after in created_at order follows each
// operation, and both legs of a transfer share the transfer id and the amount.
func (w *world) requireLedgerInvariant(msg string) {
	w.t.Helper()
	ctx := context.Background()
	rows, err := w.db.Owner.Query(ctx, `
		SELECT n.id::text, coalesce(b.amount, 0)::bigint,
		       coalesce(sum(CASE WHEN o.kind IN ('credit','transfer_in') THEN o.amount ELSE -o.amount END), 0)::bigint
		  FROM nodes n
		  LEFT JOIN balances b ON b.node_id = n.id
		  LEFT JOIN operations o ON o.node_id = n.id
		 GROUP BY n.id, b.amount`)
	require.NoError(w.t, err)
	for rows.Next() {
		var id string
		var stored, summed int64
		require.NoError(w.t, rows.Scan(&id, &stored, &summed))
		require.Equal(w.t, summed, stored, "%s: balance of %s differs from its operations", msg, id)
	}
	require.NoError(w.t, rows.Err())
	rows.Close()

	rows, err = w.db.Owner.Query(ctx,
		`SELECT node_id::text, kind, amount, balance_after FROM operations ORDER BY node_id, created_at, id`)
	require.NoError(w.t, err)
	prev := map[string]int64{}
	for rows.Next() {
		var id, kind string
		var amount, after int64
		require.NoError(w.t, rows.Scan(&id, &kind, &amount, &after))
		delta := -amount
		if kind == string(dbalance.Credit) || kind == string(dbalance.TransferIn) {
			delta = amount
		}
		require.Equal(w.t, prev[id]+delta, after, "%s: balance_after chain of %s broken at a %s of %d", msg, id, kind, amount)
		prev[id] = after
	}
	require.NoError(w.t, rows.Err())
	rows.Close()

	rows, err = w.db.Owner.Query(ctx, `
		SELECT transfer_id::text, count(*),
		       count(*) FILTER (WHERE kind = 'transfer_out'), count(*) FILTER (WHERE kind = 'transfer_in'),
		       min(amount), max(amount), count(DISTINCT node_id)
		  FROM operations WHERE transfer_id IS NOT NULL GROUP BY transfer_id`)
	require.NoError(w.t, err)
	for rows.Next() {
		var id string
		var total, outs, ins, lo, hi, nodes int64
		require.NoError(w.t, rows.Scan(&id, &total, &outs, &ins, &lo, &hi, &nodes))
		require.Equal(w.t, []int64{2, 1, 1, 2}, []int64{total, outs, ins, nodes}, "%s: transfer %s is not one out and one in", msg, id)
		require.Equal(w.t, lo, hi, "%s: legs of transfer %s differ in amount", msg, id)
	}
	require.NoError(w.t, rows.Err())
	rows.Close()
}
