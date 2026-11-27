package report

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// SubtreeSummaryInput is the request of UC-R1. From and To are dates
// (YYYY-MM-DD, To exclusive); TZ empty means the effective display_timezone of
// the node; Granularity empty means day; Kinds empty means every kind.
type SubtreeSummaryInput struct {
	Actor       user.ActorID
	NodeID      dnode.ID
	From, To    string
	TZ          string
	Granularity string
	Kinds       []string
}

type summaryParams struct {
	rng  drep.Range
	tz   string // empty: resolve the effective zone of the node
	gran drep.Granularity
	kind []balance.Kind
}

func (in SubtreeSummaryInput) parse() (summaryParams, error) {
	var p summaryParams
	if err := requireID(in.NodeID); err != nil {
		return p, err
	}
	var err error
	if p.rng, err = drep.NewRange(in.From, in.To); err != nil {
		return p, err
	}
	if in.TZ != "" {
		if p.tz, err = drep.ParseTZ(in.TZ); err != nil {
			return p, err
		}
	}
	if p.gran, err = drep.ParseGranularity(in.Granularity); err != nil {
		return p, err
	}
	p.kind, err = drep.ParseKinds(in.Kinds)
	return p, err
}

// Validate checks the input without touching the store.
func (in SubtreeSummaryInput) Validate() error {
	_, err := in.parse()
	return err
}

// SubtreeSummaryOutput is the report: the totals of the whole period and the
// series, one entry per period of the granularity, empty ones included. TZ is
// the zone that was used.
type SubtreeSummaryOutput struct {
	ScopeNodeID dnode.ID
	TZ          string
	From, To    drep.Date
	Totals      drep.Totals
	Series      []drep.PeriodTotals
}

// SubtreeSummary is UC-R1.
type SubtreeSummary struct{ d Deps }

// NewSubtreeSummary builds the use case.
func NewSubtreeSummary(d Deps) *SubtreeSummary { return &SubtreeSummary{d: d} }

// Execute builds the summary of the subtree of the node: all operations of the
// node and its descendants the actor can see.
func (uc *SubtreeSummary) Execute(ctx context.Context, in SubtreeSummaryInput) (SubtreeSummaryOutput, error) {
	p, err := in.parse()
	if err != nil {
		return SubtreeSummaryOutput{}, err
	}
	d := uc.d
	var out SubtreeSummaryOutput
	err = d.Runner.InTx(ctx, in.Actor, snapshotTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID); err != nil {
			return err
		}
		tz := p.tz
		if tz == "" {
			var err error
			if tz, err = d.Reports.EffectiveTimezone(ctx, tx, in.NodeID); err != nil {
				return err
			}
			if tz, err = drep.ParseTZ(tz); err != nil {
				return err
			}
		}
		q := port.SeriesQuery{
			Scope:  in.NodeID,
			Period: drep.Period{Range: p.rng, TZ: tz, Granularity: p.gran},
			Kinds:  p.kind,
		}
		series, err := d.Reports.Series(ctx, tx, q)
		if err != nil {
			return err
		}
		totals, err := d.Reports.Totals(ctx, tx, q)
		if err != nil {
			return err
		}
		out = SubtreeSummaryOutput{
			ScopeNodeID: in.NodeID, TZ: tz, From: p.rng.From, To: p.rng.To, Totals: totals, Series: series,
		}
		return nil
	})
	if err != nil {
		return SubtreeSummaryOutput{}, err
	}
	return out, nil
}
