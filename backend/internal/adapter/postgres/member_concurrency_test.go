//go:build integration

package postgres_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Test-ID: T-I-15
//
// Two owners of the root remove each other at the same moment. Both
// transactions are parked at the barrier after their checks and before the
// delete, so both decided on the same state. Without the lock on the owner rows
// both pass the check and the root is left without an owner; with it only one
// reaches the barrier, the other queues on the lock, rereads the owners after
// the first commit and is refused. The outcome does not depend on timing: the
// barrier only makes the unprotected variant fail every time.
func TestOwnersRemoveEachOther(t *testing.T) {
	for i := 0; i < 3; i++ {
		w := newWorld(t)
		gate := newBarrier(2)
		c := w.memberCases(func(rec *recAudit) port.AuditWriter {
			return gateAudit{recAudit: rec, action: "member.revoked", gate: gate}
		})
		_, err := w.addMember(c, "ownerRoot", "root", "orphan@example.test", "owner", "")
		require.NoError(t, err)

		errs := parallel(
			func() error { return w.removeMember(c, "ownerRoot", "root", "orphan") },
			func() error { return w.removeMember(c, "orphan", "root", "ownerRoot") },
		)

		var failed []error
		for _, e := range errs {
			if e != nil {
				failed = append(failed, e)
			}
		}
		require.Len(t, failed, 1, "exactly one removal succeeds, iteration %d", i)
		requireErrCode(t, failed[0], apperr.LastRootOwner)
		assert.Equal(t, 1, countRows(t, w, "role_assignments", "node_id = $1 AND role = 'owner'", uuid.UUID(w.id("root"))),
			"the root keeps one owner, iteration %d", i)
	}
}

// Test-ID: T-I-42
//
// The same email, written in two ways, is added at the same moment. The first
// transaction inserts the user and waits at the barrier before it commits; the
// second one blocks on the unique index and, once the first has committed, finds
// the user instead of creating one. Both answers are successes.
func TestSameEmailRace(t *testing.T) {
	t.Run("same node", func(t *testing.T) {
		w := newWorld(t)
		gate := newBarrier(2)
		c := w.memberCases(func(rec *recAudit) port.AuditWriter {
			return gateAudit{recAudit: rec, action: "member.granted", gate: gate}
		})
		var ids [2]string
		errs := parallel(
			func() error {
				out, err := w.addMember(c, "adminA", "A1", "User@X.io", "viewer", "twelve chars ok")
				ids[0] = out.Member.UserID.String()
				return err
			},
			func() error {
				out, err := w.addMember(c, "adminA", "A1", "user@x.io", "viewer", "another password")
				ids[1] = out.Member.UserID.String()
				return err
			},
		)
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, ids[0], ids[1], "one user")
		assert.Equal(t, 1, w.countUsers("user@x.io"))
		assert.Equal(t, map[string]string{"user@x.io": "viewer"}, filterEmail(w.assignments("A1"), "user@x.io"))
		assert.Len(t, c.audit.entriesOf("user.created"), 1)
		assert.Len(t, c.audit.entriesOf("member.granted"), 1, "the loser found an assignment already in place")
	})

	t.Run("two nodes", func(t *testing.T) {
		w := newWorld(t)
		gate := newBarrier(2)
		c := w.memberCases(func(rec *recAudit) port.AuditWriter {
			return gateAudit{recAudit: rec, action: "member.granted", gate: gate}
		})
		errs := parallel(
			func() error {
				_, err := w.addMember(c, "adminA", "A1u1", "User@X.io", "viewer", "twelve chars ok")
				return err
			},
			func() error {
				_, err := w.addMember(c, "adminA", "A1u2", "user@x.io", "analyst", "another password")
				return err
			},
		)
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, 1, w.countUsers("user@x.io"))
		assert.Len(t, filterEmail(w.assignments("A1u1"), "user@x.io"), 1)
		assert.Len(t, filterEmail(w.assignments("A1u2"), "user@x.io"), 1)
	})
}

func filterEmail(all map[string]string, email string) map[string]string {
	out := map[string]string{}
	if r, ok := all[email]; ok {
		out[email] = r
	}
	return out
}
