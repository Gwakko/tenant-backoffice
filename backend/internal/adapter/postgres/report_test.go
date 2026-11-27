//go:build integration

package postgres_test

import (
	"context"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	rep "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

// sixtyDays writes the ledger of two months over the four units of the world:
// each day a credit and a debit per unit, a transfer inside A1 every 4th day
// (near midnight UTC), a transfer from A1 to B1 every 6th day, and a credit at
// exactly 21:00 UTC every 9th day (a local midnight in Kyiv in summer time).
func sixtyDays(l *ledger) {
	start := day("2026-03-01")
	units := []string{"A1u1", "A1u2", "B1u1", "B1u2"}
	for d := 0; d < 60; d++ {
		at := start.AddDate(0, 0, d)
		for i, u := range units {
			l.credit(u, int64(20000+d*7+i), at.Add(10*time.Hour+time.Duration(i)*time.Minute))
			l.debit(u, int64(3000+d), at.Add(15*time.Hour+30*time.Minute))
		}
		if d%4 == 0 {
			l.transfer("A1u1", "A1u2", 500, at.Add(23*time.Hour+30*time.Minute))
		}
		if d%6 == 0 {
			l.transfer("A1u2", "B1u1", 700, at.Add(15*time.Minute))
		}
		if d%9 == 0 {
			l.credit("A1u1", 1, at.Add(21*time.Hour))
		}
	}
}

var winFull = period{from: "2026-03-01", to: "2026-05-01"}

// The report equals an independent model for several scopes, zones,
// granularities, windows and kind filters on two months of data.
func TestReportMatchesModel(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()

	for _, scope := range []string{"root", "A", "A1", "A1u1", "B1"} {
		for _, tz := range []string{"UTC", "Europe/Kyiv", "America/Los_Angeles", "Asia/Kolkata"} {
			for _, gran := range []string{"day", "week", "month"} {
				for _, win := range [][2]string{{"2026-03-01", "2026-05-01"}, {"2026-03-15", "2026-04-12"}} {
					c.requireSummary(l, "ownerRoot", scope, period{from: win[0], to: win[1], tz: tz, gran: gran})
				}
			}
		}
	}
	for _, kinds := range [][]string{{"credit"}, {"transfer_in", "transfer_out"}, {"debit", "transfer_out"}} {
		for _, scope := range []string{"root", "A1"} {
			p := winFull
			p.tz, p.gran, p.kinds = "Europe/Kyiv", "week", kinds
			c.requireSummary(l, "ownerRoot", scope, p)
		}
	}
}

// Test-ID: T-I-29
// EC-85: a move or a new operation committed while a report is being built is
// either fully seen or not seen at all: series and totals of one report agree,
// and children and summary read in one transaction agree.
func TestReportSnapshotDuringMove(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()
	uc := w.useCases()
	p := period{from: "2026-03-01", to: "2026-05-01", tz: "UTC", gran: "week"}

	before, _ := l.expect(t, "A1", p)

	// The use case: unit A1u1 is moved out of A1 between the series and the
	// totals query of the same report.
	moved := false
	sum, _ := w.reportCasesWith(hookRepo{ReportRepo: c.repo, afterSeries: func() {
		require.NoError(t, w.moveAs(uc, "ownerRoot", "A1u1", "B1"))
		l.move("A1u1", "B1")
		moved = true
	}})
	out, err := sum.Execute(context.Background(), summaryInput(w, "ownerRoot", "A1", p))
	require.NoError(t, err)
	require.True(t, moved)
	require.Equal(t, before, out.Totals, "the whole report shows the tree before the move")
	requireSeriesAddsUp(t, out.Totals, out.Series)

	// A report after the commit shows the new tree.
	after := c.requireSummary(l, "ownerRoot", "A1", p)
	require.NotEqual(t, before, after.Totals)

	// A new operation committed meanwhile is not seen by the report under way.
	pre, _ := l.expect(t, "A1", p)
	sum, _ = w.reportCasesWith(hookRepo{ReportRepo: c.repo, afterSeries: func() {
		l.credit("A1u2", 123456, day("2026-03-10").Add(12*time.Hour))
	}})
	out, err = sum.Execute(context.Background(), summaryInput(w, "ownerRoot", "A1", p))
	require.NoError(t, err)
	require.Equal(t, pre, out.Totals)
	requireSeriesAddsUp(t, out.Totals, out.Series)
	c.requireSummary(l, "ownerRoot", "A1", p)
}

// EC-85 as the spec words it: a second query of the same transaction after a
// concurrent move committed still sees the tree before the move; the sum over
// the children equals the total.
func TestReportSnapshotChildrenThenSummary(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()
	uc := w.useCases()
	p := period{from: "2026-03-01", to: "2026-05-01", tz: "UTC", gran: "month"}
	win := drep.Period{Range: drep.Range{From: "2026-03-01", To: "2026-05-01"}, TZ: "UTC", Granularity: drep.Month}

	var children []port.ChildReportRow
	var totals drep.Totals
	err := w.runner.InTx(context.Background(), w.users["ownerRoot"], port.TxOptions{ReadOnly: true, Isolation: port.RepeatableRead},
		func(ctx context.Context, tx port.Tx) error {
			var err error
			children, err = c.repo.Children(ctx, tx, port.ChildrenQuery{Scope: w.id("A1"), Period: win, IncludeInactive: true, Limit: 10})
			require.NoError(t, err)
			require.NoError(t, w.moveAs(uc, "ownerRoot", "A1u1", "B1"))
			totals, err = c.repo.Totals(ctx, tx, port.SeriesQuery{Scope: w.id("A1"), Period: win})
			return err
		})
	require.NoError(t, err)
	require.Len(t, children, 2)
	var sum big.Int
	for _, ch := range children {
		v, _ := new(big.Int).SetString(ch.Totals.Credits.String(), 10)
		sum.Add(&sum, v)
	}
	require.Equal(t, totals.Credits.String(), sum.String(), "children and total come from one snapshot")

	// The next transaction sees the move.
	l.move("A1u1", "B1")
	c.requireSummary(l, "ownerRoot", "A1", p)
}

func summaryInput(w *world, who, scope string, p period) rep.SubtreeSummaryInput {
	return rep.SubtreeSummaryInput{
		Actor: w.users[who], NodeID: w.id(scope), From: p.from, To: p.to, TZ: p.tz, Granularity: p.gran, Kinds: p.kinds,
	}
}

func requireSeriesAddsUp(t *testing.T, totals drep.Totals, series []drep.PeriodTotals) {
	t.Helper()
	var count int64
	credits, debits := new(big.Int), new(big.Int)
	for _, s := range series {
		count += s.Count
		cv, _ := new(big.Int).SetString(s.Credits.String(), 10)
		dv, _ := new(big.Int).SetString(s.Debits.String(), 10)
		credits.Add(credits, cv)
		debits.Add(debits, dv)
	}
	require.Equal(t, totals.Count, count)
	require.Equal(t, totals.Credits.String(), credits.String())
	require.Equal(t, totals.Debits.String(), debits.String())
}

// Test-ID: T-I-30
// EC-86: an inactive child is in the report with active=false; includeInactive
// =false leaves it out; a child of an inactive scope is not active either.
func TestInactiveChildInReport(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()
	p := period{from: "2026-03-01", to: "2026-05-01", tz: "UTC"}

	w.setStatus("A1u2", "inactive")
	out, err := c.childrenOf("ownerRoot", "A1", p, true, 50, "")
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	active := map[string]bool{}
	counts := map[string]int64{}
	for _, it := range out.Items {
		active[it.Name] = it.Active
		counts[it.Name] = it.Totals.Count
	}
	require.Equal(t, map[string]bool{"A1u1": true, "A1u2": false}, active)
	require.Positive(t, counts["A1u2"], "an inactive node keeps its history in the report")

	out, err = c.childrenOf("ownerRoot", "A1", p, false, 50, "")
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	require.Equal(t, "A1u1", out.Items[0].Name)

	// The inactive node still counts in the summary of its ancestor.
	c.requireSummary(l, "ownerRoot", "A1", period{from: "2026-03-01", to: "2026-05-01", tz: "UTC", gran: "week"})

	// Children of an inactive scope are not effectively active.
	w.setStatus("A1u2", "active")
	w.setStatus("A1", "inactive")
	out, err = c.childrenOf("ownerRoot", "A1", p, true, 50, "")
	require.NoError(t, err)
	for _, it := range out.Items {
		require.False(t, it.Active, it.Name)
	}
	// includeInactive=false filters by the node's own status: the children stay.
	out, err = c.childrenOf("ownerRoot", "A1", p, false, 50, "")
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
}

// Test-ID: T-I-31
// EC-87: an empty subtree and empty periods give a series of zeros without
// gaps, a node without children an empty page.
func TestEmptyPeriodsZeroFilled(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	l.credit("A1u1", 100, day("2026-06-15").Add(12*time.Hour))
	c := w.reportCases()

	// B1u2 has nothing at all.
	for _, gran := range []string{"day", "week", "month"} {
		out := c.requireSummary(l, "ownerRoot", "B1u2", period{from: "2026-05-27", to: "2026-07-03", tz: "UTC", gran: gran})
		require.NotEmpty(t, out.Series)
		for _, s := range out.Series {
			require.Equal(t, drep.Totals{Count: 0, Credits: "0", Debits: "0", Net: "0"}, s.Totals)
		}
		require.Equal(t, drep.Totals{Count: 0, Credits: "0", Debits: "0", Net: "0"}, out.Totals)
	}
	// One operation in a long empty window: every day is present.
	out := c.requireSummary(l, "ownerRoot", "A1", period{from: "2026-06-01", to: "2026-07-01", tz: "Europe/Kyiv", gran: "day"})
	require.Len(t, out.Series, 30)
	require.Equal(t, int64(1), out.Totals.Count)
	// A single day window.
	out = c.requireSummary(l, "ownerRoot", "A1", period{from: "2026-06-15", to: "2026-06-16", tz: "UTC", gran: "month"})
	require.Len(t, out.Series, 1)
	require.Equal(t, drep.Date("2026-06-01"), out.Series[0].PeriodStart)

	page, err := c.childrenOf("ownerRoot", "B1u2", period{from: "2026-06-01", to: "2026-07-01", tz: "UTC"}, true, 50, "")
	require.NoError(t, err)
	require.NotNil(t, page.Items)
	require.Empty(t, page.Items)
	require.Empty(t, page.NextCursor)

	// Children without operations are listed with zeros.
	page, err = c.childrenOf("ownerRoot", "B1", period{from: "2026-06-01", to: "2026-07-01", tz: "UTC"}, true, 50, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, drep.Sum("0"), page.Items[0].Balance)
}

// The effective display_timezone of the node is the default zone.
func TestDefaultZoneFromSettings(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	l.credit("A1u1", 5, day("2026-06-10").Add(22*time.Hour+30*time.Minute))
	c := w.reportCases()
	p := period{from: "2026-06-09", to: "2026-06-13", gran: "day"}

	out, err := c.summaryOf("ownerRoot", "A1", p)
	require.NoError(t, err)
	require.Equal(t, "UTC", out.TZ)
	require.Equal(t, int64(1), out.Series[1].Count, "2026-06-10 in UTC")

	w.seedLimit("A", "display_timezone", `"Europe/Kyiv"`)
	out, err = c.summaryOf("ownerRoot", "A1", p)
	require.NoError(t, err)
	require.Equal(t, "Europe/Kyiv", out.TZ)
	require.Equal(t, int64(1), out.Series[2].Count, "2026-06-11 in Kyiv")

	w.seedLimit("A1", "display_timezone", `"Asia/Tokyo"`)
	out, err = c.summaryOf("ownerRoot", "A1u1", p)
	require.NoError(t, err)
	require.Equal(t, "Asia/Tokyo", out.TZ, "the nearest override wins")

	p.tz = "America/New_York"
	out, err = c.summaryOf("ownerRoot", "A1u1", p)
	require.NoError(t, err)
	require.Equal(t, "America/New_York", out.TZ, "a requested zone beats the setting")
}

// Test-ID: T-I-34
// EC-91: sums beyond int64 are exact decimal strings.
func TestSumBeyondInt64(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	ctx := context.Background()
	w.addNode(ctx, "A1u3", dnode.TypeUnit, "A1")
	l.parent["A1u3"] = "A1"
	at := day("2026-06-10").Add(12 * time.Hour)
	half := int64(math.MaxInt64 / 2)
	for _, u := range []string{"A1u1", "A1u2", "A1u3"} {
		l.credit(u, half, at)
	}
	c := w.reportCases()
	want := new(big.Int).Mul(big.NewInt(half), big.NewInt(3)).String()
	require.Equal(t, "13835058055282163709", want)
	v, _ := new(big.Int).SetString(want, 10)
	require.Equal(t, 1, v.Cmp(big.NewInt(math.MaxInt64)), "the figure is above int64")

	p := period{from: "2026-06-01", to: "2026-07-01", tz: "UTC", gran: "month"}
	out, err := c.summaryOf("ownerRoot", "root", p)
	require.NoError(t, err)
	require.Equal(t, drep.Totals{Count: 3, Credits: drep.Sum(want), Debits: "0", Net: drep.Sum(want)}, out.Totals)
	require.Equal(t, out.Totals, out.Series[0].Totals)

	page, err := c.childrenOf("ownerRoot", "A", p, true, 10, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, drep.Sum(want), page.Items[0].Totals.Credits)
	require.Equal(t, drep.Sum(want), page.Items[0].Balance, "the balance of a subtree is above int64 too")

	// Debits on the next day push net below zero: the sign survives and the
	// figure is below the int64 minimum as well.
	next := at.Add(24 * time.Hour)
	for _, u := range []string{"A1u1", "A1u2", "A1u3"} {
		l.debit(u, half, next)
	}
	neg := period{from: "2026-06-11", to: "2026-06-12", tz: "UTC", gran: "day"}
	out = c.requireSummary(l, "ownerRoot", "A1", neg)
	require.Equal(t, drep.Sum("-"+want), out.Totals.Net)
	require.Equal(t, drep.Sum(want), out.Totals.Debits)
	out = c.requireSummary(l, "ownerRoot", "A1", p)
	require.Equal(t, drep.Sum("0"), out.Totals.Net)
}

// Test-ID: T-I-36
// EC-95: operations are attributed by the current place of the node: after a
// move the old ones count in the new ancestors and leave the old ones.
func TestAttributionFollowsMove(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	ctx := context.Background()
	w.addNode(ctx, "P1", dnode.TypePartner, "root")
	w.addNode(ctx, "P2", dnode.TypePartner, "root")
	w.addNode(ctx, "C", dnode.TypeCompany, "P1")
	w.addNode(ctx, "Cu", dnode.TypeUnit, "C")
	l.parent["P1"], l.parent["P2"], l.parent["C"], l.parent["Cu"] = "root", "root", "P1", "C"
	l.credit("Cu", 1000, day("2026-06-02").Add(9*time.Hour))
	l.debit("Cu", 300, day("2026-06-03").Add(9*time.Hour))
	c := w.reportCases()
	uc := w.useCases()
	p := period{from: "2026-06-01", to: "2026-07-01", tz: "UTC", gran: "week"}

	require.Equal(t, int64(2), c.requireSummary(l, "ownerRoot", "P1", p).Totals.Count)
	require.Equal(t, int64(0), c.requireSummary(l, "ownerRoot", "P2", p).Totals.Count)

	require.NoError(t, w.moveAs(uc, "ownerRoot", "C", "P2"))
	l.move("C", "P2")
	require.Equal(t, int64(0), c.requireSummary(l, "ownerRoot", "P1", p).Totals.Count)
	out := c.requireSummary(l, "ownerRoot", "P2", p)
	require.Equal(t, drep.Totals{Count: 2, Credits: "1000", Debits: "300", Net: "700"}, out.Totals)

	page, err := c.childrenOf("ownerRoot", "P2", p, true, 10, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, drep.Sum("700"), page.Items[0].Balance)
}

// Transfers inside the scope count on both sides and leave net unchanged; a
// transfer across the border of the scope counts one leg.
func TestTransfersInsideAndAcrossScope(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	at := day("2026-06-10").Add(12 * time.Hour)
	l.credit("A1u1", 10000, at)
	l.credit("B1u1", 10000, at)
	l.transfer("A1u1", "A1u2", 400, at.Add(time.Hour))
	l.transfer("A1u1", "B1u1", 250, at.Add(2*time.Hour))
	c := w.reportCases()
	p := period{from: "2026-06-01", to: "2026-07-01", tz: "UTC", gran: "month"}

	// A1 holds both legs of the first transfer and the out leg of the second.
	out := c.requireSummary(l, "ownerRoot", "A1", p)
	require.Equal(t, drep.Totals{Count: 4, Credits: "10400", Debits: "650", Net: "9750"}, out.Totals)
	// Partner A and partner B: the second transfer is out for one, in for the other.
	a := c.requireSummary(l, "ownerRoot", "A", p).Totals
	b := c.requireSummary(l, "ownerRoot", "B", p).Totals
	require.Equal(t, drep.Sum("9750"), a.Net)
	require.Equal(t, drep.Sum("10250"), b.Net)
	// The whole tree: both legs of each transfer, net is the credits only.
	all := c.requireSummary(l, "ownerRoot", "root", p).Totals
	require.Equal(t, drep.Totals{Count: 6, Credits: "20650", Debits: "650", Net: "20000"}, all)

	// A single unit: one leg of the transfer inside A1.
	u1 := c.requireSummary(l, "ownerRoot", "A1u1", p).Totals
	require.Equal(t, drep.Sum("9350"), u1.Net)
	u2 := c.requireSummary(l, "ownerRoot", "A1u2", p).Totals
	require.Equal(t, drep.Totals{Count: 1, Credits: "400", Debits: "0", Net: "400"}, u2)
}

// A report counts the subtree of its node only: the operations of other
// branches are in no sum, no child row and no balance.
func TestReportCountsOnlyTheSubtree(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()
	p := period{from: "2026-03-01", to: "2026-05-01", tz: "UTC", gran: "month"}

	for _, scope := range []string{"A1", "A1u1", "B1u2"} {
		c.requireSummary(l, "ownerRoot", scope, p)
	}
	// Children of A: the only child is A1 and its figures are the subtree of A1.
	page, err := c.childrenOf("ownerRoot", "A", p, true, 50, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	totals, _ := l.expect(t, "A1", p)
	require.Equal(t, totals, page.Items[0].Totals)
	require.Equal(t, drep.Sum(w.sumBalances("A1u1", "A1u2")), page.Items[0].Balance)

	// The actor sees only his branch: adminA on A1 gets the same as the owner.
	c.requireSummary(l, "adminA", "A1", p)
	// multi is admin in A1 and only a viewer in B1: reports of A1 do not mix B1.
	c.requireSummary(l, "multi", "A1", p)
}

func (w *world) sumBalances(keys ...string) string {
	w.t.Helper()
	total := new(big.Int)
	for _, k := range keys {
		var v string
		require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
			`SELECT coalesce((SELECT amount FROM balances WHERE node_id = $1), 0)::text`, [16]byte(w.id(k))).Scan(&v))
		n, _ := new(big.Int).SetString(v, 10)
		total.Add(total, n)
	}
	return total.String()
}

