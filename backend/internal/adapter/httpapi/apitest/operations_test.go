//go:build integration

package apitest_test

import (
	"bytes"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

func ids(items []gen.OperationListItem) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = it.Id
	}
	return out
}

// Test-ID: T-A-54
// The operation list of a scope: its own branch only, newest first, with the
// node name, filtered by node, kind (repeatable) and period.
func TestAPIOperationsList(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	scope := "scopeNodeId=" + w.A.String() + "&limit=200"

	page := operationsPage(t, admin, scope)
	assert.ElementsMatch(t, w.OpsA, ids(page.Items), "the operations of branch A and none of branch B")
	assert.Nil(t, page.NextCursor)
	for i, it := range page.Items {
		assert.Equal(t, w.NameOf[it.NodeId], it.NodeName)
		if i > 0 {
			prev := page.Items[i-1]
			assert.False(t, it.CreatedAt.After(prev.CreatedAt), "newest first")
			if it.CreatedAt.Equal(prev.CreatedAt) {
				assert.Negative(t, bytes.Compare(it.Id[:], prev.Id[:]), "ties by id descending")
			}
		}
	}

	byNode := operationsPage(t, admin, scope+"&nodeId="+w.A1u1.String())
	require.Len(t, byNode.Items, 2)
	for _, it := range byNode.Items {
		assert.Equal(t, w.A1u1, it.NodeId)
	}

	assert.Len(t, operationsPage(t, admin, scope+"&kind=credit").Items, 2)
	assert.Len(t, operationsPage(t, admin, scope+"&kind=transfer_in&kind=transfer_out").Items, 2)
	empty := admin.Get(api + "/operations?" + scope + "&kind=debit")
	requireStatus(t, empty, http.StatusOK)
	assert.Contains(t, string(empty.Body), `"items":[]`, "an empty page is [] and not null")

	past, future := "2000-01-01T00:00:00Z", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	assert.Len(t, operationsPage(t, admin, scope+"&from="+past+"&to="+future).Items, 4)
	assert.Empty(t, operationsPage(t, admin, scope+"&from="+url.QueryEscape(future)).Items)
	assert.Empty(t, operationsPage(t, admin, scope+"&to="+past).Items)
	admin.Get(api+"/operations?"+scope+"&from="+future+"&to="+past).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(api+"/operations?limit=5").RequireProblem(http.StatusBadRequest, "validation_failed")

	// operation.read is held by every role: a viewer reads its own unit, an
	// analyst the company; a scope above the role is not found.
	viewer := s.As(w.ViewerA1u1)
	assert.Len(t, operationsPage(t, viewer, "scopeNodeId="+w.A1u1.String()).Items, 2)
	viewer.Get(api+"/operations?scopeNodeId="+w.A1.String()).RequireProblem(http.StatusNotFound, "not_found")
	assert.Len(t, operationsPage(t, s.As(w.AnalystA), "scopeNodeId="+w.A1.String()).Items, 4)
	s.Anon.Get(api+"/operations?scopeNodeId="+w.A.String()).RequireProblem(http.StatusUnauthorized, "token_missing")
}

