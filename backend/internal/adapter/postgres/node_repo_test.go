//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

var errNameTaken = apperr.New(apperr.NameTaken, "")

// Test-ID: T-I-01
func TestSinglePlatformIndex(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	t.Run("the database allows one platform node", func(t *testing.T) {
		second := node.ID(uuid.Must(uuid.NewV7()))
		_, err := w.db.Owner.Exec(ctx,
			`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
			 VALUES ($1, 'platform', NULL, $2::ltree, 'second', 'second', now(), now())`,
			uuid.UUID(second), ownerPath(node.Path{second}))
		require.Error(t, err)
		assert.Equal(t, "23505", sqlState(err))
		assert.Contains(t, err.Error(), "nodes_single_platform")
	})

	t.Run("a platform built by hand is refused through the repository", func(t *testing.T) {
		id := node.ID(uuid.Must(uuid.NewV7()))
		name, err := node.ParseName("second root")
		require.NoError(t, err)
		now := time.Now().UTC()
		n := node.Node{ID: id, Type: node.TypePlatform, Path: node.Path{id}, Name: name, Status: node.StatusActive, CreatedAt: now, UpdatedAt: now}
		err = w.try("ownerRoot", func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
		// The new root is outside every subtree of the actor: row-level security
		// rejects it before the unique index is reached.
		require.ErrorIs(t, err, apperr.NotFoundError("node"))
		assert.Contains(t, w.logs.String(), `"level":"ERROR"`)
	})

	t.Run("the domain cannot create a platform below a node", func(t *testing.T) {
		name, err := node.ParseName("x")
		require.NoError(t, err)
		_, err = node.NewChild(w.nodes["root"], node.ID(uuid.New()), node.TypePlatform, name, time.Now())
		require.ErrorIs(t, err, apperr.New(apperr.InvalidParentType, ""))
	})

	assert.Empty(t, w.violations())
}

// Test-ID: T-I-09
func TestNameKeyUnique(t *testing.T) {
	w := newWorld(t)
	insert := func(who string, n node.Node) error {
		return w.try(who, func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
	}

	require.NoError(t, insert("adminA", w.newChild("A1", node.TypeUnit, "Team")))
	t.Run("same parent, different case", func(t *testing.T) {
		assert.ErrorIs(t, insert("adminA", w.newChild("A1", node.TypeUnit, "team")), errNameTaken)
	})
	t.Run("same parent, canonically equivalent Unicode", func(t *testing.T) {
		require.NoError(t, insert("adminA", w.newChild("A1", node.TypeUnit, "Café")))
		assert.ErrorIs(t, insert("adminA", w.newChild("A1", node.TypeUnit, "CAFÉ")), errNameTaken)
	})
	t.Run("another parent may reuse the name", func(t *testing.T) {
		require.NoError(t, insert("adminA", w.newChild("A", node.TypeCompany, "Second company")))
		second := w.lookupChild("A", "Second company")
		child, err := nodeUnder(second, "Team")
		require.NoError(t, err)
		assert.NoError(t, insert("adminA", child))
	})
	t.Run("rename onto a sibling", func(t *testing.T) {
		var other node.Node
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			n, err := w.repo.Get(ctx, tx, w.id("A1u2"))
			require.NoError(t, err)
			name, err := node.ParseName("TEAM")
			require.NoError(t, err)
			other, _ = n.Rename(name, time.Now())
		})
		err := w.try("adminA", func(ctx context.Context, tx port.Tx) error { return w.repo.Update(ctx, tx, other) })
		assert.ErrorIs(t, err, errNameTaken)
	})
	t.Run("move onto a sibling name", func(t *testing.T) {
		// A1 holds "Team"; a second company of A gets a unit "Team" moved in.
		second := w.lookupChild("A", "Second company")
		var secondTeam node.Node
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			kids, err := w.repo.ListChildren(ctx, tx, second.ID, nil, 10)
			require.NoError(t, err)
			require.Len(t, kids, 1)
			secondTeam = kids[0]
		})
		a1 := w.lookupChild("A", "A1")
		err := w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			plan, perr := secondTeam.PlanMove(a1)
			require.NoError(t, perr)
			return w.repo.Move(ctx, tx, plan, 1, time.Now())
		})
		assert.ErrorIs(t, err, errNameTaken)
	})
	assert.Empty(t, w.violations())
}

