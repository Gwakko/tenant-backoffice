//go:build integration

package postgres_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	rep "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

// reportCases are the report use cases wired to the real adapters.
type reportCases struct {
	w       *world
	repo    *postgres.ReportRepo
	summary *rep.SubtreeSummary
	kids    *rep.ChildrenBreakdown
}

func (w *world) reportCases() *reportCases {
	repo := postgres.NewReportRepo(w.runnerLog())
	d := rep.Deps{Runner: w.runner, Checker: w.checker, Reports: repo}
	return &reportCases{w: w, repo: repo, summary: rep.NewSubtreeSummary(d), kids: rep.NewChildrenBreakdown(d)}
}

// reportCasesWith builds the use cases over another repository (a decorator).
func (w *world) reportCasesWith(repo port.ReportRepo) (*rep.SubtreeSummary, *rep.ChildrenBreakdown) {
	d := rep.Deps{Runner: w.runner, Checker: w.checker, Reports: repo}
	return rep.NewSubtreeSummary(d), rep.NewChildrenBreakdown(d)
}

// hookRepo runs hooks between the queries of one report, inside its transaction.
type hookRepo struct {
	port.ReportRepo
	afterSeries func()
}

func (h hookRepo) Series(ctx context.Context, tx port.Tx, q port.SeriesQuery) ([]drep.PeriodTotals, error) {
	out, err := h.ReportRepo.Series(ctx, tx, q)
	if h.afterSeries != nil {
		h.afterSeries()
	}
	return out, err
}

func (c *reportCases) summaryOf(who, scope string, p period) (rep.SubtreeSummaryOutput, error) {
	return c.summary.Execute(context.Background(), rep.SubtreeSummaryInput{
		Actor: c.w.users[who], NodeID: c.w.id(scope), From: p.from, To: p.to, TZ: p.tz, Granularity: p.gran, Kinds: p.kinds,
	})
}

func (c *reportCases) childrenOf(who, scope string, p period, includeInactive bool, limit int, cursor string) (rep.ChildrenBreakdownOutput, error) {
	return c.kids.Execute(context.Background(), rep.ChildrenBreakdownInput{
		Actor: c.w.users[who], NodeID: c.w.id(scope), From: p.from, To: p.to, TZ: p.tz,
		IncludeInactive: includeInactive, Limit: limit, Cursor: cursor,
	})
}

// period is the request part of a report.
type period struct {
	from, to, tz, gran string
	kinds              []string
}

// opRec is one operation the test wrote; the oracle works on these.
type opRec struct {
	node   string
	kind   balance.Kind
	amount int64
	at     time.Time
}

// ledger writes operations straight to the tables as the owner (created_at is
// the test's, not the clock's) and remembers them, with the tree they were
// written in, so that an independent model can compute what a report must say.
type ledger struct {
	w      *world
	ops    []opRec
	parent map[string]string
}

func (w *world) newLedger() *ledger {
	l := &ledger{w: w, parent: map[string]string{}}
	for key, n := range w.nodes {
		if n.ParentID != nil {
			for pk, p := range w.nodes {
				if p.ID == *n.ParentID {
					l.parent[key] = pk
				}
			}
		}
	}
	return l
}

// add writes one operation and moves the balance of the node; the balance may
// not go below zero (the table forbids it).
func (l *ledger) add(nodeKey string, kind balance.Kind, amount int64, at time.Time) {
	l.w.t.Helper()
	l.insert(nodeKey, kind, amount, at, nil, "")
	l.ops = append(l.ops, opRec{node: nodeKey, kind: kind, amount: amount, at: at})
}

func (l *ledger) credit(nodeKey string, amount int64, at time.Time) {
	l.w.t.Helper()
	l.add(nodeKey, balance.Credit, amount, at)
}

func (l *ledger) debit(nodeKey string, amount int64, at time.Time) {
	l.w.t.Helper()
	l.add(nodeKey, balance.Debit, amount, at)
}

// transfer writes both legs under one transfer id.
func (l *ledger) transfer(from, to string, amount int64, at time.Time) {
	l.w.t.Helper()
	tid := uuid.Must(uuid.NewV7())
	l.insert(from, balance.TransferOut, amount, at, &tid, to)
	l.insert(to, balance.TransferIn, amount, at, &tid, from)
	l.ops = append(l.ops,
		opRec{node: from, kind: balance.TransferOut, amount: amount, at: at},
		opRec{node: to, kind: balance.TransferIn, amount: amount, at: at})
}

