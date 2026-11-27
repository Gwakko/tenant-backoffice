//go:build integration

package isolation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// only keeps the ops whose name starts with one of the prefixes.
func only(ops []hOp, prefixes ...string) []hOp {
	var out []hOp
	for _, op := range ops {
		for _, p := range prefixes {
			if strings.HasPrefix(op.name, p) {
				out = append(out, op)
				break
			}
		}
	}
	return out
}

// everyone is every actor of the tables: writers and readers.
func (h *hWorld) everyone() []hActor { return append(h.actors(), h.readers()...) }

// requireOutsideScopeLikeMissing: a node the reader sees (Multi views B1) but
// that lies outside the scope of the listing is a missing node, as a foreign one.
func requireOutsideScopeLikeMissing(t *testing.T, h *hWorld, listing string) {
	t.Helper()
	w := h.w
	multi := h.as(w.Multi)
	path := func(id uuid.UUID) string {
		return hAPI + "/" + listing + "?scopeNodeId=" + w.A1.String() + "&nodeId=" + id.String()
	}
	missing := multi.Get(path(uuid.New()))
	requireHTTPNotFound(t, listing+" missing", missing)
	for _, outside := range []uuid.UUID{w.B1, w.B1u1, w.B1u2} {
		requireSameAnswer(t, listing+" outside the scope "+outside.String(), missing, multi.Get(path(outside)))
	}
	// Inside the scope the same filter answers.
	hStatus(t, multi.Get(path(w.A1u1)), http.StatusOK)
}

// Test-ID: T-ISO-05
// EC-50: balance, credit, debit and transfer with a node of another branch
// (as source, as destination through toNodeId in the body, and between two
// foreign nodes) answer as a missing node answers: byte for byte; balances, the
// journal and the idempotency keys do not change, not even the refused key is
// stored, and the same key sent again is still the 404.
func TestHTTPForeignBalanceOps(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()
	for _, a := range h.actors() {
		t.Run(a.name, func(t *testing.T) {
			ops := append(only(ledgerReadOps(a), "balance"), moneyWriteOps(a)...)
			requireAllLikeMissing(t, a, ops, a.foreign)
		})
	}
	for _, a := range h.readers() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, append(only(ledgerReadOps(a), "balance"), moneyWriteOps(a)...), a.foreign)
		})
	}
	admin := h.as(w.AdminA)
	key := hKey()
	body := map[string]string{"amount": "1"}
	first := admin.Post(hNodePath(w.B1u1, "/credits"), body, apitest.WithIdempotencyKey(key))
	again := admin.Post(hNodePath(w.B1u1, "/credits"), body, apitest.WithIdempotencyKey(key))
	requireHTTPNotFound(t, "first", first)
	requireSameAnswer(t, "the same key again", first, again)
	// A transfer from one node of A to one of B, and back, and between two of B.
	cross := func(from, to uuid.UUID) *apitest.Response {
		return admin.Post(hAPI+"/transfers",
			map[string]string{"fromNodeId": from.String(), "toNodeId": to.String(), "amount": "1"},
			apitest.WithIdempotencyKey(hKey()))
	}
	toMissing := cross(w.A1u1, uuid.New())
	requireHTTPNotFound(t, "to missing", toMissing)
	requireSameAnswer(t, "A to B", toMissing, cross(w.A1u1, w.B1u1))
	fromMissing := cross(uuid.New(), w.A1u1)
	requireSameAnswer(t, "B to A", fromMissing, cross(w.B1u1, w.A1u1))
	requireSameAnswer(t, "B to B", cross(uuid.New(), uuid.New()), cross(w.B1u1, w.B1u2))

	// A user who sees the foreign node read-only is refused, not told it is missing, and nothing moves.
	multi := h.as(w.Multi)
	hStatus(t, multi.Post(hAPI+"/transfers", map[string]string{
		"fromNodeId": w.A1u1.String(), "toNodeId": w.B1u1.String(), "amount": "1",
	}, apitest.WithIdempotencyKey(hKey())),
		http.StatusForbidden)
	hStatus(t, multi.Post(hNodePath(w.B1u1, "/credits"), body, apitest.WithIdempotencyKey(hKey())), http.StatusForbidden)

	h.requireUnchanged(before, "no balance, operation, key or audit entry changed")
	requireNoLogErrors(t, h.s)

	// Control: the same call on an own node acts, and the other branch stays as it was.
	bBefore := hDecode[gen.Balance](t, h.as(w.AdminB).Get(hNodePath(w.B1u1, "/balance"))).Amount
	hStatus(t, admin.Post(hNodePath(w.A1u1, "/credits"), body, apitest.WithIdempotencyKey(hKey())), http.StatusCreated)
	assert.Equal(t, "1101", hDecode[gen.Balance](t, admin.Get(hNodePath(w.A1u1, "/balance"))).Amount)
	assert.Equal(t, bBefore, hDecode[gen.Balance](t, h.as(w.AdminB).Get(hNodePath(w.B1u1, "/balance"))).Amount)
}

