//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Test-ID: T-I-18
// EC-64: the filters of the listings are values, never SQL. Hostile text in the
// name search, the status, the node type, the operation kinds and the audit
// action is bound as a parameter: it either matches nothing or is refused as a
// value of the wrong type, never widens the result, never changes a table.
func TestFiltersAreParameterized(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a1u1 := w.id("A1u1")
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, kind := range []string{"credit", "debit", "credit"} {
		_, err := w.db.Owner.Exec(ctx,
			`INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at)
			 VALUES ($1, $2, $3, 1, 1, $4, $5)`,
			uuid.Must(uuid.NewV7()), uuid.UUID(a1u1), kind, uuid.UUID(w.users["adminA"]), now.Add(time.Duration(i)*time.Microsecond))
		require.NoError(t, err)
	}
	for _, action := range []string{"node.created", "node.renamed"} {
		_, err := w.db.Owner.Exec(ctx,
			`INSERT INTO audit_log (id, occurred_at, actor_id, node_id, action, target_type, target_id, before, after, request_id)
			 VALUES ($1, $2, $3, $4, $5, 'node', $4, NULL, '{"name":"x"}'::jsonb, 'req-filters')`,
			uuid.Must(uuid.NewV7()), now, uuid.UUID(w.users["adminA"]), uuid.UUID(a1u1), action)
		require.NoError(t, err)
	}
	tables := func() map[string]int {
		out := map[string]int{}
		for _, tbl := range []string{"nodes", "operations", "audit_log", "role_assignments", "users", "balances"} {
			var n int
			require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n))
			out[tbl] = n
		}
		return out
	}
	before := tables()

	hostile := []string{
		"%' OR 1=1 --", "'; DROP TABLE nodes; --", `" OR ""="`, "%", "_", `\`, "' UNION SELECT password_hash FROM users --",
		"a1u1') OR ('1'='1", "\x00", "$1", "{{.}}", "a1u1'; SELECT pg_sleep(5); --",
	}

	// Control: the honest filters work.
	w.do("adminA", func(ctx context.Context, tx port.Tx) {
		kids, err := w.repo.FindChildren(ctx, tx, w.id("A1"), port.ChildFilter{Query: "a1u"}, nil, 10)
		require.NoError(t, err)
		assert.Len(t, kids, 2)
		kids, err = w.repo.FindChildren(ctx, tx, w.id("A1"), port.ChildFilter{Status: node.StatusActive}, nil, 10)
		require.NoError(t, err)
		assert.Len(t, kids, 2)
		ops, err := postgres.NewOperationRepo(w.runnerLog()).List(ctx, tx, port.OperationFilter{ScopeNodeID: w.id("A"), Kinds: []balance.Kind{"credit"}}, nil, 10)
		require.NoError(t, err)
		assert.Len(t, ops, 2)
		act := audit.Action("node.created")
		entries, err := postgres.NewAuditRepo(w.runnerLog()).List(ctx, tx, port.AuditFilter{ScopeNodeID: w.id("A"), Action: &act}, nil, 10)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})

	// empty is the only acceptable outcome of a hostile value that is bound as data;
	// an error (a value of the wrong type for an enum column) is acceptable too.
	empty := func(label string, n int, err error) {
		t.Helper()
		if err == nil {
			assert.Zero(t, n, "%s widened the result", label)
		}
	}
	for _, h := range hostile {
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			kids, err := w.repo.FindChildren(ctx, tx, w.id("A1"), port.ChildFilter{Query: h}, nil, 10)
			empty("name search "+h, len(kids), err)
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			kids, err := w.repo.FindChildren(ctx, tx, w.id("A1"), port.ChildFilter{Status: node.Status(h)}, nil, 10)
			empty("status "+h, len(kids), err)
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			kids, err := w.repo.ListActiveByType(ctx, tx, node.Type(h), "", nil, 10)
			empty("type "+h, len(kids), err)
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			kids, err := w.repo.ListActiveByType(ctx, tx, node.TypeUnit, h, nil, 10)
			empty("type search "+h, len(kids), err)
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			ops, err := postgres.NewOperationRepo(w.runnerLog()).List(ctx, tx,
				port.OperationFilter{ScopeNodeID: w.id("A"), Kinds: []balance.Kind{balance.Kind(h)}}, nil, 10)
			empty("kind "+h, len(ops), err)
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			// An honest kind next to a hostile one must not turn the filter into "all".
			ops, err := postgres.NewOperationRepo(w.runnerLog()).List(ctx, tx,
				port.OperationFilter{ScopeNodeID: w.id("A"), NodeID: &a1u1, Kinds: []balance.Kind{"credit", balance.Kind(h)}}, nil, 10)
			if err == nil {
				assert.LessOrEqual(t, len(ops), 2, "kind with a node filter %q widened the result", h)
				for _, o := range ops {
					assert.Equal(t, balance.Kind("credit"), o.Operation.Kind)
				}
			}
			return err
		})
		_ = w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			act := audit.Action(h)
			entries, err := postgres.NewAuditRepo(w.runnerLog()).List(ctx, tx, port.AuditFilter{ScopeNodeID: w.id("A"), Action: &act}, nil, 10)
			empty("action "+h, len(entries), err)
			return err
		})
	}

	assert.Equal(t, before, tables(), "no table changed")
	assert.Empty(t, w.violations(), "the tree is intact")
}