// Test-ID: T-I-11
func TestChildrenLimit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// One batch under tb_owner: 1000 units under the company A1u... use B1.
	b1 := w.nodes["B1"]
	_, err := w.db.Owner.Exec(ctx, `
INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
SELECT g.id, 'unit', $1, ($2::ltree || tb_label(g.id)::ltree),
       'unit ' || lpad(g.n::text, 4, '0'), 'unit ' || lpad(g.n::text, 4, '0'), now(), now()
  FROM (SELECT n, gen_random_uuid() AS id FROM generate_series(1, $3::int) AS n) g`,
		uuid.UUID(b1.ID), ownerPath(b1.Path), node.MaxChildren)
	require.NoError(t, err)

	w.do("adminB", func(ctx context.Context, tx port.Tx) {
		n, err := w.repo.CountChildren(ctx, tx, b1.ID)
		require.NoError(t, err)
		// The use case refuses the 1001st child when the count reaches the limit.
		assert.GreaterOrEqual(t, n, node.MaxChildren)

		// Keyset pagination walks all of them once, in (name key, id) order.
		var seen []string
		var cursor *port.ChildCursor
		pages := 0
		for {
			page, err := w.repo.ListChildren(ctx, tx, b1.ID, cursor, 200)
			require.NoError(t, err)
			if len(page) == 0 {
				break
			}
			pages++
			for _, c := range page {
				seen = append(seen, c.Name.String())
			}
			last := page[len(page)-1]
			cursor = &port.ChildCursor{NameKey: last.Name.Key(), ID: last.ID}
		}
		assert.Len(t, seen, n)
		assert.GreaterOrEqual(t, pages, 5)
		assert.IsIncreasing(t, seen)
		seenSet := map[string]bool{}
		for _, s := range seen {
			assert.False(t, seenSet[s], "duplicate %s", s)
			seenSet[s] = true
		}
	})
	assert.Empty(t, w.violations())
}

