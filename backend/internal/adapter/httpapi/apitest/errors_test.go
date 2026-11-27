//go:build integration

package apitest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
)

// Proposed ID (not in spec 08 yet): T-A-45
// A failure of the database reaches the client as a generic answer: no table,
// SQL state or driver text, and the cause goes to the log with the request id.
func TestAPIDatabaseErrorIsNotLeaked(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	_, err := s.DB.Owner.Exec(context.Background(), `DROP TABLE node_settings`)
	require.NoError(t, err)

	res := admin.Get(nodeURL(w.A1, "/settings"))
	require.GreaterOrEqual(t, res.Status, http.StatusInternalServerError, "body: %s", res.Body)
	p := res.Problem()
	require.Contains(t, []string{"internal", "db_unavailable"}, p.Code)
	body := strings.ToLower(string(res.Body))
	for _, leak := range []string{"node_settings", "relation", "sqlstate", "pgx", "does not exist", "select "} {
		require.NotContains(t, body, leak)
	}
	require.Contains(t, s.Log.String(), p.RequestID, "the cause is logged with the request id")
}
