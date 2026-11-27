//go:build integration

package apitest_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// reportWindow is [yesterday, day after tomorrow) in UTC: three days that hold
// the operations the World wrote "now", whatever the time of day.
func reportWindow() (from, to string) {
	now := time.Now().UTC()
	return now.AddDate(0, 0, -1).Format(time.DateOnly), now.AddDate(0, 0, 2).Format(time.DateOnly)
}

// reportURL is the path of a report of a node with query parameters.
func reportURL(node uuid.UUID, kind string, q url.Values) string {
	return nodeURL(node, "/reports/"+kind+"?"+q.Encode())
}

func windowQuery(extra ...string) url.Values {
	from, to := reportWindow()
	q := url.Values{"from": {from}, "to": {to}, "tz": {"UTC"}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return q
}

// Per branch the World holds: credit 1000 on the first unit, credit 600 on the
// second, a transfer of 100 from the second to the first (both legs).
// So a company has 4 operations: credits 1700, debits 100, net 1600.

// Proposed ID (not in spec 08 yet): T-A-60, the summary report over HTTP, shape and numbers.
func TestAPIReportSummary(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	analyst := s.As(w.AnalystA)

	res := analyst.Get(reportURL(w.A1, "summary", windowQuery("granularity", "day")))
	requireStatus(t, res, http.StatusOK)
	sum := decode[gen.SubtreeSummary](t, res)
	require.Equal(t, w.A1, sum.ScopeNodeId)
	require.Equal(t, "UTC", sum.Tz)
	require.Equal(t, gen.Totals{Count: 4, Credits: "1700", Debits: "100", Net: "1600"}, sum.Totals)
	require.Len(t, sum.Series, 3, "one entry per day, empty days included")

	var count int64
	for _, p := range sum.Series {
		count += p.Count
	}
	require.Equal(t, int64(4), count)

	// Sums are JSON strings, counts numbers, dates plain dates.
	raw := decode[map[string]any](t, res)
	totals := raw["totals"].(map[string]any)
	require.Equal(t, "1700", totals["credits"])
	require.Equal(t, float64(4), totals["count"])
	for _, p := range raw["series"].([]any) {
		e := p.(map[string]any)
		require.Regexp(t, regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`), e["periodStart"])
		for _, f := range []string{"credits", "debits", "net"} {
			require.IsType(t, "", e[f], f)
		}
	}

	// Without a zone the effective display_timezone of the node is used; the
	// default granularity is day.
	res = analyst.Get(reportURL(w.A1, "summary", url.Values{"from": {windowQuery().Get("from")}, "to": {windowQuery().Get("to")}}))
	requireStatus(t, res, http.StatusOK)
	require.Len(t, decode[gen.SubtreeSummary](t, res).Series, 3)

	// A kind filter.
	res = analyst.Get(reportURL(w.A1, "summary", func() url.Values {
		q := windowQuery("granularity", "month")
		q.Add("kind", "transfer_in")
		q.Add("kind", "transfer_out")
		return q
	}()))
	requireStatus(t, res, http.StatusOK)
	require.Equal(t, gen.Totals{Count: 2, Credits: "100", Debits: "100", Net: "0"}, decode[gen.SubtreeSummary](t, res).Totals,
		"a transfer inside the subtree counts on both sides and leaves net unchanged")

	// A unit: its own operations only.
	res = analyst.Get(reportURL(w.A1u2, "summary", windowQuery()))
	requireStatus(t, res, http.StatusOK)
	require.Equal(t, gen.Totals{Count: 2, Credits: "600", Debits: "100", Net: "500"}, decode[gen.SubtreeSummary](t, res).Totals)
}

// Proposed ID (not in spec 08 yet): T-A-61, the children report over HTTP, shape,
// balances and the cursor.
func TestAPIReportChildren(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	res := admin.Get(reportURL(w.A1, "children", windowQuery()))
	requireStatus(t, res, http.StatusOK)
	page := decode[gen.ChildTotalsPage](t, res)
	require.Nil(t, page.NextCursor)
	require.Len(t, page.Items, 2)
	require.Equal(t, gen.ChildTotals{
		NodeId: w.A1u1, Name: "A1u1", Type: gen.NodeType("unit"), Active: true,
		Count: 2, Credits: "1100", Debits: "0", Net: "1100", Balance: "1100",
	}, page.Items[0])
	require.Equal(t, gen.ChildTotals{
		NodeId: w.A1u2, Name: "A1u2", Type: gen.NodeType("unit"), Active: true,
		Count: 2, Credits: "600", Debits: "100", Net: "500", Balance: "500",
	}, page.Items[1])

	// A partner: its only child is the company with the totals of the subtree.
	page = decode[gen.ChildTotalsPage](t, admin.Get(reportURL(w.A, "children", windowQuery())))
	require.Len(t, page.Items, 1)
	require.Equal(t, gen.Totals{Count: 4, Credits: "1700", Debits: "100", Net: "1600"},
		gen.Totals{Count: page.Items[0].Count, Credits: page.Items[0].Credits, Debits: page.Items[0].Debits, Net: page.Items[0].Net})
	require.Equal(t, "1600", page.Items[0].Balance)

	// A unit has no children: an empty page, not null.
	res = admin.Get(reportURL(w.A1u1, "children", windowQuery()))
	requireStatus(t, res, http.StatusOK)
	require.JSONEq(t, `{"items":[],"nextCursor":null}`, string(res.Body))

	// Inactive children: listed with active=false, or left out.
	requireStatus(t, s.As(w.OwnerRoot).Post(nodeURL(w.A1u2, "/deactivate"), nil), http.StatusOK)
	page = decode[gen.ChildTotalsPage](t, admin.Get(reportURL(w.A1, "children", windowQuery())))
	require.Len(t, page.Items, 2)
	require.True(t, page.Items[0].Active)
	require.False(t, page.Items[1].Active)
	page = decode[gen.ChildTotalsPage](t, admin.Get(reportURL(w.A1, "children", windowQuery("includeInactive", "false"))))
	require.Len(t, page.Items, 1)
	require.Equal(t, "A1u1", page.Items[0].Name)

	// The cursor: one child per page, valid only for the same request.
	q := windowQuery("limit", "1")
	first := decode[gen.ChildTotalsPage](t, admin.Get(reportURL(w.A1, "children", q)))
	require.Len(t, first.Items, 1)
	require.NotNil(t, first.NextCursor)
	q.Set("cursor", *first.NextCursor)
	second := decode[gen.ChildTotalsPage](t, admin.Get(reportURL(w.A1, "children", q)))
	require.Len(t, second.Items, 1)
	require.Equal(t, "A1u2", second.Items[0].Name)
	require.Nil(t, second.NextCursor)

	bad := func(mod func(url.Values)) {
		v := windowQuery("limit", "1", "cursor", *first.NextCursor)
		mod(v)
		admin.Get(reportURL(w.A1, "children", v)).RequireProblem(http.StatusBadRequest, "invalid_cursor")
	}
	bad(func(v url.Values) { v.Set("cursor", *first.NextCursor+"x") })
	bad(func(v url.Values) { v.Set("tz", "Europe/Kyiv") })
	bad(func(v url.Values) { v.Set("from", time.Now().UTC().AddDate(0, 0, -2).Format(time.DateOnly)) })
	bad(func(v url.Values) { v.Set("includeInactive", "false") })
	admin.Get(reportURL(w.A, "children", windowQuery("limit", "1", "cursor", *first.NextCursor))).
		RequireProblem(http.StatusBadRequest, "invalid_cursor")
}

// Proposed ID (not in spec 08 yet): T-A-62, EC-90 and the parameter errors of the
// reports over HTTP.
func TestAPIReportInvalidPeriod(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	base := func(kv ...string) url.Values {
		q := url.Values{"from": {"2026-06-01"}, "to": {"2026-07-01"}, "tz": {"UTC"}, "granularity": {"day"}}
		for i := 0; i+1 < len(kv); i += 2 {
			q.Set(kv[i], kv[i+1])
		}
		return q
	}

	period := map[string]url.Values{
		"from equals to":   base("to", "2026-06-01"),
		"from after to":    base("from", "2026-07-02"),
		"367 days":         base("from", "2025-01-01", "to", "2026-01-03"),
		"granularity year": base("granularity", "year"),
		"unknown zone":     base("tz", "Foo/Bar"),
		"zone Local":       base("tz", "Local"),
		"not a real date":  base("from", "2026-02-30"),
		"date with time":   base("to", "2026-07-01T00:00:00Z"),
		"short date":       base("to", "2026-7-1"),
		"empty from":       base("from", ""),
		"path-like zone":   base("tz", "../etc/passwd"),
	}
	for name, q := range period {
		admin.Get(reportURL(w.A1, "summary", q)).RequireProblem(http.StatusBadRequest, "invalid_period")
		if name != "granularity year" {
			qc := url.Values{"from": q["from"], "to": q["to"], "tz": q["tz"]}
			admin.Get(reportURL(w.A1, "children", qc)).RequireProblem(http.StatusBadRequest, "invalid_period")
		}
	}
	// 366 days is allowed.
	requireStatus(t, admin.Get(reportURL(w.A1, "summary", base("from", "2024-01-01", "to", "2025-01-01"))), http.StatusOK)

	// Missing parameters and wrong filters are validation errors.
	admin.Get(nodeURL(w.A1, "/reports/summary?to=2026-07-01")).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(nodeURL(w.A1, "/reports/children?from=2026-06-01")).RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Get(reportURL(w.A1, "summary", func() url.Values { q := base(); q.Set("kind", "refund"); return q }())).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	for _, limit := range []string{"0", "-1", "201", "abc"} {
		admin.Get(reportURL(w.A1, "children", base("limit", limit))).RequireProblem(http.StatusBadRequest, "validation_failed")
	}
	admin.Get(nodeURL(uuid.New(), "/reports/summary?from=2026-06-01&to=2026-07-01")).RequireProblem(http.StatusNotFound, "not_found")
	admin.Get(api+"/nodes/not-a-uuid/reports/summary?from=2026-06-01&to=2026-07-01").RequireProblem(http.StatusBadRequest, "invalid_id")
	s.Anon.Get(reportURL(w.A1, "summary", base())).RequireProblem(http.StatusUnauthorized, "token_missing")
}

// Test-ID: T-ISO-06
// EC-51, EC-32: a report of another branch is 404, byte for byte like a node that
// does not exist; a report never contains the data of another branch (HTTP
// variant; the use-case variant of T-ISO-06 belongs to the isolation suite).
func TestAPIReportForeignBranch(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	ctx := context.Background()
	// Make the branches distinguishable: one more credit in each, different sizes.
	w.AddOperation(ctx, t, w.AdminA, w.A1u1, "credit", 77, 1177, nil, nil)
	w.AddOperation(ctx, t, w.AdminB, w.B1u1, "credit", 999999, 1000999, nil, nil)

	adminA, adminB := s.As(w.AdminA), s.As(w.AdminB)
	q := windowQuery()

	for _, kind := range []string{"summary", "children"} {
		foreign := adminA.Get(reportURL(w.B1, kind, q))
		missing := adminA.Get(reportURL(uuid.New(), kind, q))
		require.Equal(t, http.StatusNotFound, foreign.Status, kind)
		require.Equal(t, missing.BodyWithoutRequestID(), foreign.BodyWithoutRequestID(), "%s: foreign equals missing", kind)
		// Above the branch (the common ancestor without a role) is the same 404.
		root := adminA.Get(reportURL(w.Root, kind, q))
		require.Equal(t, missing.BodyWithoutRequestID(), root.BodyWithoutRequestID(), "%s: the root", kind)
	}

	// Own branch: only own figures.
	sumA := decode[gen.SubtreeSummary](t, adminA.Get(reportURL(w.A, "summary", q)))
	require.Equal(t, gen.Totals{Count: 5, Credits: "1777", Debits: "100", Net: "1677"}, sumA.Totals)
	sumB := decode[gen.SubtreeSummary](t, adminB.Get(reportURL(w.B, "summary", q)))
	require.Equal(t, gen.Totals{Count: 5, Credits: "1001699", Debits: "100", Net: "1001599"}, sumB.Totals)

	// The owner of the root sees both branches separately and the sum.
	owner := s.As(w.OwnerRoot)
	all := decode[gen.SubtreeSummary](t, owner.Get(reportURL(w.Root, "summary", q)))
	require.Equal(t, gen.Totals{Count: 10, Credits: "1003476", Debits: "200", Net: "1003276"}, all.Totals)
	kids := decode[gen.ChildTotalsPage](t, owner.Get(reportURL(w.Root, "children", q)))
	require.Len(t, kids.Items, 2)
	require.Equal(t, "1677", kids.Items[0].Net)
	require.Equal(t, "1001599", kids.Items[1].Net)

	// A user with roles in two branches gets the requested scope only; where the
	// role is too low for reports the answer is forbidden, not a mix of data.
	multi := s.As(w.Multi)
	own := decode[gen.SubtreeSummary](t, multi.Get(reportURL(w.A1, "summary", q)))
	require.Equal(t, sumA.Totals, own.Totals)
	multi.Get(reportURL(w.B1, "summary", q)).RequireProblem(http.StatusForbidden, "forbidden")
	// A role on a unit below does not open the company above.
	s.As(w.ViewerA1u1).Get(reportURL(w.A1u1, "summary", q)).RequireProblem(http.StatusForbidden, "forbidden")
	s.As(w.ViewerA1u1).Get(reportURL(w.A1, "summary", q)).RequireProblem(http.StatusNotFound, "not_found")
	s.As(w.AnalystA).Get(reportURL(w.A, "summary", q)).RequireProblem(http.StatusNotFound, "not_found")
}
