//go:build integration

package isolation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Test-ID: T-ISO-23
// TestTwoLayerIsolation shows that the two layers of protection hold on their
// own. Layer 1 is the use case with its Checker (rights by subtree); layer 2
// is row-level security in the database.
//
//   - both layers: the use cases on the tb_app pool (what production runs);
//   - layer 1 alone: the same use cases on the owner pool, which RLS does not
//     apply to, so only the Checker stands between the actor and the foreign
//     branch;
//   - layer 2 alone: the repositories called directly as tb_app, with no
//     Checker and no use case, so only RLS does.
//
// In every row the foreign branch answers like a missing one and the data stays
// as it was.
func TestTwoLayerIsolation(t *testing.T) {
	ctx, db, w, _ := setup(t)
	foreign := concat(w.NodesB, []uuid.UUID{w.Root})
	// A cut of the operations of T-ISO-01, 03, 04 and 10: one of each kind.
	pick := func(ops []op, names ...string) (out []op) {
		for _, o := range ops {
			for _, n := range names {
				if o.name == n {
					out = append(out, o)
				}
			}
		}
		return out
	}
	ops := concat(
		pick(readOps(), "get", "children", "members inherited"),
		writeOps(w.A),
		memberOps(w.AdminB, w.Emails[w.AdminB]),
	)

	t.Run("both layers", func(t *testing.T) {
		a := newApp(db.App)
		before := isolation.Snapshot(ctx, t, db)
		requireIndistinguishable(ctx, t, a, w.AdminA, ops, foreign)
		assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
	})

	t.Run("layer 1 alone: RLS does not apply", func(t *testing.T) {
		a := newApp(db.Owner)
		// Control: on this pool the repository does hand over a foreign node,
		// so a green result below is the Checker's doing.
		require.NoError(t, a.runner.InTx(ctx, actorOf(w.AdminA), port.TxOptions{ReadOnly: true}, func(ctx context.Context, tx port.Tx) error {
			_, err := a.nodes.Get(ctx, tx, nid(w.B1))
			return err
		}), "RLS must be out of the way on the owner pool")
		before := isolation.Snapshot(ctx, t, db)
		requireIndistinguishable(ctx, t, a, w.AdminA, ops, foreign)
		assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
	})

	t.Run("layer 2 alone: repositories without checks", func(t *testing.T) {
		a := newApp(db.App)
		notFound := func(err error) bool {
			c, ok := apperr.CodeOf(err)
			return ok && c == apperr.NotFound
		}
		in := func(fn func(ctx context.Context, tx port.Tx) error) error {
			return a.runner.InTx(ctx, actorOf(w.AdminA), port.TxOptions{}, fn)
		}
		// Foreign rows fetched as the root owner, to build real write requests.
		var b1, b1u1 node.Node
		require.NoError(t, a.runner.InTx(ctx, actorOf(w.OwnerRoot), port.TxOptions{ReadOnly: true}, func(ctx context.Context, tx port.Tx) (err error) {
			if b1, err = a.nodes.Get(ctx, tx, nid(w.B1)); err != nil {
				return err
			}
			b1u1, err = a.nodes.Get(ctx, tx, nid(w.B1u1))
			return err
		}))
		name, err := node.ParseName("hijacked")
		require.NoError(t, err)
		renamed, _ := b1u1.Rename(name, time.Now())
		intruder, err := node.NewChild(b1, node.ID(uuid.Must(uuid.NewV7())), node.TypeUnit, name, time.Now())
		require.NoError(t, err)
		ownUnit := node.Node{}
		require.NoError(t, a.runner.InTx(ctx, actorOf(w.AdminA), port.TxOptions{ReadOnly: true}, func(ctx context.Context, tx port.Tx) (err error) {
			ownUnit, err = a.nodes.Get(ctx, tx, nid(w.A1u1))
			return err
		}))
		plan, err := ownUnit.PlanMove(b1)
		require.NoError(t, err)

		before := isolation.Snapshot(ctx, t, db)
		for _, id := range foreign {
			// Reads: nothing.
			require.NoError(t, in(func(ctx context.Context, tx port.Tx) error {
				_, err := a.nodes.Get(ctx, tx, nid(id))
				assert.True(t, notFound(err), "Get %s: %v", id, err)
				_, err = a.nodes.GetForUpdate(ctx, tx, nid(id))
				assert.True(t, notFound(err), "GetForUpdate %s: %v", id, err)
				_, err = a.nodes.Subtree(ctx, tx, nid(id))
				assert.True(t, notFound(err), "Subtree %s: %v", id, err)
				kids, err := a.nodes.FindChildren(ctx, tx, nid(id), port.ChildFilter{}, nil, 50)
				require.NoError(t, err)
				for _, k := range kids {
					assert.NotContains(t, w.NodesB, uuid.UUID(k.ID), "FindChildren of %s returned a foreign node", id)
				}
				_, found, err := a.check.EffectiveRole(ctx, tx, actorOf(w.AdminA), nid(id))
				require.NoError(t, err)
				assert.False(t, found)
				ms, err := a.member.List(ctx, tx, nid(id), true, nil, 50)
				require.NoError(t, err)
				for _, m := range ms {
					assert.NotContains(t, foreign, uuid.UUID(m.NodeID), "List members of %s returned a foreign assignment", id)
				}
				_, found, err = a.member.GetForUpdate(ctx, tx, nid(id), user.ID(w.OnlyB))
				require.NoError(t, err)
				assert.False(t, found)
				owners, err := a.member.LockActiveOwners(ctx, tx, nid(id))
				require.NoError(t, err)
				assert.Empty(t, owners)
				return nil
			}))
			// Writes by id: nothing to touch.
			assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error { return a.nodes.Delete(ctx, tx, nid(id)) })), "Delete %s", id)
			assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error {
				return a.member.Delete(ctx, tx, nid(id), user.ID(w.OnlyB))
			})), "assignment delete on %s", id)
			assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error {
				return a.member.Update(ctx, tx, role.Assignment{UserID: user.ID(w.OnlyB), NodeID: nid(id), Role: role.Owner, GrantedBy: actorOf(w.AdminA), GrantedAt: time.Now()})
			})), "assignment update on %s", id)
		}
		assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error { return a.nodes.Update(ctx, tx, renamed) })), "Update")
		assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error { return a.nodes.Insert(ctx, tx, intruder) })), "Insert under a foreign parent")
		assert.True(t, notFound(in(func(ctx context.Context, tx port.Tx) error { return a.nodes.Move(ctx, tx, plan, 1, time.Now()) })), "Move into a foreign branch")
		// A new assignment on a foreign node: refused by RLS, or at least not stored.
		inserted := true
		err = in(func(ctx context.Context, tx port.Tx) (err error) {
			inserted, err = a.member.Insert(ctx, tx, role.Assignment{UserID: user.ID(w.Orphan), NodeID: nid(w.B1), Role: role.Owner, GrantedBy: actorOf(w.AdminA), GrantedAt: time.Now()})
			return err
		})
		assert.True(t, err != nil || !inserted, "an assignment on a foreign node was stored")
		if err != nil {
			assert.True(t, notFound(err), "refusal must read as not_found, got %v", err)
		}
		assert.Equal(t, before, isolation.Snapshot(ctx, t, db), "foreign data changed")
	})
}
