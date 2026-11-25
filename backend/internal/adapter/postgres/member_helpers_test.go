//go:build integration

package postgres_test

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// testHasher stands in for argon2id: the tests care where the hash goes, not
// how it is made.
type testHasher struct{}

func (testHasher) Hash(p string) (user.PasswordHash, error) {
	return user.PasswordHash("test-hash:" + p), nil
}

func (testHasher) Verify(h user.PasswordHash, p string) (bool, error) {
	return string(h) == "test-hash:"+p, nil
}

// barrier holds the first callers until n of them have arrived or the timeout
// passes. Behind a lock only one caller can ever arrive, so the timeout turns
// "the other one is queued" into a proceed signal; without the lock both arrive
// within milliseconds and are released together, which is the race to catch.
type barrier struct {
	mu      sync.Mutex
	n       int
	arrived int
	release chan struct{}
	timeout time.Duration
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, release: make(chan struct{}), timeout: 300 * time.Millisecond}
}

func (b *barrier) wait() {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-time.After(b.timeout):
	}
}

// gateAudit records entries like recAudit and parks the writer of one action
// at the barrier. The use cases write that entry after their checks and before
// their last write, so parked transactions have both decided on the same state.
type gateAudit struct {
	*recAudit
	action string
	gate   *barrier
}

func (a gateAudit) Write(ctx context.Context, tx port.Tx, e port.AuditEntry) error {
	if e.Action == a.action {
		a.gate.wait()
	}
	return a.recAudit.Write(ctx, tx, e)
}

// memberCases is the member use cases wired to the real adapters of the world.
type memberCases struct {
	audit  *recAudit
	add    *uc.AddMember
	change *uc.ChangeMemberRole
	remove *uc.RemoveMember
	list   *uc.ListMembers
}

func (w *world) memberCases(gate func(*recAudit) port.AuditWriter) *memberCases {
	rec := &recAudit{}
	var aw port.AuditWriter = rec
	if gate != nil {
		aw = gate(rec)
	}
	log := w.runnerLog()
	d := uc.Deps{
		Runner: w.runner, Checker: w.checker, Nodes: w.repo, Members: postgres.NewMemberRepo(log),
		Users: postgres.NewUserRepo(log), Hasher: testHasher{}, Audit: aw, Clock: realClock{}, IDs: v7IDs{},
	}
	return &memberCases{
		audit: rec, add: uc.NewAddMember(d), change: uc.NewChangeMemberRole(d),
		remove: uc.NewRemoveMember(d), list: uc.NewListMembers(d),
	}
}

func (w *world) addMember(c *memberCases, actor, nodeKey, email, role, password string) (uc.AddMemberOutput, error) {
	return c.add.Execute(context.Background(), uc.AddMemberInput{
		Actor: w.users[actor], RequestID: "t", NodeID: w.id(nodeKey), Email: email, Role: role, InitialPassword: password,
	})
}

func (w *world) changeMember(c *memberCases, actor, nodeKey, target, role string) error {
	_, err := c.change.Execute(context.Background(), uc.ChangeMemberRoleInput{
		Actor: w.users[actor], RequestID: "t", NodeID: w.id(nodeKey), UserID: w.users[target], Role: role,
	})
	return err
}

func (w *world) removeMember(c *memberCases, actor, nodeKey, target string) error {
	return c.remove.Execute(context.Background(), uc.RemoveMemberInput{
		Actor: w.users[actor], RequestID: "t", NodeID: w.id(nodeKey), UserID: w.users[target],
	})
}

// assignments returns user email -> role on a node, read as the owner.
func (w *world) assignments(nodeKey string) map[string]string {
	w.t.Helper()
	rows, err := w.db.Owner.Query(context.Background(),
		`SELECT u.email, ra.role FROM role_assignments ra JOIN users u ON u.id = ra.user_id WHERE ra.node_id = $1`,
		uuid.UUID(w.id(nodeKey)))
	require.NoError(w.t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var e, r string
		require.NoError(w.t, rows.Scan(&e, &r))
		out[e] = r
	}
	require.NoError(w.t, rows.Err())
	return out
}

// countUsers counts the users with the email, read as the owner.
func (w *world) countUsers(email string) int {
	w.t.Helper()
	var n int
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE email = $1`, email).Scan(&n))
	return n
}

// runnerLog is a logger that writes into the world's buffer, so a test can
// check what was logged (and what must never be).
func (w *world) runnerLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
