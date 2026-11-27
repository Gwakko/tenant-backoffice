//go:build integration

package apitest_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-09
// EC-64: filters are values, never SQL: an injection string in kind, action,
// ids or the search text is a client error or a literal, no extra row comes
// back, and the tables are intact.
func TestAPIInjectionInQueryParams(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)
	scope := "scopeNodeId=" + w.A.String()

	inj := url.QueryEscape("credit') OR ('1'='1")
	admin.Get(api+"/operations?"+scope+"&kind="+inj).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(api+"/operations?"+scope+"&kind=credit&kind="+inj).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(api+"/audit?"+scope+"&action="+url.QueryEscape("node.created' OR '1'='1")).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	quote := url.QueryEscape("' OR 1=1 --")
	for _, q := range []string{
		"/operations?scopeNodeId=" + quote, "/operations?" + scope + "&nodeId=" + quote,
		"/audit?scopeNodeId=" + quote, "/audit?" + scope + "&actorId=" + quote, "/audit?" + scope + "&nodeId=" + quote,
	} {
		admin.Get(api+q).RequireProblem(http.StatusBadRequest, "invalid_id")
	}

	// A sort that is not in the contract is ignored, never concatenated.
	plain := operationsPage(t, admin, scope+"&limit=200")
	sorted := operationsPage(t, admin, scope+"&limit=200&sort="+url.QueryEscape("created_at; DROP TABLE operations --")+"&orderBy=amount")
	assert.Equal(t, plain, sorted)

	// A valid kind returns that kind only: no row the filter did not ask for.
	credits := operationsPage(t, admin, scope+"&limit=200&kind=credit")
	require.Len(t, credits.Items, 2)
	for _, it := range credits.Items {
		assert.Equal(t, gen.Credit, it.Kind)
	}

	// The search text of a listing is literal: % and quotes match nothing.
	for _, q := range []string{"%' OR 1=1 --", "%", "_", `\`} {
		res := admin.Get(nodeURL(w.A1, "/children?q="+url.QueryEscape(q)))
		requireStatus(t, res, http.StatusOK)
		assert.Empty(t, decode[gen.NodeSummaryPage](t, res).Items, "q=%q", q)
	}
	assert.Equal(t, before, stateOf(t, s), "the tables are intact")
	assert.Len(t, operationsPage(t, admin, scope+"&limit=200").Items, 4)
}
