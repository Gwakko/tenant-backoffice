//go:build integration

package isolation_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// stack is the real adapters of the application on a tb_app pool.
type stack struct {
	runner *postgres.TxRunner
	repo   *postgres.NodeRepo
	check  *postgres.Checker
	logs   *logSink
}

func newStack(pool *pgxpool.Pool) *stack {
	logs := &logSink{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &stack{runner: postgres.NewTxRunner(pool, log), repo: postgres.NewNodeRepo(log), check: postgres.NewChecker(log), logs: logs}
}

func (s *stack) in(ctx context.Context, actor uuid.UUID, fn func(ctx context.Context, tx port.Tx) error) error {
	return s.runner.InTx(ctx, user.ActorID(actor), port.TxOptions{}, fn)
}

func nid(id uuid.UUID) node.ID { return node.ID(id) }

// visibleTo reports which of the ids the actor can read through the repository.
func (s *stack) visibleTo(ctx context.Context, t *testing.T, actor uuid.UUID, all []uuid.UUID) []uuid.UUID {
	t.Helper()
	var out []uuid.UUID
	require.NoError(t, s.in(ctx, actor, func(ctx context.Context, tx port.Tx) error {
		for _, id := range all {
			_, err := s.repo.Get(ctx, tx, nid(id))
			if err == nil {
				out = append(out, id)
			} else if !errors.Is(err, apperr.New(apperr.NotFound, "")) {
				return err
			}
		}
		return nil
	}))
	return out
}

// Test-ID: T-ISO-11
func TestRLSThroughRepositories(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	notFound := apperr.NotFoundError("node")
	var s *stack

	get := func(id uuid.UUID) node.Node { // as the root owner, who sees everything
		var n node.Node
		require.NoError(t, s.in(ctx, w.OwnerRoot, func(ctx context.Context, tx port.Tx) error {
			var err error
			n, err = s.repo.Get(ctx, tx, nid(id))
			return err
		}))
		return n
	}

	cases := []struct {
		name         string
		actor        uuid.UUID
		own, foreign []uuid.UUID
	}{
		{"admin of A", w.AdminA, w.NodesA, w.NodesB},
		{"admin of B", w.AdminB, w.NodesB, w.NodesA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s = newStack(db.App)
			before := snapshot(ctx, t, db)
			foreignCompany, foreignUnit := get(c.foreign[1]), get(c.foreign[2])
			ownUnit := get(c.own[2])
			assert.ElementsMatch(t, c.own, s.visibleTo(ctx, t, c.actor, append(append([]uuid.UUID{}, c.own...), append(c.foreign, w.Root)...)))

			err := s.in(ctx, c.actor, func(ctx context.Context, tx port.Tx) error {
				for _, f := range append(append([]uuid.UUID{}, c.foreign...), w.Root) {
					id := nid(f)
					_, err := s.repo.Get(ctx, tx, id)
					assert.Equal(t, notFound, err)
					_, err = s.repo.GetForUpdate(ctx, tx, id)
					assert.Equal(t, notFound, err)
					_, err = s.repo.Subtree(ctx, tx, id)
					assert.Equal(t, notFound, err)
					kids, err := s.repo.ListChildren(ctx, tx, id, nil, 10)
					assert.NoError(t, err)
					assert.Empty(t, kids)
					if f != w.Root { // the root has a child the actor does see (its own branch)
						cnt, err := s.repo.CountChildren(ctx, tx, id)
						assert.NoError(t, err)
						assert.Zero(t, cnt)
					}
					_, err = s.repo.IsInSubtree(ctx, tx, nid(c.own[0]), id)
					assert.Equal(t, notFound, err)
					_, ok, err := s.check.EffectiveRole(ctx, tx, user.ActorID(c.actor), id)
					assert.NoError(t, err)
					assert.False(t, ok)
				}
				// Writes to foreign rows touch nothing.
				renamed, _ := foreignUnit.Rename(mustName(t, "hijacked"), time.Now())
				assert.Equal(t, notFound, s.repo.Update(ctx, tx, renamed))
				assert.Equal(t, notFound, s.repo.Delete(ctx, tx, foreignUnit.ID))
				return nil
			})
			require.NoError(t, err)
			assert.Empty(t, s.logs.String(), "silent not-found paths must not log errors")

			// An insert under a foreign parent, and a move into a foreign branch:
			// the database rejects both, the adapter answers not_found and logs.
			child, err := node.NewChild(foreignCompany, node.ID(uuid.Must(uuid.NewV7())), node.TypeUnit, mustName(t, "intruder"), time.Now())
			require.NoError(t, err)
			err = s.in(ctx, c.actor, func(ctx context.Context, tx port.Tx) error { return s.repo.Insert(ctx, tx, child) })
			assert.Equal(t, notFound, err)

			plan, err := ownUnit.PlanMove(foreignCompany)
			require.NoError(t, err)
			err = s.in(ctx, c.actor, func(ctx context.Context, tx port.Tx) error {
				return s.repo.Move(ctx, tx, plan, 1, time.Now())
			})
			assert.Equal(t, notFound, err)

			logs := s.logs.String()
			assert.Contains(t, logs, `"level":"ERROR"`)
			assert.Regexp(t, `"sqlstate":"(42501|23514)"`, logs)

			// A new root path lies in no subtree of the actor: here the trigger
			// has nothing to check, and the policy itself answers with 42501.
			rootID := nid(uuid.Must(uuid.NewV7()))
			now := time.Now().UTC()
			stray := node.Node{
				ID: rootID, Type: node.TypePlatform, Path: node.Path{rootID}, Name: mustName(t, "stray root"),
				Status: node.StatusActive, CreatedAt: now, UpdatedAt: now,
			}
			err = s.in(ctx, c.actor, func(ctx context.Context, tx port.Tx) error { return s.repo.Insert(ctx, tx, stray) })
			assert.Equal(t, notFound, err)
			assert.Contains(t, s.logs.String(), `"sqlstate":"42501"`)
			assert.NotContains(t, err.Error(), "policy", "the real reason must not reach the client")

			assert.Equal(t, before, snapshot(ctx, t, db), "foreign data changed")
		})
	}
}

