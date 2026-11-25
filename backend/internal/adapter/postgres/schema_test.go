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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
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

// Test-ID: T-I-12
func TestTreeConstraintsViaRepo(t *testing.T) {
	w := newWorld(t)
	insert := func(n node.Node) error {
		return w.try("adminA", func(ctx context.Context, tx port.Tx) error { return w.repo.Insert(ctx, tx, n) })
	}
	name := func(s string) node.Name {
		nm, err := node.ParseName(s)
		require.NoError(t, err)
		return nm
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	a1, a := w.nodes["A1"], w.nodes["A"]

	// Entities built by hand, bypassing node.NewChild (which would refuse them):
	// the database is the second line. The adapter reports the rejection as
	// not_found and logs the real reason at error level.
	badID := func() node.ID { return node.ID(uuid.Must(uuid.NewV7())) }
	cases := map[string]func() node.Node{
		"unit directly under a partner": func() node.Node {
			id := badID()
			return node.Node{ID: id, Type: node.TypeUnit, ParentID: &a.ID, Path: a.Path.Append(id), Name: name("x1"), Status: node.StatusActive, CreatedAt: now, UpdatedAt: now}
		},
		"unit child of a unit": func() node.Node {
			u := w.nodes["A1u1"]
			id := badID()
			return node.Node{ID: id, Type: node.TypeUnit, ParentID: &u.ID, Path: u.Path.Append(id), Name: name("x2"), Status: node.StatusActive, CreatedAt: now, UpdatedAt: now}
		},
		"path that is not the parent path plus the own label": func() node.Node {
			id := badID()
			return node.Node{ID: id, Type: node.TypeUnit, ParentID: &a1.ID, Path: w.nodes["A"].Path.Append(id), Name: name("x3"), Status: node.StatusActive, CreatedAt: now, UpdatedAt: now}
		},
		"unknown parent": func() node.Node {
			ghost, id := badID(), badID()
			return node.Node{ID: id, Type: node.TypeUnit, ParentID: &ghost, Path: a1.Path.Append(id), Name: name("x4"), Status: node.StatusActive, CreatedAt: now, UpdatedAt: now}
		},
	}
	for label, build := range cases {
		t.Run(label, func(t *testing.T) {
			err := insert(build())
			require.ErrorIs(t, err, apperr.New(apperr.NotFound, ""), label)
		})
	}
	assert.Contains(t, w.logs.String(), `"sqlstate":"23514"`)
	assert.Contains(t, w.logs.String(), `"level":"ERROR"`)

	// A node moved through the repository keeps the tree consistent.
	var a2 node.Node
	require.NoError(t, insert(w.newChild("A", node.TypeCompany, "A2")))
	a2 = w.lookupChild("A", "A2")
	w.do("adminA", func(ctx context.Context, tx port.Tx) {
		u1, err := w.repo.Get(ctx, tx, w.id("A1u1"))
		require.NoError(t, err)
		plan, err := u1.PlanMove(a2)
		require.NoError(t, err)
		require.NoError(t, w.repo.Move(ctx, tx, plan, 1, now))
	})
	assert.Empty(t, w.violations())
}