// Test-ID: T-ISO-06
// EC-51: a report of another branch, or of the root above the access root, is
// the answer of a missing node (summary and children, byte for byte); a report
// of an own node holds only own figures and its breakdown by children lists
// only own children; the owner of the root sees both branches and their sum.
// (The earlier apitest TestAPIReportForeignBranch is the same criterion on the
// body only.)
func TestHTTPForeignReports(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	ctx := t.Context()
	// Different sizes, so that a mix-up of the branches changes a figure.
	w.AddOperation(ctx, t, w.AdminA, w.A1u1, "credit", 77, 1177, nil, nil)
	w.AddOperation(ctx, t, w.AdminB, w.B1u1, "credit", 999999, 1000999, nil, nil)
	w.AddOperation(ctx, t, w.AdminB, w.B1u2, "credit", 555555, 556055, nil, nil)
	before := h.snap()

	for _, a := range h.everyone() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, only(ledgerReadOps(a), "report"), a.foreign)
		})
	}

	adminA, adminB, owner := h.as(w.AdminA), h.as(w.AdminB), h.as(w.OwnerRoot)
	q := reportQuery()
	sumA := adminA.Get(hNodePath(w.A, "/reports/summary?"+q))
	requireNoneOf(t, "summary of A", sumA.Body, idStrings(w.NodesB, []uuid.UUID{w.Root}))
	assert.Equal(t, gen.Totals{Count: 5, Credits: "1777", Debits: "100", Net: "1677"}, hDecode[gen.SubtreeSummary](t, sumA).Totals)
	sumB := hDecode[gen.SubtreeSummary](t, adminB.Get(hNodePath(w.B, "/reports/summary?"+q)))
	assert.Equal(t, gen.Totals{Count: 6, Credits: "1557254", Debits: "100", Net: "1557154"}, sumB.Totals)

	for _, tc := range []struct {
		node uuid.UUID
		want []uuid.UUID
	}{{w.A, []uuid.UUID{w.A1}}, {w.A1, []uuid.UUID{w.A1u1, w.A1u2}}} {
		kids := adminA.Get(hNodePath(tc.node, "/reports/children?"+q))
		requireNoneOf(t, "breakdown", kids.Body, idStrings(w.NodesB, []uuid.UUID{w.Root}))
		var got []uuid.UUID
		for _, it := range hDecode[gen.ChildTotalsPage](t, kids).Items {
			got = append(got, uuid.UUID(it.NodeId))
		}
		assert.ElementsMatch(t, tc.want, got, "the breakdown lists exactly the own children")
	}

	// A user who also sees a company of the other branch (viewer of B1) gets the
	// scope asked for and nothing of that company: not in the totals, not in the breakdown.
	multi := h.as(w.Multi)
	mSum := multi.Get(hNodePath(w.A1, "/reports/summary?"+q))
	requireNoneOf(t, "multi summary", mSum.Body, idStrings(w.NodesB, []uuid.UUID{w.Root}))
	assert.Equal(t, gen.Totals{Count: 5, Credits: "1777", Debits: "100", Net: "1677"}, hDecode[gen.SubtreeSummary](t, mSum).Totals)
	mKids := multi.Get(hNodePath(w.A1, "/reports/children?"+q))
	requireNoneOf(t, "multi breakdown", mKids.Body, idStrings(w.NodesB, []uuid.UUID{w.Root}))
	var mGot []uuid.UUID
	for _, it := range hDecode[gen.ChildTotalsPage](t, mKids).Items {
		mGot = append(mGot, uuid.UUID(it.NodeId))
	}
	assert.ElementsMatch(t, []uuid.UUID{w.A1u1, w.A1u2}, mGot)
	bKids := multi.Get(hNodePath(w.B1, "/reports/children?"+q))
	requireNoneOf(t, "multi breakdown of B1", bKids.Body, idStrings(w.NodesA, []uuid.UUID{w.Root, w.A1}))

	all := hDecode[gen.SubtreeSummary](t, owner.Get(hNodePath(w.Root, "/reports/summary?"+q)))
	assert.Equal(t, gen.Totals{Count: 11, Credits: "1559031", Debits: "200", Net: "1558831"}, all.Totals)
	rootKids := hDecode[gen.ChildTotalsPage](t, owner.Get(hNodePath(w.Root, "/reports/children?"+q)))
	require.Len(t, rootKids.Items, 2)
	assert.Equal(t, "1677", rootKids.Items[0].Net)
	assert.Equal(t, "1557154", rootKids.Items[1].Net)
	h.requireUnchanged(before, "reports changed data")
}

