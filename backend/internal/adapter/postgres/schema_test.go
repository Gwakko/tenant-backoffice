//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

type nodeRow struct {
	id     uuid.UUID
	typ    string
	parent *uuid.UUID
	path   string
}

func newNode(typ string, parent *nodeRow) nodeRow {
	id := uuid.New()
	n := nodeRow{id: id, typ: typ}
	label := strings.ReplaceAll(id.String(), "-", "")
	if parent == nil {
		n.path = label
	} else {
		n.path = parent.path + "." + label
		n.parent = &parent.id
	}
	return n
}

func insert(ctx context.Context, db *testkit.DB, n nodeRow) error {
	now := time.Now().UTC()
	_, err := db.Owner.Exec(ctx,
		`INSERT INTO nodes (id, type, parent_id, path, name, name_key, created_at, updated_at)
		 VALUES ($1, $2, $3, $4::ltree, $5, $5, $6, $6)`,
		n.id, n.typ, n.parent, n.path, "n-"+n.id.String()[:8], now)
	return err
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// Test-ID: T-I-12
func TestTreeConstraints(t *testing.T) {
	ctx := context.Background()
	db := testkit.NewDB(t)

	root := newNode("platform", nil)
	require.NoError(t, insert(ctx, db, root))
	partner := newNode("partner", &root)
	require.NoError(t, insert(ctx, db, partner))
	company := newNode("company", &partner)
	require.NoError(t, insert(ctx, db, company))
	unit := newNode("unit", &company)
	require.NoError(t, insert(ctx, db, unit))

	const checkViolation = "23514"
	cases := []struct {
		name string
		node func() nodeRow
		code string
	}{
		{"unit child of a unit has nlevel 5", func() nodeRow { return newNode("unit", &unit) }, checkViolation},
		{"unit directly under a partner", func() nodeRow {
			n := newNode("unit", &partner)
			return n
		}, checkViolation},
		{"company under the platform", func() nodeRow { return newNode("company", &root) }, checkViolation},
		{"second platform", func() nodeRow { return newNode("platform", nil) }, "23505"},
		{"non-platform without parent", func() nodeRow {
			n := newNode("partner", &root)
			n.parent = nil
			return n
		}, checkViolation},
		{"path does not extend the parent path", func() nodeRow {
			n := newNode("partner", &root)
			other := newNode("platform", nil)
			n.path = other.path + "." + strings.ReplaceAll(n.id.String(), "-", "")
			return n
		}, checkViolation},
		{"last label differs from id", func() nodeRow {
			n := newNode("partner", &root)
			n.path = root.path + ".deadbeef"
			return n
		}, checkViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := insert(ctx, db, tc.node())
			require.Error(t, err)
			require.Equal(t, tc.code, sqlState(err), fmt.Sprint(err))
		})
	}

	t.Run("trigger rejects a wrong parent type for a valid path level", func(t *testing.T) {
		// The path level matches type company, but parent_id points to a
		// company, so only the trigger can reject it.
		n := newNode("company", &partner)
		n.parent = &company.id
		err := insert(ctx, db, n)
		require.Error(t, err)
		require.Equal(t, checkViolation, sqlState(err))
		require.Contains(t, err.Error(), "not allowed under parent type")
	})

	t.Run("valid tree has no violations", func(t *testing.T) {
		rows, err := db.Owner.Query(ctx, `SELECT node_id, problem FROM tb_tree_violations()`)
		require.NoError(t, err)
		defer rows.Close()
		require.False(t, rows.Next())
		require.NoError(t, rows.Err())
	})

	t.Run("tb_tree_violations reports a mismatched path and level", func(t *testing.T) {
		// Bypass both lines of defence to prove the helper sees the damage.
		_, err := db.Owner.Exec(ctx, `ALTER TABLE nodes DISABLE TRIGGER nodes_parent_type_check`)
		require.NoError(t, err)
		_, err = db.Owner.Exec(ctx, `DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
           WHERE conrelid = 'nodes'::regclass AND contype = 'c'
             AND pg_get_constraintdef(oid) LIKE '%nlevel%' LOOP
    EXECUTE format('ALTER TABLE nodes DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$`)
		require.NoError(t, err)
		_, err = db.Owner.Exec(ctx, `UPDATE nodes SET path = $1::ltree WHERE id = $2`,
			root.path+"."+strings.ReplaceAll(unit.id.String(), "-", ""), unit.id)
		require.NoError(t, err)

		rows, err := db.Owner.Query(ctx, `SELECT node_id, problem FROM tb_tree_violations() WHERE node_id = $1`, unit.id)
		require.NoError(t, err)
		defer rows.Close()
		var problems []string
		for rows.Next() {
			var id uuid.UUID
			var problem string
			require.NoError(t, rows.Scan(&id, &problem))
			problems = append(problems, problem)
		}
		require.NoError(t, rows.Err())
		require.Contains(t, problems, "level_mismatch")
		require.Contains(t, problems, "path_mismatch")
	})
}