// Test-ID: T-I-50
func TestRepositoriesRoundTrip(t *testing.T) {
	w := newWorld(t)

	t.Run("get maps every column", func(t *testing.T) {
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			got, err := w.repo.Get(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			want := w.nodes["A1"]
			assert.Equal(t, want, got)
			assert.Equal(t, time.UTC, got.CreatedAt.Location())
		})
	})

	t.Run("a missing node and a node of another branch give the identical error", func(t *testing.T) {
		var missing, foreign error
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			_, missing = w.repo.Get(ctx, tx, node.ID(uuid.New()))
			_, foreign = w.repo.Get(ctx, tx, w.id("B1"))
		})
		assert.Equal(t, apperr.NotFoundError("node"), missing)
		assert.Equal(t, missing, foreign)
	})

	t.Run("insert, read back, update, delete", func(t *testing.T) {
		n := w.newChild("A1", node.TypeUnit, "Round trip")
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			require.NoError(t, w.repo.Insert(ctx, tx, n))
			got, err := w.repo.Get(ctx, tx, n.ID)
			require.NoError(t, err)
			assert.Equal(t, n, got)

			renamed, name := n, "Round trip 2"
			nm, err := node.ParseName(name)
			require.NoError(t, err)
			renamed, changed := renamed.Rename(nm, time.Now().Truncate(time.Microsecond))
			require.True(t, changed)
			renamed, changed = renamed.Deactivate(renamed.UpdatedAt)
			require.True(t, changed)
			require.NoError(t, w.repo.Update(ctx, tx, renamed))
			got, err = w.repo.Get(ctx, tx, n.ID)
			require.NoError(t, err)
			assert.Equal(t, renamed, got)
			assert.False(t, got.EffectiveActive())

			require.NoError(t, w.repo.Delete(ctx, tx, n.ID))
			_, err = w.repo.Get(ctx, tx, n.ID)
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
			assert.ErrorIs(t, w.repo.Delete(ctx, tx, n.ID), apperr.New(apperr.NotFound, ""))
			ghost := n
			assert.ErrorIs(t, w.repo.Update(ctx, tx, ghost), apperr.New(apperr.NotFound, ""))
		})
	})

	t.Run("delete of a node with children is refused", func(t *testing.T) {
		err := w.try("adminA", func(ctx context.Context, tx port.Tx) error { return w.repo.Delete(ctx, tx, w.id("A1")) })
		assert.ErrorIs(t, err, apperr.New(apperr.NodeHasChildren, ""))
	})

	t.Run("children and counts", func(t *testing.T) {
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			n, err := w.repo.CountChildren(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			assert.Equal(t, 2, n)
			kids, err := w.repo.ListChildren(ctx, tx, w.id("A1"), nil, 1)
			require.NoError(t, err)
			require.Len(t, kids, 1)
			assert.Equal(t, w.nodes["A1u1"], kids[0])
			rest, err := w.repo.ListChildren(ctx, tx, w.id("A1"), &port.ChildCursor{NameKey: kids[0].Name.Key(), ID: kids[0].ID}, 5)
			require.NoError(t, err)
			require.Len(t, rest, 1)
			assert.Equal(t, w.nodes["A1u2"], rest[0])
			none, err := w.repo.ListChildren(ctx, tx, w.id("B1"), nil, 5)
			require.NoError(t, err)
			assert.Empty(t, none, "the children of an invisible node are not listed")
			_, err = w.repo.ListChildren(ctx, tx, w.id("A1"), nil, 0)
			assert.Error(t, err)
		})
	})

	t.Run("subtree and membership", func(t *testing.T) {
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			sub, err := w.repo.Subtree(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			require.Len(t, sub, 3)
			assert.Equal(t, w.nodes["A1"], sub[0], "the root of the subtree comes first")
			assert.ElementsMatch(t, []node.ID{w.id("A1"), w.id("A1u1"), w.id("A1u2")},
				[]node.ID{sub[0].ID, sub[1].ID, sub[2].ID})
			_, err = w.repo.Subtree(ctx, tx, w.id("B1"))
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))

			in, err := w.repo.IsInSubtree(ctx, tx, w.id("A1u1"), w.id("A"))
			require.NoError(t, err)
			assert.True(t, in)
			in, err = w.repo.IsInSubtree(ctx, tx, w.id("A"), w.id("A1u1"))
			require.NoError(t, err)
			assert.False(t, in)
			in, err = w.repo.IsInSubtree(ctx, tx, w.id("A"), w.id("A"))
			require.NoError(t, err)
			assert.True(t, in)
			_, err = w.repo.IsInSubtree(ctx, tx, w.id("A1"), w.id("B"))
			assert.ErrorIs(t, err, apperr.New(apperr.NotFound, ""))
		})
	})

	t.Run("activity of ancestors", func(t *testing.T) {
		ctx := context.Background()
		set := func(key, status string) {
			_, err := w.db.Owner.Exec(ctx, `UPDATE nodes SET status = $2 WHERE id = $1`, uuid.UUID(w.id(key)), status)
			require.NoError(t, err)
		}
		set("root", "inactive")
		defer set("root", "active")
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			// The access root of adminA is A; the inactive root above is hidden
			// but still counts.
			a, err := w.repo.Get(ctx, tx, w.id("A"))
			require.NoError(t, err)
			assert.False(t, a.EffectiveActive())
			u, err := w.repo.Get(ctx, tx, w.id("A1u1"))
			require.NoError(t, err)
			assert.False(t, u.AncestorsActive)
			sub, err := w.repo.Subtree(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			for _, n := range sub {
				assert.False(t, n.EffectiveActive(), n.Name.String())
			}
			kids, err := w.repo.ListChildren(ctx, tx, w.id("A1"), nil, 10)
			require.NoError(t, err)
			for _, n := range kids {
				assert.False(t, n.EffectiveActive(), n.Name.String())
			}
		})
		set("root", "active")
		set("A1", "inactive")
		defer set("A1", "active")
		w.do("adminA", func(ctx context.Context, tx port.Tx) {
			a, err := w.repo.Get(ctx, tx, w.id("A"))
			require.NoError(t, err)
			assert.True(t, a.EffectiveActive())
			sub, err := w.repo.Subtree(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			for _, n := range sub {
				assert.False(t, n.EffectiveActive(), n.Name.String())
			}
		})
		// An inactive access root: its ancestors cannot be learned, the answer
		// is the safe "not active".
		w.do("multi", func(ctx context.Context, tx port.Tx) {
			a1, err := w.repo.Get(ctx, tx, w.id("A1"))
			require.NoError(t, err)
			assert.False(t, a1.EffectiveActive())
			assert.False(t, a1.AncestorsActive)
		})
	})

	t.Run("move rewrites the paths of the whole subtree", func(t *testing.T) {
		var a1 node.Node
		w.do("ownerRoot", func(ctx context.Context, tx port.Tx) {
			var err error
			a1, err = w.repo.Get(ctx, tx, w.id("A1"))
			require.NoError(t, err)
		})
		b := w.nodes["B"]
		plan, err := a1.PlanMove(b)
		require.NoError(t, err)

		// A wrong expected size aborts and, after rollback, changes nothing.
		err = w.try("ownerRoot", func(ctx context.Context, tx port.Tx) error {
			return w.repo.Move(ctx, tx, plan, 2, time.Now())
		})
		require.Error(t, err)
		w.do("ownerRoot", func(ctx context.Context, tx port.Tx) {
			got, err := w.repo.Get(ctx, tx, a1.ID)
			require.NoError(t, err)
			assert.Equal(t, a1.Path, got.Path)
		})

		err = w.try("ownerRoot", func(ctx context.Context, tx port.Tx) error {
			if err := w.lock.Lock(ctx, tx); err != nil {
				return err
			}
			sub, err := w.repo.Subtree(ctx, tx, a1.ID)
			if err != nil {
				return err
			}
			return w.repo.Move(ctx, tx, plan, len(sub), time.Now())
		})
		require.NoError(t, err)
		w.do("ownerRoot", func(ctx context.Context, tx port.Tx) {
			sub, err := w.repo.Subtree(ctx, tx, a1.ID)
			require.NoError(t, err)
			require.Len(t, sub, 3)
			for _, n := range sub {
				assert.Equal(t, b.Path, n.Path[:len(b.Path)], "path of %s starts with the path of B", n.Name)
			}
			moved, err := w.repo.Get(ctx, tx, a1.ID)
			require.NoError(t, err)
			require.NotNil(t, moved.ParentID)
			assert.Equal(t, b.ID, *moved.ParentID)
			in, err := w.repo.IsInSubtree(ctx, tx, w.id("A1u1"), w.id("B"))
			require.NoError(t, err)
			assert.True(t, in)
		})
		assert.Empty(t, w.violations())
	})
}

