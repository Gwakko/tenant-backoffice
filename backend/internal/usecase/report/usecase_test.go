package report_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

// A report is one REPEATABLE READ READ ONLY transaction (Р-16, EC-85).
func TestReportsRunInSnapshotTransaction(t *testing.T) {
	f := newFakes()
	_, err := uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(nil))
	require.NoError(t, err)
	require.Equal(t, port.TxOptions{ReadOnly: true, Isolation: port.RepeatableRead}, f.runner.last)
	require.Equal(t, 1, f.runner.opened, "series and totals share one transaction")

	_, err = uc.NewChildrenBreakdown(f.deps()).Execute(context.Background(), childrenIn(nil))
	require.NoError(t, err)
	require.Equal(t, port.TxOptions{ReadOnly: true, Isolation: port.RepeatableRead}, f.runner.last)
}

// The right is report.read on the node; no role is not_found, a role that is
// too low is forbidden, and the store is not asked in either case.
func TestReportsRequireReportRead(t *testing.T) {
	f := newFakes()
	f.checker.role = role.Viewer
	_, err := uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(nil))
	require.ErrorIs(t, err, apperr.ForbiddenError(""))
	_, err = uc.NewChildrenBreakdown(f.deps()).Execute(context.Background(), childrenIn(nil))
	require.ErrorIs(t, err, apperr.ForbiddenError(""))

	f.checker.on = dnode.ID{1}
	_, err = uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(nil))
	require.ErrorIs(t, err, apperr.NotFoundError(""))
	_, err = uc.NewChildrenBreakdown(f.deps()).Execute(context.Background(), childrenIn(nil))
	require.ErrorIs(t, err, apperr.NotFoundError(""))
	require.Empty(t, f.repo.seriesQ)
	require.Empty(t, f.repo.totalsQ)
	require.Empty(t, f.repo.childrenQ)
}

// An empty zone is the effective display_timezone of the node; a given zone wins.
func TestDefaultTimezone(t *testing.T) {
	f := newFakes()
	out, err := uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(func(in *uc.SubtreeSummaryInput) { in.TZ = "" }))
	require.NoError(t, err)
	require.Equal(t, "Europe/Kyiv", out.TZ)
	require.Equal(t, "Europe/Kyiv", f.repo.seriesQ[0].Period.TZ)
	require.Equal(t, f.repo.seriesQ[0], f.repo.totalsQ[0], "series and totals use the same query")

	out, err = uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(func(in *uc.SubtreeSummaryInput) { in.TZ = "Asia/Tokyo" }))
	require.NoError(t, err)
	require.Equal(t, "Asia/Tokyo", out.TZ)

	f.repo.zone = "Local"
	_, err = uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(func(in *uc.SubtreeSummaryInput) { in.TZ = "" }))
	require.Error(t, err, "a stored zone that is not valid is not used")
}

// The page asks for one row more than the limit; the extra row only decides
// whether there is a next page and the cursor continues after the last shown.
func TestChildrenPaging(t *testing.T) {
	f := newFakes()
	mk := func(key string, id byte) port.ChildReportRow {
		return port.ChildReportRow{ChildTotals: drep.ChildTotals{NodeID: dnode.ID{15: id}, Name: key}, NameKey: key}
	}
	f.repo.children = []port.ChildReportRow{mk("a", 1), mk("b", 2), mk("c", 3)}
	d := f.deps()

	out, err := uc.NewChildrenBreakdown(d).Execute(context.Background(), childrenIn(func(in *uc.ChildrenBreakdownInput) { in.Limit = 2 }))
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	require.NotEmpty(t, out.NextCursor)
	require.Equal(t, 3, f.repo.childrenQ[0].Limit)

	out, err = uc.NewChildrenBreakdown(d).Execute(context.Background(), childrenIn(func(in *uc.ChildrenBreakdownInput) { in.Limit = 3 }))
	require.NoError(t, err)
	require.Len(t, out.Items, 3)
	require.Empty(t, out.NextCursor)

	f.repo.children = nil
	out, err = uc.NewChildrenBreakdown(d).Execute(context.Background(), childrenIn(nil))
	require.NoError(t, err)
	require.NotNil(t, out.Items, "no children is an empty page, not nil")
	require.Empty(t, out.Items)
}