// Test-ID: T-A-55
// EC-92: pages never repeat and never skip an operation, also when many
// operations share one created_at, and a newer operation that appears between
// two pages does not enter the walk.
func TestAPIOperationsPagination(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	scope := "scopeNodeId=" + w.A.String()

	tie := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	insertOperations(t, s, w.A1u2, w.AdminA, 9, tie)
	for range 6 {
		mustOperation(t, credit(admin, w.A1u1, "1", newKey("p")))
	}
	all := operationsPage(t, admin, scope+"&limit=200")
	require.Len(t, all.Items, 4+9+6)

	var walked []uuid.UUID
	cursor, pages := "", 0
	for {
		q := scope + "&limit=7"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page := operationsPage(t, admin, q)
		require.LessOrEqual(t, len(page.Items), 7)
		walked = append(walked, ids(page.Items)...)
		pages++
		if pages == 1 {
			// A newer operation between the pages is not part of this walk.
			mustOperation(t, credit(admin, w.A1u1, "1", newKey("p")))
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		require.Less(t, pages, 10)
	}
	assert.Equal(t, 3, pages)
	assert.Equal(t, ids(all.Items), walked, "the pages are the full list, in order, with no repeat and no gap")
	assert.Len(t, operationsPage(t, admin, scope+"&limit=200").Items, 4+9+6+1)
}

// Test-ID: T-A-56
// EC-62: a cursor works only for the listing it came from: the same scope,
// node, kinds, period and reader. Any other listing, a changed or a cut cursor
// is 400 invalid_cursor.
func TestAPIOperationsCursorIsStrict(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	scopeA := "scopeNodeId=" + w.A.String()

	cursorOf := func(c *apitest.Client, q string) string {
		page := operationsPage(t, c, q)
		require.NotNil(t, page.NextCursor, q)
		return *page.NextCursor
	}
	c := cursorOf(admin, scopeA+"&limit=2")
	use := func(cl *apitest.Client, q, cur string) *apitest.Response {
		return cl.Get(api + "/operations?" + q + "&cursor=" + url.QueryEscape(cur))
	}
	requireStatus(t, use(admin, scopeA+"&limit=2", c), http.StatusOK)
	requireStatus(t, use(admin, scopeA+"&limit=1", c), http.StatusOK) // the page size is free

	foreign := map[string]string{
		"other scope":   "scopeNodeId=" + w.A1.String() + "&limit=2",
		"added node":    scopeA + "&nodeId=" + w.A1u1.String() + "&limit=2",
		"added kind":    scopeA + "&kind=credit&limit=2",
		"added from":    scopeA + "&from=2000-01-01T00:00:00Z&limit=2",
		"added to":      scopeA + "&to=2100-01-01T00:00:00Z&limit=2",
		"foreign scope": "scopeNodeId=" + w.B.String() + "&limit=2",
		"missing scope": "scopeNodeId=" + uuid.NewString() + "&limit=2",
	}
	for name, q := range foreign {
		res := use(admin, q, c)
		require.Equal(t, http.StatusBadRequest, res.Status, "%s: %s", name, res.Body)
		assert.Equal(t, "invalid_cursor", res.Problem().Code, name)
	}

	// A cursor of one node is not valid for another node of the same scope.
	cNode := cursorOf(admin, scopeA+"&nodeId="+w.A1u1.String()+"&limit=1")
	requireStatus(t, use(admin, scopeA+"&nodeId="+w.A1u1.String()+"&limit=1", cNode), http.StatusOK)
	use(admin, scopeA+"&nodeId="+w.A1u2.String()+"&limit=1", cNode).RequireProblem(http.StatusBadRequest, "invalid_cursor")

	// The kind set is the same set whatever the order or repeats.
	cKinds := cursorOf(admin, scopeA+"&kind=credit&kind=transfer_in&limit=1")
	requireStatus(t, use(admin, scopeA+"&kind=transfer_in&kind=credit&kind=credit&limit=1", cKinds), http.StatusOK)

	// Another reader, even of the same scope, cannot use the cursor.
	cCompany := cursorOf(admin, "scopeNodeId="+w.A1.String()+"&limit=2")
	use(s.As(w.AnalystA), "scopeNodeId="+w.A1.String()+"&limit=2", cCompany).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	use(s.As(w.AdminB), "scopeNodeId="+w.B.String()+"&limit=2", c).RequireProblem(http.StatusBadRequest, "invalid_cursor")

	// A cursor of the audit listing is no operations cursor, and the reverse.
	ac := auditPage(t, admin, scopeA+"&limit=1")
	require.NotNil(t, ac.NextCursor)
	use(admin, scopeA+"&limit=2", *ac.NextCursor).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	admin.Get(api+"/audit?"+scopeA+"&cursor="+url.QueryEscape(c)).RequireProblem(http.StatusBadRequest, "invalid_cursor")

	// Damaged cursors.
	head, sig, _ := strings.Cut(c, ".")
	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'A' {
			b[0] = 'B'
		} else {
			b[0] = 'A'
		}
		return string(b)
	}
	for name, bad := range map[string]string{
		"signature changed": head + "." + flip(sig),
		"payload changed":   flip(head) + "." + sig,
		"no signature":      head,
		"cut":               c[:len(c)/2],
		"swapped":           sig + "." + head,
		"garbage":           "garbage",
		"dots":              "..",
		"too long":          strings.Repeat("a", 3000),
		"empty parts":       ".",
	} {
		res := use(admin, scopeA+"&limit=2", bad)
		require.Equal(t, http.StatusBadRequest, res.Status, "%s: %s", name, res.Body)
		assert.Equal(t, "invalid_cursor", res.Problem().Code, name)
	}
}

