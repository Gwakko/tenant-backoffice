//go:build integration

package apitest_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-46
// The balance of a node: the amount is a string, the currency comes from the
// installation, a node without operations has zero.
func TestAPIBalanceRead(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin, viewer := s.As(w.AdminA), s.As(w.ViewerA1u1)

	res := admin.Get(nodeURL(w.A1u1, "/balance"))
	requireStatus(t, res, http.StatusOK)
	b := decode[gen.Balance](t, res)
	assert.Equal(t, w.A1u1, b.NodeId)
	assert.Equal(t, "1100", b.Amount)
	assert.Equal(t, "EUR", b.Currency)
	assert.False(t, b.UpdatedAt.IsZero())
	assert.Contains(t, string(res.Body), `"amount":"1100"`, "money is a string on the wire")

	assert.Equal(t, "1100", balanceOf(t, viewer, w.A1u1), "a viewer reads the balance")
	assert.Equal(t, "0", balanceOf(t, admin, w.A), "a node without operations has zero")
	requireStatus(t, viewer.Get(nodeURL(w.A1u2, "/balance")), http.StatusNotFound)
	requireStatus(t, s.Anon.Get(nodeURL(w.A1u1, "/balance")), http.StatusUnauthorized)
}

// Test-ID: T-A-47
// Credit and debit: 201, the stored operation, the new balance, the actor from
// the token, one audit entry each.
func TestAPICreditDebitFlow(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	res := admin.Post(nodeURL(w.A1u1, "/credits"), map[string]string{"amount": "100", "comment": "top up"},
		apitest.WithIdempotencyKey(newKey("c")))
	op := mustOperation(t, res)
	assert.Equal(t, gen.Credit, op.Kind)
	assert.Equal(t, "100", op.Amount)
	assert.Equal(t, "1200", op.BalanceAfter)
	assert.Equal(t, w.A1u1, op.NodeId)
	assert.Equal(t, w.AdminA, op.ActorId)
	assert.Equal(t, "top up", str(op.Comment))
	assert.Nil(t, op.TransferId)
	assert.Nil(t, op.CounterpartyNodeId)
	assert.Empty(t, res.Header.Get("Idempotent-Replayed"), "the first answer is not a replay")

	d := mustOperation(t, debit(admin, w.A1u1, "250", newKey("d")))
	assert.Equal(t, gen.Debit, d.Kind)
	assert.Equal(t, "950", d.BalanceAfter)
	assert.Nil(t, d.Comment, "no comment is null")
	assert.Equal(t, "950", balanceOf(t, admin, w.A1u1))

	got := admin.Get(api + "/operations/" + d.Id.String())
	requireStatus(t, got, http.StatusOK)
	assert.Equal(t, d, decode[gen.Operation](t, got))

	actions := map[string]int{}
	for _, e := range auditPage(t, admin, "scopeNodeId="+w.A1u1.String()+"&limit=200").Items {
		actions[string(e.Action)]++
	}
	assert.Equal(t, 1, actions["balance.credited"])
	assert.Equal(t, 1, actions["balance.debited"])
}

