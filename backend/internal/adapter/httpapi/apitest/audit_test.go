//go:build integration

package apitest_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

func auditIDs(items []gen.AuditEntry) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, e := range items {
		out[i] = e.Id
	}
	return out
}

// Proposed ID (not in spec 08 yet): T-A-59
// EC-100: the journal shows before and after of a change, the request id of
// the request that made it, the display names, and never a secret.
func TestAPIAuditBeforeAfterWithoutSecrets(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	requireStatus(t, admin.Patch(nodeURL(w.A1u1, ""), map[string]string{"name": "Renamed"}), http.StatusOK)
	const reqID = "req-ledger-audit-0001"
	op := mustOperation(t, admin.Post(nodeURL(w.A1u1, "/credits"), map[string]string{"amount": "100"},
		apitest.WithIdempotencyKey(newKey("au")), apitest.WithRequestID(reqID)))
	member := admin.Post(nodeURL(w.A1u1, "/members"),
		map[string]any{"email": "audited@example.test", "role": "viewer", "initialPassword": newPassword12})
	requireStatus(t, member, http.StatusCreated)

	res := admin.Get(api + "/audit?scopeNodeId=" + w.A1u1.String() + "&limit=200")
	requireStatus(t, res, http.StatusOK)
	page := decode[gen.AuditPage](t, res)

	byAction := map[gen.AuditAction][]gen.AuditEntry{}
	for _, e := range page.Items {
		byAction[e.Action] = append(byAction[e.Action], e)
	}
	renamed := byAction[gen.NodeRenamed]
	require.NotEmpty(t, renamed)
	assert.Equal(t, "A1u1", (*renamed[0].Before)["name"])
	assert.Equal(t, "Renamed", (*renamed[0].After)["name"])
	assert.Equal(t, "Renamed", renamed[0].NodeName, "the node name is the current one")
	assert.Equal(t, w.AdminA, renamed[0].ActorId)

	credited := byAction[gen.BalanceCredited]
	require.Len(t, credited, 1)
	e := credited[0]
	assert.Nil(t, e.Before)
	require.NotNil(t, e.After)
	assert.Equal(t, op.Id.String(), (*e.After)["operation_id"])
	assert.Equal(t, "100", (*e.After)["amount"])
	assert.Equal(t, "1200", (*e.After)["balance_after"])
	assert.Equal(t, reqID, e.RequestId)
	assert.Equal(t, gen.AuditTargetType("balance"), e.TargetType)
	assert.Equal(t, w.A1u1.String(), e.TargetId)
	require.NotNil(t, e.ActorEmail)
	assert.Equal(t, w.Emails[w.AdminA], *e.ActorEmail)
	assert.NotEmpty(t, byAction[gen.MemberGranted])

	// No secret anywhere in the journal, nor in any page of it.
	body := strings.ToLower(string(res.Body))
	for _, secret := range []string{newPassword12, "argon2", "$argon", "password", "hash", "token", "secret", "bearer"} {
		assert.NotContains(t, body, strings.ToLower(secret), "the journal leaks %q", secret)
	}
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(res.Body, &raw))
	for _, it := range raw.Items {
		for _, k := range []string{"id", "occurredAt", "actorId", "actorEmail", "nodeId", "nodeName", "action", "targetType", "targetId", "before", "after", "requestId"} {
			assert.Contains(t, it, k, "every field of the contract is present, null when empty")
		}
	}
}