// Test-ID: T-A-57
// One operation by id: the same record as in the list, readable with
// operation.read on its node; another branch or a node outside the reader's
// roles is not found, never forbidden.
func TestAPIGetOperation(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin, viewer := s.As(w.AdminA), s.As(w.ViewerA1u1)

	res := admin.Get(api + "/operations/" + w.CreditA.String())
	requireStatus(t, res, http.StatusOK)
	op := decode[gen.Operation](t, res)
	assert.Equal(t, w.CreditA, op.Id)
	assert.Equal(t, "1000", op.Amount)
	assert.Equal(t, w.A1u1, op.NodeId)
	var inList gen.OperationListItem
	for _, it := range operationsPage(t, admin, "scopeNodeId="+w.A.String()+"&limit=200").Items {
		if it.Id == op.Id {
			inList = it
		}
	}
	assert.Equal(t, op.Amount, inList.Amount)
	assert.Equal(t, op.BalanceAfter, inList.BalanceAfter)
	assert.Equal(t, op.CreatedAt, inList.CreatedAt)

	requireStatus(t, viewer.Get(api+"/operations/"+w.CreditA.String()), http.StatusOK)
	for _, foreign := range []uuid.UUID{w.CreditB, w.TransferB, w.OpsB[1]} {
		admin.Get(api+"/operations/"+foreign.String()).RequireProblem(http.StatusNotFound, "not_found")
	}
	// The viewer of unit 1 does not see the operations of unit 2.
	viewer.Get(api+"/operations/"+w.OpsA[1].String()).RequireProblem(http.StatusNotFound, "not_found")
	admin.Get(api+"/operations/"+uuid.NewString()).RequireProblem(http.StatusNotFound, "not_found")
	s.Anon.Get(api+"/operations/"+w.CreditA.String()).RequireProblem(http.StatusUnauthorized, "token_missing")
}

