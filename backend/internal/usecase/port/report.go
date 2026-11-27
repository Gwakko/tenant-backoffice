package port

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
)

// SeriesQuery selects the operations of the subtree of Scope inside the
// period. Kinds empty means every kind.
type SeriesQuery struct {
	Scope  node.ID
	Period report.Period
	Kinds  []balance.Kind
}

// ChildrenQuery asks for one page of the direct children of Scope with the
// totals of their subtrees. Limit is the number of rows to return (the use
// case asks for one more than the page to learn whether a next page exists).
type ChildrenQuery struct {
	Scope           node.ID
	Period          report.Period
	IncludeInactive bool
	After           *ChildCursor
	Limit           int
}

// ReportRepo reads the aggregates of reports. Every method works inside the
// transaction of the acting user, so row-level security limits what is counted
// to the actor's subtrees. The use case opens one REPEATABLE READ transaction
// and calls the methods in it: all of them see the same snapshot.
type ReportRepo interface {
	// EffectiveTimezone is the effective display_timezone of the node (the
	// nearest override going up, UTC by default).
	EffectiveTimezone(ctx context.Context, tx Tx, scope node.ID) (string, error)
	// Series returns one row per period of the granularity from the period
	// that contains From to the one that contains To minus one instant,
	// including the periods without operations.
	Series(ctx context.Context, tx Tx, q SeriesQuery) ([]report.PeriodTotals, error)
	// Totals returns the totals of the whole period in one aggregate (not a
	// sum of the series).
	Totals(ctx context.Context, tx Tx, q SeriesQuery) (report.Totals, error)
	// Children returns up to Limit children ordered by (name key, id) after
	// the cursor. The cursor of the next page is built by the caller.
	Children(ctx context.Context, tx Tx, q ChildrenQuery) ([]ChildReportRow, error)
}

// ChildReportRow is one child of a children report with the sort key the next
// page continues after.
type ChildReportRow struct {
	report.ChildTotals
	NameKey string
}