// Proposed ID (not in spec 08 yet): T-A-60
// The audit list: filters, strict paging without repeats or gaps, the right
// needed on the scope, and the cursor bound to its listing (EC-62).
func TestAPIAuditListFiltersAndCursor(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	scope := "scopeNodeId=" + w.A.String()

	for range 5 {
		mustOperation(t, credit(admin, w.A1u1, "1", newKey("al")))
	}
	mustOperation(t, debit(admin, w.A1u2, "1", newKey("al")))
	all := auditPage(t, admin, scope+"&limit=200")
	require.GreaterOrEqual(t, len(all.Items), 8)
	for i := 1; i < len(all.Items); i++ {
		assert.False(t, all.Items[i].OccurredAt.After(all.Items[i-1].OccurredAt), "newest first")
	}
	for _, e := range all.Items {
		assert.NotContains(t, w.NodesB, e.NodeId, "no entry of the other branch")
		assert.NotEqual(t, w.AuditRoot, e.Id, "an entry above the reader's roots is invisible")
	}

	var walked []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20)
		q := scope + "&limit=3"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page := auditPage(t, admin, q)
		walked = append(walked, auditIDs(page.Items)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	assert.Equal(t, auditIDs(all.Items), walked, "no repeat and no gap")

	credited := auditPage(t, admin, scope+"&limit=200&action=balance.credited")
	assert.Len(t, credited.Items, 5)
	for _, e := range credited.Items {
		assert.Equal(t, gen.BalanceCredited, e.Action)
	}
	byNode := auditPage(t, admin, scope+"&limit=200&nodeId="+w.A1u2.String())
	require.NotEmpty(t, byNode.Items)
	for _, e := range byNode.Items {
		assert.Equal(t, w.A1u2, e.NodeId)
	}
	assert.Empty(t, auditPage(t, admin, scope+"&limit=200&actorId="+w.OwnerRoot.String()).Items)
	assert.Empty(t, auditPage(t, admin, scope+"&limit=200&from="+url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))).Items)
	assert.Len(t, auditPage(t, admin, scope+"&limit=200&from=2000-01-01T00:00:00Z&to="+
		url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))).Items, len(all.Items))

	admin.Get(api+"/audit?"+scope+"&action=node.exploded").RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(api+"/audit?"+scope+"&from=2030-01-01T00:00:00Z&to=2020-01-01T00:00:00Z").RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(api+"/audit").RequireProblem(http.StatusBadRequest, "validation_failed")

	// audit.read is needed on the scope: a viewer sees the node and gets 403.
	s.As(w.ViewerA1u1).Get(api+"/audit?scopeNodeId="+w.A1u1.String()).RequireProblem(http.StatusForbidden, "forbidden")
	s.Anon.Get(api+"/audit?"+scope).RequireProblem(http.StatusUnauthorized, "token_missing")

	// The cursor belongs to its listing.
	first := auditPage(t, admin, scope+"&limit=2")
	require.NotNil(t, first.NextCursor)
	c := url.QueryEscape(*first.NextCursor)
	for name, q := range map[string]string{
		"other scope":  "scopeNodeId=" + w.A1.String() + "&limit=2",
		"other action": scope + "&action=balance.credited&limit=2",
		"other node":   scope + "&nodeId=" + w.A1u1.String() + "&limit=2",
		"other actor":  scope + "&actorId=" + w.AdminA.String() + "&limit=2",
		"other period": scope + "&from=2000-01-01T00:00:00Z&limit=2",
		"foreign":      "scopeNodeId=" + w.B.String() + "&limit=2",
	} {
		res := admin.Get(api + "/audit?" + q + "&cursor=" + c)
		require.Equal(t, http.StatusBadRequest, res.Status, "%s: %s", name, res.Body)
		assert.Equal(t, "invalid_cursor", res.Problem().Code, name)
	}
	admin.Get(api+"/audit?"+scope+"&cursor="+c+"x").RequireProblem(http.StatusBadRequest, "invalid_cursor")
	// A reader with the same rights but another identity cannot continue it.
	s.As(w.OwnerRoot).Get(api+"/audit?"+scope+"&limit=2&cursor="+c).RequireProblem(http.StatusBadRequest, "invalid_cursor")
}

// Test-ID: T-A-15
// EC-99: refused requests (403, 404, 409, 422) write nothing to the journal and
// leave a log record with the request id and the code; a 404 says only that the
// node is missing or invisible.
func TestAPIFailuresNotAuditedButLogged(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin, viewer := s.As(w.AdminA), s.As(w.ViewerA1u1)
	before := stateOf(t, s)

	cases := []struct {
		name   string
		res    *apitest.Response
		status int
		code   string
	}{
		{"forbidden", credit(viewer, w.A1u1, "1", newKey("l")), http.StatusForbidden, "forbidden"},
		{"not found", debit(admin, w.B1u1, "1", newKey("l")), http.StatusNotFound, "not_found"},
		{"insufficient", debit(admin, w.A1u1, "2000", newKey("l")), http.StatusConflict, "insufficient_funds"},
		{"over limit", credit(admin, w.A1u1, "5001", newKey("l")), http.StatusUnprocessableEntity, "amount_exceeds_limit"},
	}
	for _, c := range cases {
		p := c.res.RequireProblem(c.status, c.code)
		rec := refusalLog(t, s, p.RequestID)
		require.NotNil(t, rec, "%s: a log record with the request id", c.name)
		assert.Equal(t, c.code, rec["code"], c.name)
		assert.EqualValues(t, c.status, rec["status"], c.name)
		if c.status == http.StatusNotFound {
			assert.NotEmpty(t, rec["reason"], "a 404 log record carries a reason")
		} else {
			assert.NotContains(t, rec, "reason", c.name)
		}
	}
	assert.Equal(t, before, stateOf(t, s), "no refusal wrote an audit entry or anything else")
}
