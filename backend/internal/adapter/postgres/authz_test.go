//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// roleOn asks the checker for the actor's effective role on a node.
func (w *world) roleOn(who, where string) (role.Role, bool) {
	w.t.Helper()
	var r role.Role
	var ok bool
	w.do(who, func(ctx context.Context, tx port.Tx) {
		var err error
		r, ok, err = w.checker.EffectiveRole(ctx, tx, w.users[who], w.id(where))
		require.NoError(w.t, err)
	})
	return r, ok
}

// Test-ID: T-I-13
func TestMultiBranchUser(t *testing.T) {
	w := newWorld(t)

	// admin on A1, viewer on B1: the role of each branch applies to its subtree only.
	want := map[string]struct {
		role  role.Role
		found bool
	}{
		"A1":   {role.Admin, true},
		"A1u1": {role.Admin, true},
		"A1u2": {role.Admin, true},
		"B1":   {role.Viewer, true},
		"B1u1": {role.Viewer, true},
		"B1u2": {role.Viewer, true},
		// Nothing upward and nothing sideways.
		"A": {"", false}, "B": {"", false}, "root": {"", false},
	}
	for key, exp := range want {
		r, ok := w.roleOn("multi", key)
		assert.Equal(t, exp.found, ok, key)
		assert.Equal(t, exp.role, r, key)
	}

	t.Run("rights follow the branch", func(t *testing.T) {
		w.do("multi", func(ctx context.Context, tx port.Tx) {
			actor := w.users["multi"]
			_, err := authz.Require(ctx, w.checker, tx, actor, w.id("A1u1"), role.NodeCreate)
			assert.NoError(t, err)
			_, err = authz.Require(ctx, w.checker, tx, actor, w.id("B1u1"), role.NodeRead)
			assert.NoError(t, err)
			_, err = authz.Require(ctx, w.checker, tx, actor, w.id("B1u1"), role.NodeCreate)
			assert.ErrorIs(t, err, apperr.New(apperr.Forbidden, ""))
			_, err = authz.Require(ctx, w.checker, tx, actor, w.id("B"), role.NodeRead)
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
			_, err = authz.Require(ctx, w.checker, tx, actor, node.ID(uuid.New()), role.NodeRead)
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
		})
	})

	t.Run("the data seen is exactly the two subtrees", func(t *testing.T) {
		w.do("multi", func(ctx context.Context, tx port.Tx) {
			var all []node.ID
			for _, key := range []string{"A1", "B1"} {
				sub, err := w.repo.Subtree(ctx, tx, w.id(key))
				require.NoError(t, err)
				for _, n := range sub {
					all = append(all, n.ID)
				}
			}
			assert.ElementsMatch(t, []node.ID{
				w.id("A1"), w.id("A1u1"), w.id("A1u2"), w.id("B1"), w.id("B1u1"), w.id("B1u2"),
			}, all)
			_, err := w.repo.Get(ctx, tx, w.id("A"))
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
		})
	})

	t.Run("an inactive user has no role anywhere", func(t *testing.T) {
		_, err := w.db.Owner.Exec(context.Background(),
			`UPDATE users SET status = 'inactive' WHERE id = $1`, uuid.UUID(w.users["multi"]))
		require.NoError(t, err)
		for _, key := range []string{"A1", "B1u1"} {
			_, ok := w.roleOn("multi", key)
			assert.False(t, ok, key)
		}
	})
}

// Test-ID: T-I-16
func TestRevokeDuringTransaction(t *testing.T) {
	revoke := func(w *world, who, where string) {
		_, err := w.db.Owner.Exec(context.Background(),
			`DELETE FROM role_assignments WHERE user_id = $1 AND node_id = $2`,
			uuid.UUID(w.users[who]), uuid.UUID(w.id(where)))
		require.NoError(t, err)
	}

	t.Run("a request finished before the revoke commits ran with the old rights", func(t *testing.T) {
		w := newWorld(t)
		var seen node.Node
		w.runner.BeforeCommit(func(context.Context, pgx.Tx) error {
			// The request function is done; the revoke commits while the request
			// transaction is still open.
			revoke(w, "viewerA1u1", "A1u1")
			return nil
		})
		w.do("viewerA1u1", func(ctx context.Context, tx port.Tx) {
			_, err := authz.Require(ctx, w.checker, tx, w.users["viewerA1u1"], w.id("A1u1"), role.NodeRead)
			require.NoError(t, err)
			seen, err = w.repo.Get(ctx, tx, w.id("A1u1"))
			require.NoError(t, err)
		})
		assert.Equal(t, w.id("A1u1"), seen.ID)

		// The next request has no rights.
		w.runner.BeforeCommit(nil)
		w.do("viewerA1u1", func(ctx context.Context, tx port.Tx) {
			_, err := authz.Require(ctx, w.checker, tx, w.users["viewerA1u1"], w.id("A1u1"), role.NodeRead)
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
			_, err = w.repo.Get(ctx, tx, w.id("A1u1"))
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
		})
	})

	t.Run("statements after the revoke commits see less, never more", func(t *testing.T) {
		w := newWorld(t)
		w.do("viewerA1u1", func(ctx context.Context, tx port.Tx) {
			_, err := w.repo.Get(ctx, tx, w.id("A1u1"))
			require.NoError(t, err)
			_, ok, err := w.checker.EffectiveRole(ctx, tx, w.users["viewerA1u1"], w.id("A1u1"))
			require.NoError(t, err)
			require.True(t, ok)

			revoke(w, "viewerA1u1", "A1u1") // commits on another connection

			// Row-level security (second line) and the role check (first line)
			// both answer from the new state: 404, no data, no role.
			_, err = w.repo.Get(ctx, tx, w.id("A1u1"))
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
			_, ok, err = w.checker.EffectiveRole(ctx, tx, w.users["viewerA1u1"], w.id("A1u1"))
			require.NoError(t, err)
			assert.False(t, ok)
			n, err := w.repo.CountChildren(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			assert.Zero(t, n)
		})
	})

	t.Run("a downgrade takes effect with the next request", func(t *testing.T) {
		w := newWorld(t)
		_, err := w.db.Owner.Exec(context.Background(),
			`UPDATE role_assignments SET role = 'viewer' WHERE user_id = $1 AND node_id = $2`,
			uuid.UUID(w.users["adminA"]), uuid.UUID(w.id("A")))
		require.NoError(t, err)
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			_, err := authz.Require(ctx, w.checker, tx, w.users["adminA"], w.id("A1"), role.NodeRename)
			assert.ErrorIs(t, err, apperr.New(apperr.Forbidden, ""))
			_, err = authz.Require(ctx, w.checker, tx, w.users["adminA"], w.id("A1"), role.NodeRead)
			assert.NoError(t, err)
		})
	})
}
