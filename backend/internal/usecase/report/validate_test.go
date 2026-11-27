package report_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

var (
	actor = user.ActorID(uuid.MustParse("00000000-0000-7000-8000-0000000000a1"))
	scope = dnode.ID(uuid.MustParse("00000000-0000-7000-8000-0000000000b1"))
)

func summaryIn(mod func(*uc.SubtreeSummaryInput)) uc.SubtreeSummaryInput {
	in := uc.SubtreeSummaryInput{Actor: actor, NodeID: scope, From: "2026-06-01", To: "2026-07-01", TZ: "UTC", Granularity: "day"}
	if mod != nil {
		mod(&in)
	}
	return in
}

func childrenIn(mod func(*uc.ChildrenBreakdownInput)) uc.ChildrenBreakdownInput {
	in := uc.ChildrenBreakdownInput{Actor: actor, NodeID: scope, From: "2026-06-01", To: "2026-07-01", TZ: "UTC", IncludeInactive: true, Limit: 50}
	if mod != nil {
		mod(&in)
	}
	return in
}

// Test-ID: T-U-27
// EC-90: from >= to, more than 366 days, an unknown granularity and an unknown
// zone are invalid_period, for both reports.
func TestInvalidPeriod(t *testing.T) {
	cases := map[string]func(from, to, tz *string){
		"from equals to":     func(from, to, _ *string) { *to = *from },
		"from after to":      func(from, to, _ *string) { *from, *to = *to, *from },
		"367 days":           func(from, to, _ *string) { *from, *to = "2024-01-01", "2025-01-02" },
		"from is not a date": func(from, _, _ *string) { *from = "2026-02-30" },
		"to has a time":      func(_, to, _ *string) { *to = "2026-07-01T00:00:00Z" },
		"to is short":        func(_, to, _ *string) { *to = "2026-7-1" },
		"zone is unknown":    func(_, _, tz *string) { *tz = "Foo/Bar" },
		"zone is Local":      func(_, _, tz *string) { *tz = "Local" },
		"from is empty":      func(from, _, _ *string) { *from = "" },
	}
	for name, mod := range cases {
		t.Run("summary "+name, func(t *testing.T) {
			err := summaryIn(func(in *uc.SubtreeSummaryInput) { mod(&in.From, &in.To, &in.TZ) }).Validate()
			require.ErrorIs(t, err, apperr.InvalidPeriodError())
		})
		t.Run("children "+name, func(t *testing.T) {
			err := childrenIn(func(in *uc.ChildrenBreakdownInput) { mod(&in.From, &in.To, &in.TZ) }).Validate()
			require.ErrorIs(t, err, apperr.InvalidPeriodError())
		})
	}
	for _, g := range []string{"year", "Day", "hour"} {
		err := summaryIn(func(in *uc.SubtreeSummaryInput) { in.Granularity = g }).Validate()
		require.ErrorIs(t, err, apperr.InvalidPeriodError(), g)
	}
}

// 366 days is the longest accepted period; day, week, month and the default
// granularity are valid; an empty zone means the zone of the node.
func TestValidPeriods(t *testing.T) {
	require.NoError(t, summaryIn(func(in *uc.SubtreeSummaryInput) { in.From, in.To = "2024-01-01", "2025-01-01" }).Validate())
	for _, g := range []string{"", "day", "week", "month"} {
		require.NoError(t, summaryIn(func(in *uc.SubtreeSummaryInput) { in.Granularity = g }).Validate(), g)
	}
	require.NoError(t, summaryIn(func(in *uc.SubtreeSummaryInput) { in.TZ = "" }).Validate())
	require.NoError(t, childrenIn(func(in *uc.ChildrenBreakdownInput) { in.TZ = "Europe/Kyiv" }).Validate())
}

func TestInvalidOtherFields(t *testing.T) {
	err := summaryIn(func(in *uc.SubtreeSummaryInput) { in.Kinds = []string{"credit", "refund"} }).Validate()
	require.ErrorIs(t, err, apperr.New(apperr.ValidationFailed, ""))
	err = summaryIn(func(in *uc.SubtreeSummaryInput) { in.NodeID = dnode.ID{} }).Validate()
	require.ErrorIs(t, err, apperr.New(apperr.ValidationFailed, ""))

	for _, limit := range []int{0, -1, 201} {
		err := childrenIn(func(in *uc.ChildrenBreakdownInput) { in.Limit = limit }).Validate()
		require.ErrorIs(t, err, apperr.New(apperr.ValidationFailed, ""), limit)
	}
	for _, cur := range []string{"not base64 !", "YWJj", "YQBub3QtYS11dWlk"} {
		err := childrenIn(func(in *uc.ChildrenBreakdownInput) { in.Cursor = cur }).Validate()
		require.ErrorIs(t, err, apperr.InvalidCursorError(), cur)
	}
}

// An invalid request never reaches the store.
func TestInvalidInputDoesNotOpenTransaction(t *testing.T) {
	f := newFakes()
	_, err := uc.NewSubtreeSummary(f.deps()).Execute(context.Background(), summaryIn(func(in *uc.SubtreeSummaryInput) { in.To = in.From }))
	require.ErrorIs(t, err, apperr.InvalidPeriodError())
	_, err = uc.NewChildrenBreakdown(f.deps()).Execute(context.Background(), childrenIn(func(in *uc.ChildrenBreakdownInput) { in.Limit = 0 }))
	require.Error(t, err)
	require.Zero(t, f.runner.opened)
}