// Test-ID: T-A-58
// EC-52, EC-53, EC-54: for balances, operations and audit, a node, a scope, a
// node filter or an operation of another branch answers byte for byte like one
// that does not exist, and no foreign state is touched.
func TestAPIForeignLedgerIsIndistinguishableFromMissing(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	multi := s.As(w.Multi) // administers A1, views B1

	type call func(c *apitest.Client, id uuid.UUID) *apitest.Response
	nodeCalls := map[string]call{
		"balance": func(c *apitest.Client, id uuid.UUID) *apitest.Response { return c.Get(nodeURL(id, "/balance")) },
		"credit":  func(c *apitest.Client, id uuid.UUID) *apitest.Response { return credit(c, id, "1", newKey("f")) },
		"debit":   func(c *apitest.Client, id uuid.UUID) *apitest.Response { return debit(c, id, "1", newKey("f")) },
		"transfer from": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return transfer(c, id, w.A1u2, "1", newKey("f"))
		},
		"transfer to": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return transfer(c, w.A1u2, id, "1", newKey("f"))
		},
		"operations scope": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(api + "/operations?scopeNodeId=" + id.String())
		},
		"operations node filter": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(api + "/operations?scopeNodeId=" + w.A.String() + "&nodeId=" + id.String())
		},
		"audit scope": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(api + "/audit?scopeNodeId=" + id.String())
		},
		"audit node filter": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(api + "/audit?scopeNodeId=" + w.A.String() + "&nodeId=" + id.String())
		},
	}
	before := stateOf(t, s)
	for name, op := range nodeCalls {
		for _, foreign := range []uuid.UUID{w.B, w.B1, w.B1u1} {
			missing, got := op(admin, uuid.New()), op(admin, foreign)
			require.Equal(t, http.StatusNotFound, missing.Status, "%s missing: %s", name, missing.Body)
			require.Equal(t, http.StatusNotFound, got.Status, "%s foreign: %s", name, got.Body)
			assert.Equal(t, missing.BodyWithoutRequestID(), got.BodyWithoutRequestID(), name)
		}
	}
	opCalls := map[string]call{
		"operation": func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(api + "/operations/" + id.String())
		},
	}
	for name, op := range opCalls {
		for _, foreign := range slices.Concat(w.OpsB, []uuid.UUID{w.CreditB, w.TransferB}) {
			missing, got := op(admin, uuid.New()), op(admin, foreign)
			require.Equal(t, http.StatusNotFound, got.Status, "%s foreign: %s", name, got.Body)
			assert.Equal(t, missing.BodyWithoutRequestID(), got.BodyWithoutRequestID(), name)
		}
	}

	// A node the reader sees but that lies outside the scope is the same 404 as
	// a missing one: multi sees B1 as a viewer, scope A1 does not contain it.
	for name, q := range map[string]string{
		"operations": "/operations?scopeNodeId=" + w.A1.String() + "&nodeId=",
		"audit":      "/audit?scopeNodeId=" + w.A1.String() + "&nodeId=",
	} {
		missing := multi.Get(api + q + uuid.NewString())
		for _, outside := range []uuid.UUID{w.B1, w.B1u1} {
			got := multi.Get(api + q + outside.String())
			require.Equal(t, http.StatusNotFound, got.Status, "%s: %s", name, got.Body)
			assert.Equal(t, missing.BodyWithoutRequestID(), got.BodyWithoutRequestID(), name)
		}
	}
	assert.Equal(t, before, stateOf(t, s), "no foreign state was touched")
}

// Test-ID: T-A-61
// The actor is the authenticated user and nobody else: a body field naming
// another actor is refused, a query parameter or header naming one is ignored,
// and the audit filter actorId filters, it does not switch identity.
func TestAPILedgerActorIsNeverTakenFromTheRequest(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	for name, res := range map[string]*apitest.Response{
		"credit body": admin.Post(nodeURL(w.A1u1, "/credits"), map[string]string{"amount": "1", "actorId": w.AdminB.String()},
			apitest.WithIdempotencyKey(newKey("x"))),
		"transfer body": admin.Post(api+"/transfers", map[string]string{
			"fromNodeId": w.A1u2.String(), "toNodeId": w.A1u1.String(), "amount": "1", "userId": w.AdminB.String(),
		}, apitest.WithIdempotencyKey(newKey("x"))),
	} {
		res.RequireProblem(http.StatusBadRequest, "validation_failed")
		assert.NotEmpty(t, res.Problem().Errors, name)
	}
	assert.Equal(t, before, stateOf(t, s))

	res := admin.Post(nodeURL(w.A1u1, "/credits")+"?actorId="+w.AdminB.String()+"&userId="+w.AdminB.String(),
		map[string]string{"amount": "1"}, apitest.WithIdempotencyKey(newKey("x")),
		apitest.WithHeader("X-Actor-Id", w.AdminB.String()), apitest.WithHeader("X-User-Id", w.AdminB.String()))
	op := mustOperation(t, res)
	assert.Equal(t, w.AdminA, op.ActorId)
	assert.Equal(t, []uuid.UUID{w.AdminA}, auditActors(t, s, "balance.credited", w.A1u1))

	// actorId is a filter: another user's id gives an empty page of the same scope.
	other := admin.Get(api + "/audit?scopeNodeId=" + w.A.String() + "&actorId=" + w.AdminB.String())
	requireStatus(t, other, http.StatusOK)
	assert.Empty(t, decode[gen.AuditPage](t, other).Items)
	own := auditPage(t, admin, "scopeNodeId="+w.A.String()+"&limit=200&actorId="+w.AdminA.String())
	require.NotEmpty(t, own.Items)
	for _, e := range own.Items {
		assert.Equal(t, w.AdminA, e.ActorId)
	}
}
