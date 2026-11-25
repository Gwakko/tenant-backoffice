//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// recAudit records audit entries; it is safe for concurrent use.
type recAudit struct {
	mu      sync.Mutex
	entries []port.AuditEntry
}

func (a *recAudit) Write(_ context.Context, _ port.Tx, e port.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

// actions returns the recorded actions in write order.
func (a *recAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.entries {
		out = append(out, e.Action)
	}
	return out
}

func (a *recAudit) entriesOf(action string) []port.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []port.AuditEntry
	for _, e := range a.entries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func (a *recAudit) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = nil
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

type v7IDs struct{}

func (v7IDs) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// useCases is the node use cases wired to the real adapters of the world.
type useCases struct {
	audit  *recAudit
	create *uc.CreateNode
	rename *uc.RenameNode
	move   *uc.MoveNode
	del    *uc.DeleteNode
	get    *uc.GetNode
	list   *uc.ListChildren
	deact  *uc.DeactivateNode
	act    *uc.ActivateNode
	roots  *uc.ListAccessRoots
	target *uc.ListMoveTargets
}

func (w *world) useCases() *useCases {
	a := &recAudit{}
	d := uc.Deps{Runner: w.runner, Checker: w.checker, Nodes: w.repo, Lock: w.lock, Audit: a, Clock: realClock{}, IDs: v7IDs{}}
	return &useCases{
		audit: a, create: uc.NewCreateNode(d), rename: uc.NewRenameNode(d), move: uc.NewMoveNode(d),
		del: uc.NewDeleteNode(d), get: uc.NewGetNode(d), list: uc.NewListChildren(d),
		deact: uc.NewDeactivateNode(d), act: uc.NewActivateNode(d), roots: uc.NewListAccessRoots(d),
		target: uc.NewListMoveTargets(d),
	}
}

// moveAs runs MoveNode for the named user and nodes.
func (w *world) moveAs(c *useCases, who, nodeKey, parentKey string) error {
	_, err := c.move.Execute(context.Background(), uc.MoveNodeInput{
		Actor: w.users[who], RequestID: "t", NodeID: w.id(nodeKey), NewParentID: w.id(parentKey),
	})
	return err
}

// snapshot reads the structural columns of every node as the owner.
func (w *world) snapshot() map[string]string {
	w.t.Helper()
	rows, err := w.db.Owner.Query(context.Background(),
		`SELECT id::text, coalesce(parent_id::text,'') || '|' || path::text || '|' || name || '|' || status FROM nodes`)
	require.NoError(w.t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, v string
		require.NoError(w.t, rows.Scan(&id, &v))
		out[id] = v
	}
	require.NoError(w.t, rows.Err())
	return out
}

// pathOf reads the path of a node as the owner.
func (w *world) pathOf(id node.ID) (string, *string) {
	w.t.Helper()
	var path string
	var parent *string
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
		`SELECT path::text, parent_id::text FROM nodes WHERE id = $1`, uuid.UUID(id)).Scan(&path, &parent))
	return path, parent
}
