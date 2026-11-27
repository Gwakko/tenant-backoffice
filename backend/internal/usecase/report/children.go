package report

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// ChildrenBreakdownInput is the request of UC-R2. From and To are dates
// (YYYY-MM-DD, To exclusive); TZ empty means the effective display_timezone of
// the node. IncludeInactive false drops the children that are themselves
// inactive.
type ChildrenBreakdownInput struct {
	Actor           user.ActorID
	NodeID          dnode.ID
	From, To        string
	TZ              string
	IncludeInactive bool
	Limit           int
	Cursor          string
}

type childrenParams struct {
	rng   drep.Range
	tz    string
	after *port.ChildCursor
}

func (in ChildrenBreakdownInput) parse() (childrenParams, error) {
	var p childrenParams
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
	if in.Limit < 1 || in.Limit > maxLimit {
		return p, apperr.New(apperr.ValidationFailed, "limit must be 1..200").WithField("limit", "out_of_range")
	}
	p.after, err = decodeCursor(in.Cursor)
	return p, err
}

// Validate checks the input without touching the store.
func (in ChildrenBreakdownInput) Validate() error {
	_, err := in.parse()
	return err
}

// ChildrenBreakdownOutput is one page of children; NextCursor is empty on the
// last page. TZ is the zone that was used.
type ChildrenBreakdownOutput struct {
	TZ         string
	Items      []drep.ChildTotals
	NextCursor string
}

// ChildrenBreakdown is UC-R2.
type ChildrenBreakdown struct{ d Deps }

// NewChildrenBreakdown builds the use case.
func NewChildrenBreakdown(d Deps) *ChildrenBreakdown { return &ChildrenBreakdown{d: d} }

// Execute lists the direct children of the node with the totals of their
// subtrees for the period and the current balance of each subtree. Pages are
// read in separate transactions: consistency holds inside one page.
func (uc *ChildrenBreakdown) Execute(ctx context.Context, in ChildrenBreakdownInput) (ChildrenBreakdownOutput, error) {
	p, err := in.parse()
	if err != nil {
		return ChildrenBreakdownOutput{}, err
	}
	d := uc.d
	out := ChildrenBreakdownOutput{Items: []drep.ChildTotals{}}
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
		rows, err := d.Reports.Children(ctx, tx, port.ChildrenQuery{
			Scope:           in.NodeID,
			Period:          drep.Period{Range: p.rng, TZ: tz, Granularity: drep.Day},
			IncludeInactive: in.IncludeInactive,
			After:           p.after,
			Limit:           in.Limit + 1,
		})
		if err != nil {
			return err
		}
		if len(rows) > in.Limit {
			rows = rows[:in.Limit]
			last := rows[len(rows)-1]
			out.NextCursor = encodeCursor(port.ChildCursor{NameKey: last.NameKey, ID: last.NodeID})
		}
		out.TZ = tz
		for _, r := range rows {
			out.Items = append(out.Items, r.ChildTotals)
		}
		return nil
	})
	if err != nil {
		return ChildrenBreakdownOutput{}, err
	}
	return out, nil
}