// Test-ID: T-A-48
// A transfer answers with both legs, the same transfer id and the right
// counterparties; both balances move together.
func TestAPITransferBothLegs(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	res := transfer(admin, w.A1u2, w.A1u1, "200", newKey("t"))
	requireStatus(t, res, http.StatusCreated)
	tr := decode[gen.Transfer](t, res)
	assert.Equal(t, gen.TransferOut, tr.Out.Kind)
	assert.Equal(t, gen.TransferIn, tr.In.Kind)
	assert.Equal(t, w.A1u2, tr.Out.NodeId)
	assert.Equal(t, w.A1u1, tr.In.NodeId)
	assert.Equal(t, "300", tr.Out.BalanceAfter)
	assert.Equal(t, "1300", tr.In.BalanceAfter)
	require.NotNil(t, tr.Out.TransferId)
	require.NotNil(t, tr.In.TransferId)
	assert.Equal(t, tr.TransferId, *tr.Out.TransferId)
	assert.Equal(t, tr.TransferId, *tr.In.TransferId)
	require.NotNil(t, tr.Out.CounterpartyNodeId)
	assert.Equal(t, w.A1u1, *tr.Out.CounterpartyNodeId)
	require.NotNil(t, tr.In.CounterpartyNodeId)
	assert.Equal(t, w.A1u2, *tr.In.CounterpartyNodeId)
	assert.Equal(t, "300", balanceOf(t, admin, w.A1u2))
	assert.Equal(t, "1300", balanceOf(t, admin, w.A1u1))

	// The same node twice is refused before anything is written (EC-78).
	before := stateOf(t, s)
	same := transfer(admin, w.A1u1, w.A1u1, "1", newKey("t"))
	same.RequireProblem(http.StatusBadRequest, "same_node")
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-49
// A transfer needs balance.transfer on both nodes: an unseen node is 404, a
// seen one without the right is 403, and nothing changes in either case.
func TestAPITransferNeedsBothNodes(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	before := stateOf(t, s)

	admin := s.As(w.AdminA)
	transfer(admin, w.A1u1, w.B1u1, "10", newKey("t")).RequireProblem(http.StatusNotFound, "not_found")
	transfer(admin, w.B1u1, w.A1u1, "10", newKey("t")).RequireProblem(http.StatusNotFound, "not_found")

	// multi administers A1 and only views B1: B1u1 is seen but not writable.
	multi := s.As(w.Multi)
	transfer(multi, w.A1u1, w.B1u1, "10", newKey("t")).RequireProblem(http.StatusForbidden, "forbidden")
	transfer(multi, w.B1u1, w.A1u1, "10", newKey("t")).RequireProblem(http.StatusForbidden, "forbidden")

	// The source is checked first: a viewer lacks the right on it (403) even
	// when the destination is not visible to it.
	viewer := s.As(w.ViewerA1u1)
	transfer(viewer, w.A1u1, w.A1u2, "10", newKey("t")).RequireProblem(http.StatusForbidden, "forbidden")
	transfer(viewer, w.A1u2, w.A1u1, "10", newKey("t")).RequireProblem(http.StatusNotFound, "not_found")
	transfer(viewer, w.A1u1, w.A1u1, "10", newKey("t")).RequireProblem(http.StatusBadRequest, "same_node")

	assert.Equal(t, before, stateOf(t, s), "no refused transfer wrote anything")
}

// Test-ID: T-A-08
// EC-43: a viewer sees the node and is refused credit, debit and transfer
// with 403 (the node-level writes are in TestAPIViewerCannotWrite).
func TestAPIViewerCannotMoveMoney(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	viewer := s.As(w.ViewerA1u1)
	before := stateOf(t, s)

	requireStatus(t, viewer.Get(nodeURL(w.A1u1, "/balance")), http.StatusOK)
	credit(viewer, w.A1u1, "1", newKey("v")).RequireProblem(http.StatusForbidden, "forbidden")
	debit(viewer, w.A1u1, "1", newKey("v")).RequireProblem(http.StatusForbidden, "forbidden")
	transfer(viewer, w.A1u1, w.A1u2, "1", newKey("v")).RequireProblem(http.StatusForbidden, "forbidden")
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-50
// The business refusals are plain: the documented code and status, the
// standard problem fields only, a fixed text and no SQL or internal detail in
// the body (EC-74, EC-77, EC-81).
func TestAPIBusinessErrorsAreOpaque(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	off := createNode(t, admin, w.A1, "unit", "off")
	requireStatus(t, admin.Put(nodeURL(off.Id, "/settings/operations_enabled"), map[string]any{"value": false}), http.StatusOK)
	dead := createNode(t, admin, w.A1, "unit", "dead")
	requireStatus(t, admin.Post(nodeURL(dead.Id, "/deactivate"), nil), http.StatusOK)
	before := stateOf(t, s)

	cases := []struct {
		name   string
		res    *apitest.Response
		status int
		code   string
	}{
		{"insufficient funds", debit(admin, w.A1u1, "2000", newKey("e")), http.StatusConflict, "insufficient_funds"},
		{"transfer insufficient", transfer(admin, w.A1u1, w.A1u2, "2000", newKey("e")), http.StatusConflict, "insufficient_funds"},
		{"amount over limit", credit(admin, w.A1u1, "5001", newKey("e")), http.StatusUnprocessableEntity, "amount_exceeds_limit"},
		{"operations disabled", credit(admin, off.Id, "1", newKey("e")), http.StatusConflict, "operations_disabled"},
		{"node inactive", credit(admin, dead.Id, "1", newKey("e")), http.StatusConflict, "node_inactive"},
	}
	allowed := map[string]bool{"type": true, "title": true, "status": true, "code": true, "detail": true, "requestId": true, "errors": true}
	digits := regexp.MustCompile(`[0-9]`)
	for _, c := range cases {
		p := c.res.RequireProblem(c.status, c.code)
		for _, e := range p.Errors {
			assert.False(t, digits.MatchString(e.Message), "%s: field detail %q", c.name, e.Message)
		}
		assert.False(t, digits.MatchString(p.Detail), "%s: the detail is a fixed text, got %q", c.name, p.Detail)
		var keys map[string]json.RawMessage
		c.res.JSON(&keys)
		for k := range keys {
			assert.True(t, allowed[k], "%s: unexpected field %q", c.name, k)
		}
		body := strings.ToLower(string(c.res.Body))
		for _, leak := range []string{"sqlstate", "violates", "constraint", "balance_after", "operations_pkey", "pgx", "postgres", "goroutine", "panic"} {
			assert.NotContains(t, body, leak, "%s leaks %q", c.name, leak)
		}
	}
	assert.Equal(t, before, stateOf(t, s), "a refused operation writes no ledger, balance, key or audit row")
}

// Test-ID: T-A-13
// EC-76: the amount is a string of digits; a JSON number, a decimal, an
// exponent, a leading zero, a sign and a value above MaxInt64 are
// invalid_amount, zero is amount_must_be_positive. Nothing is written.
func TestAPIAmountFormat(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	bodies := map[string]string{
		"number":          `{"amount":100}`,
		"decimal":         `{"amount":"10.5"}`,
		"exponent":        `{"amount":"1e3"}`,
		"leading zeros":   `{"amount":"007"}`,
		"above max int64": `{"amount":"9223372036854775808"}`,
		"negative":        `{"amount":"-5"}`,
		"plus":            `{"amount":"+5"}`,
		"space":           `{"amount":" 5"}`,
		"empty":           `{"amount":""}`,
		"null":            `{"amount":null}`,
		"object":          `{"amount":{"v":1}}`,
	}
	for _, route := range []string{"/credits", "/debits"} {
		for name, body := range bodies {
			res := admin.Post(nodeURL(w.A1u1, route), body, apitest.WithIdempotencyKey(newKey("a")))
			require.Equal(t, http.StatusBadRequest, res.Status, "%s %s: %s", route, name, res.Body)
			assert.Equal(t, "invalid_amount", res.Problem().Code, "%s %s", route, name)
		}
		res := admin.Post(nodeURL(w.A1u1, route), `{"amount":"0"}`, apitest.WithIdempotencyKey(newKey("a")))
		res.RequireProblem(http.StatusBadRequest, "amount_must_be_positive")
	}
	// The transfer body is checked the same way.
	tr := admin.Post(api+"/transfers", map[string]any{
		"fromNodeId": w.A1u2.String(), "toNodeId": w.A1u1.String(), "amount": 100,
	}, apitest.WithIdempotencyKey(newKey("a")))
	tr.RequireProblem(http.StatusBadRequest, "invalid_amount")
	assert.Equal(t, before, stateOf(t, s))

	// The largest amount is a valid string, refused later only by the limit.
	credit(admin, w.A1u1, "9223372036854775807", newKey("a")).RequireProblem(http.StatusUnprocessableEntity, "amount_exceeds_limit")
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-51
// Atomicity through HTTP (EC-71, EC-80): a refused credit, debit or transfer
// leaves no ledger, balance, audit or key row, and the key stays usable: after
// the shortfall is covered the same request with the same key succeeds.
func TestAPIRefusedRequestLeavesNoTrace(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	key := newKey("retry")
	debit(admin, w.A1u2, "800", key).RequireProblem(http.StatusConflict, "insufficient_funds")
	transfer(admin, w.A1u2, w.A1u1, "800", newKey("t")).RequireProblem(http.StatusConflict, "insufficient_funds")
	assert.Equal(t, before, stateOf(t, s), "nothing, not even the idempotency key, was stored")

	mustOperation(t, credit(admin, w.A1u2, "400", newKey("fund")))
	op := mustOperation(t, debit(admin, w.A1u2, "800", key))
	assert.Equal(t, "100", op.BalanceAfter, "the retry with the same key ran as a new request")

	// The transfer is all or nothing: both legs and both balances moved once.
	tr := decode[gen.Transfer](t, transfer(admin, w.A1u1, w.A1u2, "100", newKey("t")))
	assert.Equal(t, "1000", tr.Out.BalanceAfter)
	assert.Equal(t, "200", tr.In.BalanceAfter)
	assert.Equal(t, 2, count(t, s, `SELECT count(*) FROM operations WHERE transfer_id = $1`, tr.TransferId))
}