// A node of another branch is not_found, the same answer as a node that does
// not exist; a role below analyst is forbidden.
func TestReportAccess(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	sixtyDays(l)
	c := w.reportCases()
	p := period{from: "2026-03-01", to: "2026-05-01", tz: "UTC", gran: "day"}

	_, err := c.summaryOf("adminA", "B1", p)
	require.ErrorIs(t, err, apperr.NotFoundError(""))
	_, err = c.childrenOf("adminA", "B1", p, true, 10, "")
	require.ErrorIs(t, err, apperr.NotFoundError(""))
	// The root is above adminA's branch: not visible either.
	_, err = c.summaryOf("adminA", "root", p)
	require.ErrorIs(t, err, apperr.NotFoundError(""))

	// multi is only a viewer in B1: the node is visible, report.read is missing.
	_, err = c.summaryOf("multi", "B1", p)
	require.ErrorIs(t, err, apperr.ForbiddenError(""))
	_, err = c.summaryOf("viewerA1u1", "A1u1", p)
	require.ErrorIs(t, err, apperr.ForbiddenError(""))

	// A missing node.
	missing := summaryInput(w, "adminA", "A1", p)
	missing.NodeID = dnode.ID(uuid.New())
	_, err = c.summary.Execute(context.Background(), missing)
	require.ErrorIs(t, err, apperr.NotFoundError(""))
}

