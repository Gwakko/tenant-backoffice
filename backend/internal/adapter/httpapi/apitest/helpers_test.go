//go:build integration

package apitest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

const api = "/api/v1"

func nodeURL(id uuid.UUID, suffix string) string { return api + "/nodes/" + id.String() + suffix }

func requireStatus(t *testing.T, res *apitest.Response, status int) {
	t.Helper()
	require.Equal(t, status, res.Status, "body: %s", res.Body)
}

func decode[T any](t *testing.T, res *apitest.Response) T {
	t.Helper()
	var v T
	res.JSON(&v)
	return v
}

// createNode creates a child through the API and returns it.
func createNode(t *testing.T, c *apitest.Client, parent uuid.UUID, typ, name string) gen.Node {
	t.Helper()
	res := c.Post(nodeURL(parent, "/children"), map[string]string{"type": typ, "name": name})
	requireStatus(t, res, http.StatusCreated)
	return decode[gen.Node](t, res)
}

// count runs a count query as the table owner (the fixture side, not the API).
func count(t *testing.T, s *apitest.Stack, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, s.DB.Owner.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func nodeRows(t *testing.T, s *apitest.Stack) int {
	t.Helper()
	return count(t, s, `SELECT count(*) FROM nodes`)
}

// auditActors lists actor ids of audit rows of an action on a node.
func auditActors(t *testing.T, s *apitest.Stack, action string, node uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := s.DB.Owner.Query(context.Background(),
		`SELECT actor_id FROM audit_log WHERE action = $1 AND node_id = $2`, action, node)
	require.NoError(t, err)
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}