// Test-ID: T-ISO-07
// EC-52: the audit of another branch: scopeNodeId of B, a nodeId filter of B
// inside an own scope, and the root, answer as a missing node; the own list
// holds only own entries (even after the other branch worked); the filter by an
// actor of the other branch does not say whether the actor exists.
func TestHTTPForeignAudit(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	adminA, adminB := h.as(w.AdminA), h.as(w.AdminB)
	// Both branches write audit entries through the API.
	hStatus(t, adminA.Patch(hNodePath(w.A1u2, ""), map[string]string{"name": "A1u2 renamed"}), http.StatusOK)
	hStatus(t, adminB.Patch(hNodePath(w.B1u2, ""), map[string]string{"name": "B1u2 renamed"}), http.StatusOK)
	hStatus(t, adminB.Put(hNodePath(w.B1, "/settings/operations_enabled"), map[string]any{"value": false}), http.StatusOK)
	before := h.snap()

	for _, a := range h.everyone() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, only(ledgerReadOps(a), "audit"), a.foreign)
		})
	}

	requireOutsideScopeLikeMissing(t, h, "audit")

	// The own list, paged one by one, holds own entries only.
	forbidden := append(idStrings(w.NodesB, []uuid.UUID{w.Root, w.AdminB, w.AuditRoot}, w.AuditB),
		"B1u2 renamed", w.Emails[w.AdminB], w.Emails[w.OwnerRoot])
	var seen []gen.AuditEntry
	cursor := ""
	for {
		q := "scopeNodeId=" + w.A.String() + "&limit=1"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		r := adminA.Get(hAPI + "/audit?" + q)
		requireNoneOf(t, "audit of A", r.Body, forbidden)
		page := hDecode[gen.AuditPage](t, r)
		seen = append(seen, page.Items...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	require.GreaterOrEqual(t, len(seen), 3, "the two seeded entries and the rename")
	inA := map[uuid.UUID]bool{}
	for _, n := range w.NodesA {
		inA[n] = true
	}
	for _, e := range seen {
		assert.True(t, inA[uuid.UUID(e.NodeId)], "entry %s is on a node of A", e.Id)
	}
	// The admin of B reads its own entries: the control that the data exists.
	mine := adminB.Get(hAPI + "/audit?scopeNodeId=" + w.B.String() + "&limit=200")
	assert.Contains(t, string(mine.Body), "B1u2 renamed")

	// A filter by the actor of the other branch, and by a missing actor, are one answer.
	byOther := adminA.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&actorId=" + w.AdminB.String())
	byNobody := adminA.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&actorId=" + uuid.NewString())
	hStatus(t, byOther, http.StatusOK)
	requireSameAnswer(t, "actorId filter", byNobody, byOther)
	assert.Empty(t, hDecode[gen.AuditPage](t, byOther).Items)

	h.requireUnchanged(before, "reads changed data")
}

// Test-ID: T-ISO-08
// EC-53: the operations of another branch: scopeNodeId of B, a nodeId filter of
// B in an own scope and an operation id of B (every operation of the World,
// both legs of the transfer) answer as missing ones; the own list holds own
// operations only, with their counterparties inside the branch.
func TestHTTPForeignOperations(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	before := h.snap()

	for _, a := range h.everyone() {
		t.Run(a.name, func(t *testing.T) {
			requireAllLikeMissing(t, a, only(ledgerReadOps(a), "operations"), a.foreign)
		})
	}

	requireOutsideScopeLikeMissing(t, h, "operations")

	byID := hOp{name: "operation by id", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
		return c.Get(hAPI + "/operations/" + id.String())
	}}
	opsB := append([]uuid.UUID{w.CreditB, w.TransferB}, w.OpsB...)
	opsA := append([]uuid.UUID{w.CreditA, w.TransferA}, w.OpsA...)
	adminA := h.actors()[0]
	adminB := h.actors()[1]
	requireAllLikeMissing(t, adminA, []hOp{byID}, opsB)
	requireAllLikeMissing(t, adminB, []hOp{byID}, opsA)
	for _, a := range h.readers() {
		requireAllLikeMissing(t, a, []hOp{byID}, opsB)
	}

	// The own list: every item on a node of A; B's operations are not in it.
	own := map[uuid.UUID]bool{}
	for _, id := range w.OpsA {
		own[id] = true
	}
	cursor := ""
	var got []uuid.UUID
	for {
		q := "scopeNodeId=" + w.A.String() + "&limit=2"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		r := adminA.c.Get(hAPI + "/operations?" + q)
		requireNoneOf(t, "operations of A", r.Body, idStrings(opsB, w.NodesB, []uuid.UUID{w.Root, w.AdminB}))
		page := hDecode[gen.OperationPage](t, r)
		for _, it := range page.Items {
			got = append(got, uuid.UUID(it.Id))
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	assert.ElementsMatch(t, w.OpsA, got)
	// The admin of B sees its own: the control.
	mine := hDecode[gen.OperationPage](t, adminB.c.Get(hAPI+"/operations?scopeNodeId="+w.B.String()+"&limit=200"))
	assert.Len(t, mine.Items, len(w.OpsB))
	hStatus(t, adminB.c.Get(hAPI+"/operations/"+w.CreditB.String()), http.StatusOK)
	h.requireUnchanged(before, "reads changed data")
}