// Test-ID: T-I-90
// EC-92 for UC-R2: the keyset of the children report is stable under inserts. T-I-35
// is the same property for the operations list.
func TestReportChildrenKeysetStableUnderInserts(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	ctx := context.Background()
	names := []string{"n1", "n3", "n5", "n7", "n9", "nb", "nd"}
	for _, n := range names {
		w.addNode(ctx, n, dnode.TypeUnit, "A1")
		l.parent[n] = "A1"
		l.credit(n, 100, day("2026-06-05").Add(8*time.Hour))
	}
	c := w.reportCases()
	p := period{from: "2026-06-01", to: "2026-07-01", tz: "UTC"}

	first, err := c.childrenOf("ownerRoot", "A1", p, true, 3, "")
	require.NoError(t, err)
	require.Len(t, first.Items, 3)
	require.NotEmpty(t, first.NextCursor)

	// Five inserts: before the cursor, after it, and with operations.
	// A1u1, A1u2 and n1 are the first page; n0 and a0 sort before the cursor.
	for _, n := range []string{"a0", "n0", "n2", "n4", "nc", "ne"} {
		w.addNode(ctx, n, dnode.TypeUnit, "A1")
		l.parent[n] = "A1"
		l.credit(n, 7, day("2026-06-06").Add(8*time.Hour))
	}

	seen := map[string]int{}
	for _, it := range first.Items {
		seen[it.Name]++
	}
	cursor := first.NextCursor
	for cursor != "" {
		page, err := c.childrenOf("ownerRoot", "A1", p, true, 3, cursor)
		require.NoError(t, err)
		for _, it := range page.Items {
			seen[it.Name]++
		}
		cursor = page.NextCursor
	}
	for _, n := range names {
		require.Equal(t, 1, seen[n], "%s is on exactly one page", n)
	}
	for n, k := range seen {
		require.Equal(t, 1, k, "%s has no duplicate", n)
	}
	// Inserts after the cursor show up, those before it do not come back.
	for _, n := range []string{"n2", "n4", "nc", "ne"} {
		require.Equal(t, 1, seen[n], n)
	}
	require.Zero(t, seen["n0"])
	require.Zero(t, seen["a0"])
}

// Kinds filter and the kind set of the operations.
func TestReportKindFilter(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	at := day("2026-06-10").Add(12 * time.Hour)
	l.credit("A1u1", 1000, at)
	l.debit("A1u1", 100, at)
	l.transfer("A1u1", "A1u2", 50, at)
	c := w.reportCases()
	p := period{from: "2026-06-01", to: "2026-07-01", tz: "UTC", gran: "month"}
	for _, kinds := range [][]string{nil, {"credit"}, {"debit"}, {"transfer_in"}, {"transfer_out"}, {"credit", "transfer_in"}} {
		q := p
		q.kinds = kinds
		c.requireSummary(l, "ownerRoot", "A1", q)
	}
}
