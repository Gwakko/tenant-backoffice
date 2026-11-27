//go:build integration

package apitest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-30
// The whole life of a tenant through HTTP only: the owner of the root signs in,
// builds a partner with a company and two units, hands out roles, a company
// admin signs in and moves money (credit, debit, transfer, each retried with
// its key), the report and the audit tell the same story, the balances equal
// the sum of the operations, and both users sign out.
func TestAPIHappyPathFullScenario(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	ctx := context.Background()

	// 1. Sign in as the owner of the root.
	owner := signIn(t, s, w.Emails[w.OwnerRoot], apitest.Password)
	me := decode[gen.Me](t, owner.Get(api+"/me"))
	require.Equal(t, w.OwnerRoot, me.Id)
	require.Len(t, me.AccessRoots, 1)
	require.Equal(t, w.Root, me.AccessRoots[0].NodeId)

	// 2. Build the tree: a partner, a company, two units.
	partner := createNode(t, owner.Client, w.Root, "partner", "Scenario Partner")
	company := createNode(t, owner.Client, partner.Id, "company", "Scenario Company")
	u1 := createNode(t, owner.Client, company.Id, "unit", "Unit One")
	u2 := createNode(t, owner.Client, company.Id, "unit", "Unit Two")
	kids := decode[gen.NodeSummaryPage](t, owner.Get(nodeURL(company.Id, "/children")))
	require.Len(t, kids.Items, 2)
	got := decode[gen.Node](t, owner.Get(nodeURL(u1.Id, "")))
	require.Equal(t, []uuid.UUID{w.Root, partner.Id, company.Id, u1.Id}, crumbIDs(got.Breadcrumbs))

	// 3. Roles: an admin of the company, an analyst on it and a viewer of one unit.
	const adminMail, analystMail, viewerMail = "scenario-admin@example.test", "scenario-analyst@example.test", "scenario-viewer@example.test"
	for _, grant := range []struct {
		mail, role string
		node       uuid.UUID
	}{
		{adminMail, "admin", company.Id}, {analystMail, "analyst", company.Id}, {viewerMail, "viewer", u1.Id},
	} {
		res := owner.Post(nodeURL(grant.node, "/members"), map[string]any{
			"email": grant.mail, "role": grant.role, "initialPassword": newPassword12,
		})
		requireStatus(t, res, http.StatusCreated)
	}

	// 4. The company admin signs in; the viewer and the analyst cannot move money.
	admin := signIn(t, s, adminMail, newPassword12)
	viewer := signIn(t, s, viewerMail, newPassword12)
	requireStatus(t, admin.Get(nodeURL(company.Id, "")), http.StatusOK)
	admin.Get(nodeURL(partner.Id, "")).RequireProblem(http.StatusNotFound, "not_found")
	credit(viewer.Client, u1.Id, "1", newKey("sc")).RequireProblem(http.StatusForbidden, "forbidden")

	// 5. Money. Every call carries its key; a retry of a call is a replay.
	kCredit, kDebit, kTransfer := newKey("sc-credit"), newKey("sc-debit"), newKey("sc-transfer")
	c1 := mustOperation(t, credit(admin.Client, u1.Id, "1000", kCredit))
	replay := credit(admin.Client, u1.Id, "1000", kCredit)
	requireStatus(t, replay, http.StatusCreated)
	assert.Equal(t, "true", replay.Header.Get("Idempotent-Replayed"))
	assert.Equal(t, c1, decode[gen.Operation](t, replay), "a retry returns the same operation")
	credit(admin.Client, u1.Id, "999", kCredit).RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")
	d1 := mustOperation(t, debit(admin.Client, u1.Id, "250", kDebit))
	tr := transfer(admin.Client, u1.Id, u2.Id, "300", kTransfer)
	requireStatus(t, tr, http.StatusCreated)
	trOut := decode[gen.Transfer](t, tr)
	trReplay := transfer(admin.Client, u1.Id, u2.Id, "300", kTransfer)
	assert.Equal(t, "true", trReplay.Header.Get("Idempotent-Replayed"))
	assert.Equal(t, trOut, decode[gen.Transfer](t, trReplay))
	// Refusals leave no trace.
	debit(admin.Client, u2.Id, "100000", newKey("sc-big")).RequireProblem(http.StatusConflict, "insufficient_funds")
	// A second credit, then the balances.
	c2 := mustOperation(t, credit(admin.Client, u2.Id, "40", newKey("sc-credit2")))
	assert.Equal(t, "450", balanceOf(t, admin.Client, u1.Id))
	assert.Equal(t, "340", balanceOf(t, admin.Client, u2.Id))
	assert.Equal(t, "750", d1.BalanceAfter)
	assert.Equal(t, "40", c2.Amount)

	// 6. The operations list: five rows (two credits, a debit, both legs of the transfer), no gap in the pages.
	scope := "scopeNodeId=" + company.Id.String()
	ops := operationsPage(t, admin.Client, scope+"&limit=200")
	require.Len(t, ops.Items, 5)
	var walked []uuid.UUID
	for cursor := ""; ; {
		q := scope + "&limit=2"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page := operationsPage(t, admin.Client, q)
		for _, it := range page.Items {
			walked = append(walked, it.Id)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	var listed []uuid.UUID
	signed := map[uuid.UUID]int64{} // the effect of the operations on each node
	for _, it := range ops.Items {
		listed = append(listed, it.Id)
		amount := int64(0)
		for _, r := range it.Amount {
			amount = amount*10 + int64(r-'0')
		}
		switch it.Kind {
		case gen.Credit, gen.TransferIn:
			signed[it.NodeId] += amount
		case gen.Debit, gen.TransferOut:
			signed[it.NodeId] -= amount
		}
	}
	assert.Equal(t, listed, walked, "pages repeat nothing and skip nothing")

	// 7. The report of the company says the same: five operations, credits and debits, and the net is the money held.
	q := windowQuery()
	sum := decode[gen.SubtreeSummary](t, admin.Get(reportURL(company.Id, "summary", q)))
	assert.Equal(t, gen.Totals{Count: 5, Credits: "1340", Debits: "550", Net: "790"}, sum.Totals)
	rows := decode[gen.ChildTotalsPage](t, admin.Get(reportURL(company.Id, "children", q)))
	require.Len(t, rows.Items, 2)
	byNode := map[uuid.UUID]gen.ChildTotals{}
	for _, r := range rows.Items {
		byNode[r.NodeId] = r
	}
	assert.Equal(t, gen.ChildTotals{Active: true, Balance: "450", Count: 3, Credits: "1000", Debits: "550", Name: "Unit One", Net: "450", NodeId: u1.Id, Type: "unit"}, byNode[u1.Id])
	assert.Equal(t, gen.ChildTotals{Active: true, Balance: "340", Count: 2, Credits: "340", Debits: "0", Name: "Unit Two", Net: "340", NodeId: u2.Id, Type: "unit"}, byNode[u2.Id])

	// Consistency: the balance of each node is the sum of its operations, the last
	// operation says so, the table agrees, and the money in the company is the net of the report.
	var total int64
	for _, u := range []gen.Node{u1, u2} {
		held := decode[gen.Balance](t, admin.Get(nodeURL(u.Id, "/balance"))).Amount
		var inTable string
		require.NoError(t, s.DB.Owner.QueryRow(ctx, `SELECT amount::text FROM balances WHERE node_id = $1`, u.Id).Scan(&inTable))
		assert.Equal(t, held, inTable, "the API and the table agree on %s", u.Name)
		assert.Equal(t, fmt.Sprint(signed[u.Id]), held, "the balance of %s is the sum of its operations", u.Name)
		var last string
		require.NoError(t, s.DB.Owner.QueryRow(ctx,
			`SELECT balance_after::text FROM operations WHERE node_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, u.Id).Scan(&last))
		assert.Equal(t, held, last, "the last operation of %s names the balance", u.Name)
		total += signed[u.Id]
	}
	assert.Equal(t, int64(790), total)
	assert.Equal(t, 2, count(t, s, `SELECT count(*) FROM operations WHERE transfer_id = $1`, trOut.TransferId), "a transfer has both legs")
	assert.Equal(t, 5, count(t, s, `SELECT count(*) FROM operations WHERE node_id IN ($1, $2)`, u1.Id, u2.Id),
		"the retries and the refusals wrote no operation")
	assert.Equal(t, 4, count(t, s, `SELECT count(*) FROM idempotency_keys WHERE user_id = $1`, admin.tokens.User.Id),
		"one key per accepted call (two credits, a debit, a transfer) and none for the refusals")

	// 8. The audit tells the story in order, oldest first, and holds nothing of the refusals and retries.
	page := auditPage(t, owner.Client, "scopeNodeId="+partner.Id.String()+"&limit=200")
	entries := slices.Clone(page.Items)
	slices.Reverse(entries)
	type entry struct {
		action gen.AuditAction
		node   uuid.UUID
		actor  uuid.UUID
	}
	want := []entry{
		{"node.created", partner.Id, w.OwnerRoot},
		{"node.created", company.Id, w.OwnerRoot},
		{"node.created", u1.Id, w.OwnerRoot},
		{"node.created", u2.Id, w.OwnerRoot},
		{"user.created", company.Id, w.OwnerRoot},
		{"member.granted", company.Id, w.OwnerRoot},
		{"user.created", company.Id, w.OwnerRoot},
		{"member.granted", company.Id, w.OwnerRoot},
		{"user.created", u1.Id, w.OwnerRoot},
		{"member.granted", u1.Id, w.OwnerRoot},
		{"balance.credited", u1.Id, admin.tokens.User.Id},
		{"balance.debited", u1.Id, admin.tokens.User.Id},
		{"balance.transferred", u1.Id, admin.tokens.User.Id},
		{"balance.transferred", u2.Id, admin.tokens.User.Id},
		{"balance.credited", u2.Id, admin.tokens.User.Id},
	}
	var got2 []entry
	for _, e := range entries {
		got2 = append(got2, entry{e.Action, e.NodeId, e.ActorId})
	}
	assert.Equal(t, want, got2)
	for i := 1; i < len(entries); i++ {
		assert.False(t, entries[i].OccurredAt.Before(entries[i-1].OccurredAt), "oldest first after the reversal")
	}
	// The admin of the company sees the part that is under the company, not the partner's own entry.
	inCompany := auditPage(t, admin.Client, scope+"&limit=200")
	assert.Len(t, inCompany.Items, len(want)-1)

	// 9. Sign out: the refresh token dies with the session.
	for name, u := range map[string]session{"owner": owner, "admin": admin, "viewer": viewer} {
		out := u.Post(logoutPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF())
		require.Equal(t, http.StatusNoContent, out.Status, name)
		s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF()).
			RequireProblem(http.StatusUnauthorized, "refresh_invalid")
	}
}

func crumbIDs(cs []gen.Breadcrumb) []uuid.UUID {
	out := make([]uuid.UUID, len(cs))
	for i, c := range cs {
		out[i] = c.Id
	}
	return out
}
