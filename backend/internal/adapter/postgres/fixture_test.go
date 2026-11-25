//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// syncBuffer collects log output from several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// world is a two-branch tree written straight to the tables as tb_owner:
//
//	root (platform) > A (partner) > A1 (company) > A1u1, A1u2 (unit)
//	                > B (partner) > B1 (company) > B1u1, B1u2 (unit)
//
// plus the real adapters on the tb_app pool.
type world struct {
	t       *testing.T
	db      *testkit.DB
	logs    *syncBuffer
	runner  *postgres.TxRunner
	repo    *postgres.NodeRepo
	checker *postgres.Checker
	lock    postgres.TreeLock

	nodes map[string]node.Node
	users map[string]user.ActorID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db := testkit.NewDB(t)
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w := &world{
		t: t, db: db, logs: logs,
		runner:  postgres.NewTxRunner(db.App, log),
		repo:    postgres.NewNodeRepo(log),
		checker: postgres.NewChecker(log),
		lock:    postgres.NewTreeLock(),
		nodes:   map[string]node.Node{},
		users:   map[string]user.ActorID{},
	}
	ctx := context.Background()
	w.addNode(ctx, "root", node.TypePlatform, "")
	w.addNode(ctx, "A", node.TypePartner, "root")
	w.addNode(ctx, "A1", node.TypeCompany, "A")
	w.addNode(ctx, "A1u1", node.TypeUnit, "A1")
	w.addNode(ctx, "A1u2", node.TypeUnit, "A1")
	w.addNode(ctx, "B", node.TypePartner, "root")
	w.addNode(ctx, "B1", node.TypeCompany, "B")
	w.addNode(ctx, "B1u1", node.TypeUnit, "B1")
	w.addNode(ctx, "B1u2", node.TypeUnit, "B1")

	for _, name := range []string{"ownerRoot", "adminA", "adminB", "viewerA1u1", "multi", "orphan"} {
		w.addUser(ctx, name)
	}
	w.grant(ctx, "ownerRoot", "root", "owner")
	w.grant(ctx, "adminA", "A", "admin")
	w.grant(ctx, "adminB", "B", "admin")
	w.grant(ctx, "viewerA1u1", "A1u1", "viewer")
	w.grant(ctx, "multi", "A1", "admin")
	w.grant(ctx, "multi", "B1", "viewer")
	return w
}

func (w *world) addNode(ctx context.Context, key string, typ node.Type, parent string) {
	w.t.Helper()
	id := node.ID(uuid.Must(uuid.NewV7()))
	var pid *node.ID
	path := node.Path{id}
	if parent != "" {
		p := w.nodes[parent]
		pid = &p.ID
		path = p.Path.Append(id)
	}
	name, err := node.ParseName(key)
	require.NoError(w.t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	n := node.Node{
		ID: id, Type: typ, ParentID: pid, Path: path, Name: name, Status: node.StatusActive,
		AncestorsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	var parentArg any
	if pid != nil {
		parentArg = uuid.UUID(*pid)
	}
	_, err = w.db.Owner.Exec(ctx,
		`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
		 VALUES ($1, $2, $3, $4::ltree, $5, $6, $7, $7)`,
		uuid.UUID(id), string(typ), parentArg, ownerPath(path), name.String(), name.Key(), now)
	require.NoError(w.t, err)
	w.nodes[key] = n
}

func ownerPath(p node.Path) string {
	var b bytes.Buffer
	for i, id := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		for _, c := range uuid.UUID(id).String() {
			if c != '-' {
				b.WriteRune(c)
			}
		}
	}
	return b.String()
}

func (w *world) addUser(ctx context.Context, name string) {
	w.t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'hash', now())`,
		id, strings.ToLower(name)+"@example.test")
	require.NoError(w.t, err)
	w.users[name] = user.ActorID(id)
}

func (w *world) grant(ctx context.Context, who, where, role string) {
	w.t.Helper()
	_, err := w.db.Owner.Exec(ctx,
		`INSERT INTO role_assignments (user_id, node_id, role, granted_at) VALUES ($1, $2, $3, now())`,
		uuid.UUID(w.users[who]), uuid.UUID(w.nodes[where].ID), role)
	require.NoError(w.t, err)
}

func (w *world) id(key string) node.ID { return w.nodes[key].ID }

// do runs fn as the named user in a read-write READ COMMITTED transaction and
// requires it to succeed.
func (w *world) do(who string, fn func(ctx context.Context, tx port.Tx)) {
	w.t.Helper()
	require.NoError(w.t, w.try(who, func(ctx context.Context, tx port.Tx) error {
		fn(ctx, tx)
		return nil
	}))
}

// try is do for functions that may fail; the error is returned.
func (w *world) try(who string, fn func(ctx context.Context, tx port.Tx) error) error {
	w.t.Helper()
	return w.runner.InTx(context.Background(), w.users[who], port.TxOptions{}, fn)
}

// violations returns the rows of tb_tree_violations() (read as the owner).
func (w *world) violations() []string {
	w.t.Helper()
	rows, err := w.db.Owner.Query(context.Background(), `SELECT node_id::text || ' ' || problem FROM tb_tree_violations()`)
	require.NoError(w.t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(w.t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(w.t, rows.Err())
	return out
}

func (w *world) newChild(parent string, typ node.Type, name string) node.Node {
	w.t.Helper()
	nm, err := node.ParseName(name)
	require.NoError(w.t, err)
	var p node.Node
	w.do("ownerRoot", func(ctx context.Context, tx port.Tx) {
		var gerr error
		p, gerr = w.repo.Get(ctx, tx, w.id(parent))
		require.NoError(w.t, gerr)
	})
	n, err := node.NewChild(p, node.ID(uuid.Must(uuid.NewV7())), typ, nm, time.Now().UTC().Truncate(time.Microsecond))
	require.NoError(w.t, err)
	return n
}