func mustName(t *testing.T, s string) node.Name {
	t.Helper()
	n, err := node.ParseName(s)
	require.NoError(t, err)
	return n
}

// Test-ID: T-ISO-13
func TestPoolDoesNotLeakActor(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	pool := db.NewAppPool(t, 1)
	s := newStack(pool)

	pid := func() (n int) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&n))
		return n
	}
	actorSetting := func() (v string) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT current_setting('app.actor_id', true)`).Scan(&v))
		return v
	}
	rowsWithoutActor := func() (n int) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n))
		return n
	}
	all := append(append([]uuid.UUID{w.Root}, w.NodesA...), w.NodesB...)
	only := func(actor uuid.UUID, own []uuid.UUID) {
		t.Helper()
		assert.ElementsMatch(t, own, s.visibleTo(ctx, t, actor, all))
	}

	only(w.AdminA, w.NodesA)
	connection := pid()

	// Between transactions, on the same single connection: no actor, no rows.
	assert.EqualValues(t, 1, pool.Stat().TotalConns())
	assert.Empty(t, actorSetting())
	assert.Zero(t, rowsWithoutActor())

	only(w.AdminB, w.NodesB)
	assert.Equal(t, connection, pid(), "the single connection must be reused")
	assert.Empty(t, actorSetting())

	// A transaction that fails after the actor was set leaks nothing either.
	boom := errors.New("boom")
	require.ErrorIs(t, s.in(ctx, w.AdminA, func(ctx context.Context, tx port.Tx) error {
		if _, err := s.repo.Get(ctx, tx, nid(w.A)); err != nil {
			return err
		}
		return boom
	}), boom)
	assert.Empty(t, actorSetting())
	assert.Zero(t, rowsWithoutActor())

	// Alternating actors from two goroutines share the one connection and
	// still never see each other's rows.
	var wg sync.WaitGroup
	for _, c := range []struct {
		actor uuid.UUID
		own   []uuid.UUID
	}{{w.AdminA, w.NodesA}, {w.AdminB, w.NodesB}} {
		wg.Go(func() {
			for range 25 {
				got := s.visibleTo(ctx, t, c.actor, all)
				if !assert.ElementsMatch(t, c.own, got) {
					return
				}
			}
		})
	}
	wg.Wait()
	assert.Empty(t, actorSetting())
	assert.EqualValues(t, 1, pool.Stat().TotalConns())
}

// Test-ID: T-ISO-14
func TestErrorAndPanicResetActor(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)
	w := newWorld(ctx, t, db)
	pool := db.NewAppPool(t, 1)
	s := newStack(pool)
	all := append(append([]uuid.UUID{w.Root}, w.NodesA...), w.NodesB...)

	rename := func(ctx context.Context, tx port.Tx) error {
		n, err := s.repo.Get(ctx, tx, nid(w.A1u1))
		if err != nil {
			return err
		}
		renamed, _ := n.Rename(mustName(t, "renamed"), time.Now())
		return s.repo.Update(ctx, tx, renamed)
	}
	// afterwards checks that nothing was written, no actor stayed on the
	// connection and the pool still serves another actor correctly.
	afterwards := func(before string) {
		t.Helper()
		assert.Equal(t, before, snapshot(ctx, t, db), "the write must be rolled back")
		var v string
		require.NoError(t, pool.QueryRow(ctx, `SELECT current_setting('app.actor_id', true)`).Scan(&v))
		assert.Empty(t, v)
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n))
		assert.Zero(t, n)
		assert.ElementsMatch(t, w.NodesB, s.visibleTo(ctx, t, w.AdminB, all))
		assert.EqualValues(t, 1, pool.Stat().TotalConns())
	}

	t.Run("error after a write", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		boom := errors.New("boom")
		err := s.in(ctx, w.AdminA, func(ctx context.Context, tx port.Tx) error {
			if err := rename(ctx, tx); err != nil {
				return err
			}
			return boom
		})
		require.ErrorIs(t, err, boom)
		afterwards(before)
	})

	t.Run("panic after a write", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		assert.PanicsWithValue(t, "kaboom", func() {
			_ = s.in(ctx, w.AdminA, func(ctx context.Context, tx port.Tx) error {
				require.NoError(t, rename(ctx, tx))
				panic("kaboom")
			})
		})
		afterwards(before)
	})

	t.Run("cancelled request after a write", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		cctx, cancel := context.WithCancel(ctx)
		err := s.in(cctx, w.AdminA, func(ctx context.Context, tx port.Tx) error {
			if err := rename(ctx, tx); err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		})
		require.Error(t, err)
		// The rollback ran although the request context is gone; the next
		// transaction on the single connection must not hang.
		tctx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		assert.ElementsMatch(t, w.NodesB, s.visibleTo(tctx, t, w.AdminB, all))
		afterwards(before)
	})

	t.Run("a statement error inside the transaction", func(t *testing.T) {
		before := snapshot(ctx, t, db)
		err := s.in(ctx, w.AdminA, func(ctx context.Context, tx port.Tx) error {
			require.NoError(t, rename(ctx, tx))
			// Deleting a node that has children violates a foreign key.
			return s.repo.Delete(ctx, tx, nid(w.A1))
		})
		require.ErrorIs(t, err, apperr.New(apperr.NodeHasChildren, ""))
		afterwards(before)
	})
}