func (l *ledger) insert(nodeKey string, kind balance.Kind, amount int64, at time.Time, transfer *uuid.UUID, counterparty string) {
	l.w.t.Helper()
	ctx := context.Background()
	id := uuid.UUID(l.w.id(nodeKey))
	delta := new(big.Int).SetInt64(amount)
	if !kind.Increases() {
		delta.Neg(delta)
	}
	var cur string
	require.NoError(l.w.t, l.w.db.Owner.QueryRow(ctx,
		`SELECT coalesce((SELECT amount FROM balances WHERE node_id = $1), 0)::text`, id).Scan(&cur))
	after, ok := new(big.Int).SetString(cur, 10)
	require.True(l.w.t, ok)
	after.Add(after, delta)
	require.GreaterOrEqual(l.w.t, after.Sign(), 0, "test data: balance of %s would go negative", nodeKey)

	var cp any
	if counterparty != "" {
		cp = uuid.UUID(l.w.id(counterparty))
	}
	var tid any
	if transfer != nil {
		tid = *transfer
	}
	_, err := l.w.db.Owner.Exec(ctx,
		`INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, actor_id, created_at)
		 VALUES ($1, $2, $3, $4, $5::bigint, $6, $7, $8, $9)`,
		uuid.Must(uuid.NewV7()), id, string(kind), amount, after.String(), tid, cp, uuid.UUID(l.w.users["ownerRoot"]), at)
	require.NoError(l.w.t, err)
	_, err = l.w.db.Owner.Exec(ctx,
		`INSERT INTO balances (node_id, amount, version, updated_at) VALUES ($1, $2::bigint, 1, $3)
		 ON CONFLICT (node_id) DO UPDATE SET amount = EXCLUDED.amount, version = balances.version + 1, updated_at = EXCLUDED.updated_at`,
		id, after.String(), at)
	require.NoError(l.w.t, err)
}

// move follows a node move in the model (the move itself is done by the use case).
func (l *ledger) move(nodeKey, newParent string) { l.parent[nodeKey] = newParent }

// inSubtree reports whether key is scope or lies below it in the model.
func (l *ledger) inSubtree(key, scope string) bool {
	for k := key; k != ""; k = l.parent[k] {
		if k == scope {
			return true
		}
	}
	return false
}

// sub is the sum of a figure in the oracle.
type sub struct {
	count          int64
	credits, debit big.Int
}

func (s sub) totals() drep.Totals {
	var net big.Int
	net.Sub(&s.credits, &s.debit)
	return drep.Totals{Count: s.count, Credits: drep.Sum(s.credits.String()), Debits: drep.Sum(s.debit.String()), Net: drep.Sum(net.String())}
}

func (s *sub) add(o opRec) {
	s.count++
	if o.kind.Increases() {
		s.credits.Add(&s.credits, big.NewInt(o.amount))
	} else {
		s.debit.Add(&s.debit, big.NewInt(o.amount))
	}
}

// localDate is the date of the instant in the zone, by the Go time package.
func localDate(at time.Time, loc *time.Location) time.Time {
	y, m, d := at.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// bucket is the start of the period of a date; weeks start on Monday.
func bucket(d time.Time, gran string) time.Time {
	switch gran {
	case "week":
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return d
	}
}

func nextBucket(d time.Time, gran string) time.Time {
	switch gran {
	case "week":
		return d.AddDate(0, 0, 7)
	case "month":
		return d.AddDate(0, 1, 0)
	default:
		return d.AddDate(0, 0, 1)
	}
}

// expect computes the summary the report must give, from the recorded
// operations alone: membership by the model tree, boundaries and periods by
// the Go time package.
func (l *ledger) expect(t *testing.T, scope string, p period) (drep.Totals, []drep.PeriodTotals) {
	t.Helper()
	loc, err := time.LoadLocation(p.tz)
	require.NoError(t, err)
	gran := p.gran
	if gran == "" {
		gran = "day"
	}
	from, err := time.ParseInLocation(time.DateOnly, p.from, loc)
	require.NoError(t, err)
	to, err := time.ParseInLocation(time.DateOnly, p.to, loc)
	require.NoError(t, err)
	kinds := map[balance.Kind]bool{}
	for _, k := range p.kinds {
		kinds[balance.Kind(k)] = true
	}

	var total sub
	per := map[time.Time]*sub{}
	for _, o := range l.ops {
		if !l.inSubtree(o.node, scope) || o.at.Before(from) || !o.at.Before(to) || (len(kinds) > 0 && !kinds[o.kind]) {
			continue
		}
		total.add(o)
		b := bucket(localDate(o.at, loc), gran)
		if per[b] == nil {
			per[b] = &sub{}
		}
		per[b].add(o)
	}
	var series []drep.PeriodTotals
	last, err := time.Parse(time.DateOnly, p.to)
	require.NoError(t, err)
	first, err := time.Parse(time.DateOnly, p.from)
	require.NoError(t, err)
	for b := bucket(first, gran); b.Before(last); b = nextBucket(b, gran) {
		s := per[b]
		if s == nil {
			s = &sub{}
		}
		series = append(series, drep.PeriodTotals{PeriodStart: drep.Date(b.Format(time.DateOnly)), Totals: s.totals()})
	}
	return total.totals(), series
}

// requireSummary runs the report and compares it with the oracle.
func (c *reportCases) requireSummary(l *ledger, who, scope string, p period) rep.SubtreeSummaryOutput {
	c.w.t.Helper()
	out, err := c.summaryOf(who, scope, p)
	require.NoError(c.w.t, err, "%s %+v", scope, p)
	totals, series := l.expect(c.w.t, scope, p)
	require.Equal(c.w.t, totals, out.Totals, "totals of %s %+v", scope, p)
	require.Equal(c.w.t, series, out.Series, "series of %s %+v", scope, p)
	return out
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}
