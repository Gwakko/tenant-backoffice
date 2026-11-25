//go:build integration

package isolation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
)

// Test-ID: T-ISO-09
// Enumeration: 100 ids (50 real nodes of branch B, 25 random v4, 25 random v7)
// asked by the admin of A through every use case that takes a node id. The
// answers are the same value for all of them; a malformed (zero) id is
// rejected the same way for every actor, whoever it belongs to.
func TestIDEnumeration(t *testing.T) {
	ctx, db, w, a := setup(t)

	realIDs := concat(w.NodesB) // 4 nodes of the fixture
	for i := range 46 {
		realIDs = append(realIDs, w.AddNode(ctx, t, "unit", &w.B1, "extra-"+uuid.NewString()[:8]+string(rune('a'+i%26))))
	}
	require.Len(t, realIDs, 50)
	var random []uuid.UUID
	for range 25 {
		random = append(random, uuid.New(), uuid.Must(uuid.NewV7()))
	}
	require.Len(t, random, 50)
	all := concat(realIDs, random)

	ops := concat(readOps(), writeOps(w.A), memberOps(w.AdminB, w.Emails[w.AdminB]))
	for _, who := range []struct {
		name  string
		actor uuid.UUID
	}{
		{"admin of A", w.AdminA},
		{"viewer of a unit of A", w.ViewerA1u1},
		{"user without roles", w.Orphan},
		{"unknown actor", uuid.New()},
	} {
		t.Run(who.name, func(t *testing.T) {
			before := isolation.Snapshot(ctx, t, db)
			requireIndistinguishable(ctx, t, a, who.actor, ops, all)
			assert.Equal(t, before, isolation.Snapshot(ctx, t, db))
			assert.Empty(t, a.audit.all())
		})
	}

	t.Run("the same id gives different answers only to those who may see it", func(t *testing.T) {
		// Control: the admin of B sees what the admin of A is told does not exist.
		for _, id := range realIDs {
			_, err := a.get.Execute(ctx, getInput(w.AdminB, id))
			require.NoError(t, err)
			_, err = a.get.Execute(ctx, getInput(w.AdminA, id))
			assert.Equal(t, apperr.NotFoundError("node"), err)
		}
		for _, id := range random {
			_, err := a.get.Execute(ctx, getInput(w.AdminB, id))
			assert.Equal(t, apperr.NotFoundError("node"), err)
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		first := map[string]error{}
		for _, actor := range []uuid.UUID{w.AdminA, w.AdminB, w.OwnerRoot, w.Orphan, uuid.New()} {
			for _, o := range ops {
				_, err := o.run(ctx, a, actor, uuid.Nil)
				require.Error(t, err, o.name)
				code, _ := apperr.CodeOf(err)
				assert.Equal(t, apperr.ValidationFailed, code, o.name)
				if base, ok := first[o.name]; ok {
					assert.Equal(t, base, err, "%s: the answer to a malformed id depends on the actor", o.name)
				} else {
					first[o.name] = err
				}
			}
		}
	})
}
