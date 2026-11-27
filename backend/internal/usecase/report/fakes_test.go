package report_test

import (
	"context"

	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

type fakeRunner struct {
	opened int
	last   port.TxOptions
}

func (r *fakeRunner) InTx(ctx context.Context, _ user.ActorID, opts port.TxOptions, fn func(context.Context, port.Tx) error) error {
	r.opened++
	r.last = opts
	return fn(ctx, fakeTx{})
}

// fakeChecker gives the role to the actor on the scope node only.
type fakeChecker struct {
	role role.Role
	on   dnode.ID
}

func (c fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, _ user.ActorID, id dnode.ID) (role.Role, bool, error) {
	if id != c.on {
		return "", false, nil
	}
	return c.role, true, nil
}

type fakeRepo struct {
	zone     string
	series   []drep.PeriodTotals
	totals   drep.Totals
	children []port.ChildReportRow

	seriesQ   []port.SeriesQuery
	totalsQ   []port.SeriesQuery
	childrenQ []port.ChildrenQuery
}

func (r *fakeRepo) EffectiveTimezone(context.Context, port.Tx, dnode.ID) (string, error) {
	return r.zone, nil
}

func (r *fakeRepo) Series(_ context.Context, _ port.Tx, q port.SeriesQuery) ([]drep.PeriodTotals, error) {
	r.seriesQ = append(r.seriesQ, q)
	return r.series, nil
}

func (r *fakeRepo) Totals(_ context.Context, _ port.Tx, q port.SeriesQuery) (drep.Totals, error) {
	r.totalsQ = append(r.totalsQ, q)
	return r.totals, nil
}

func (r *fakeRepo) Children(_ context.Context, _ port.Tx, q port.ChildrenQuery) ([]port.ChildReportRow, error) {
	r.childrenQ = append(r.childrenQ, q)
	rows := r.children
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

type fakes struct {
	runner  *fakeRunner
	checker fakeChecker
	repo    *fakeRepo
}

func newFakes() *fakes {
	return &fakes{
		runner:  &fakeRunner{},
		checker: fakeChecker{role: role.Analyst, on: scope},
		repo:    &fakeRepo{zone: "Europe/Kyiv"},
	}
}

func (f *fakes) deps() uc.Deps { return uc.Deps{Runner: f.runner, Checker: f.checker, Reports: f.repo} }