// Test-ID: T-I-50
func TestTreeLockSerialises(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	firstHolds := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- w.try("ownerRoot", func(ctx context.Context, tx port.Tx) error {
			if err := w.lock.Lock(ctx, tx); err != nil {
				return err
			}
			close(firstHolds)
			<-releaseFirst
			return nil
		})
	}()
	<-firstHolds

	acquired := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- w.try("adminA", func(ctx context.Context, tx port.Tx) error {
			if err := w.lock.Lock(ctx, tx); err != nil {
				return err
			}
			close(acquired)
			return nil
		})
	}()

	// The second transaction shows up as waiting for an advisory lock.
	require.Eventually(t, func() bool {
		var n int
		err := w.db.Owner.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n)
		return err == nil && n == 1
	}, 5*time.Second, 10*time.Millisecond)
	select {
	case <-acquired:
		t.Fatal("the second transaction took the tree lock while the first held it")
	default:
	}

	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	<-acquired
}

// lookupChild finds a child by name through the repository as the root owner.
func (w *world) lookupChild(parent, name string) node.Node {
	w.t.Helper()
	var found node.Node
	w.do("ownerRoot", func(ctx context.Context, tx port.Tx) {
		kids, err := w.repo.ListChildren(ctx, tx, w.id(parent), nil, 100)
		require.NoError(w.t, err)
		for _, k := range kids {
			if k.Name.String() == name {
				found = k
			}
		}
	})
	require.False(w.t, found.ID.IsZero(), fmt.Sprintf("child %q of %s", name, parent))
	return found
}

// nodeUnder builds a unit under the given company.
func nodeUnder(parent node.Node, name string) (node.Node, error) {
	nm, err := node.ParseName(name)
	if err != nil {
		return node.Node{}, err
	}
	return node.NewChild(parent, node.ID(uuid.Must(uuid.NewV7())), node.TypeUnit, nm, time.Now().UTC().Truncate(time.Microsecond))
}

// Test-ID: T-I-07
func TestDeleteCascades(t *testing.T) {
	w := newWorld(t)
	c := w.useCases()
	ctx := context.Background()
	del := func(who, key string) error {
		_, err := c.del.Execute(ctx, uc.DeleteNodeInput{Actor: w.users[who], RequestID: "t", NodeID: w.id(key)})
		return err
	}

	// A node with children, the root and a node the actor may not delete stay.
	requireApp(t, del("ownerRoot", "A1"), apperr.NodeHasChildren)
	requireApp(t, del("ownerRoot", "root"), apperr.RootImmutable)
	requireApp(t, del("adminA", "A1u1"), apperr.Forbidden) // admin: no node.delete on the parent
	requireApp(t, del("adminB", "A1u1"), apperr.NotFound)
	assert.Empty(t, c.audit.actions())

	// An empty unit with a role assignment (viewerA1u1) goes together with the assignment.
	var assignments int
	require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT count(*) FROM role_assignments WHERE node_id = $1`,
		uuid.UUID(w.id("A1u1"))).Scan(&assignments))
	require.Equal(t, 1, assignments)

	require.NoError(t, del("ownerRoot", "A1u1"))
	require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT count(*) FROM role_assignments WHERE node_id = $1`,
		uuid.UUID(w.id("A1u1"))).Scan(&assignments))
	assert.Zero(t, assignments, "assignments are removed by cascade")
	var left int
	require.NoError(t, w.db.Owner.QueryRow(ctx, `SELECT count(*) FROM nodes WHERE id = $1`, uuid.UUID(w.id("A1u1"))).Scan(&left))
	assert.Zero(t, left)
	require.Equal(t, []string{"node.deleted"}, c.audit.actions())
	assert.Equal(t, w.id("A1"), c.audit.entries[0].NodeID, "the record is on the parent")
	assert.Empty(t, w.violations())

	// Deleting by id as someone with no access to the parent any more is not_found.
	requireApp(t, del("ownerRoot", "A1u1"), apperr.NotFound)
}
