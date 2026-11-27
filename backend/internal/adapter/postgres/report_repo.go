package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	resourceReport = "report"

	// codeInvalidParameter is "invalid_parameter_value": the store does not know
	// the time zone name that the application accepted.
	codeInvalidParameter = "22023"

	// The operations of the subtree inside the period. Boundaries are the local
	// midnights of the dates in the zone; the store converts them to instants
	// (AT TIME ZONE), the application does no zone arithmetic. $1 scope,
	// $2 from, $3 to, $4 zone, $5 granularity, $6 kinds (NULL for all).
	// Sums of bigint are numeric: they cannot overflow and are read as text.
	subtreeOps = `
WITH scope AS (SELECT path FROM nodes WHERE id = $1::uuid),
bounds AS (
  SELECT ($2::text::date)::timestamp AT TIME ZONE $4::text AS t_from,
         ($3::text::date)::timestamp AT TIME ZONE $4::text AS t_to
),
ops AS (
  SELECT date_trunc($5::text, o.created_at AT TIME ZONE $4::text) AS period_local,
         o.kind, o.amount
    FROM operations o
    JOIN nodes n ON n.id = o.node_id
   CROSS JOIN scope CROSS JOIN bounds
   WHERE n.path <@ scope.path
     AND o.created_at >= bounds.t_from AND o.created_at < bounds.t_to
     AND ($6::text[] IS NULL OR o.kind = ANY($6::text[]))
)`

	selectSeries = subtreeOps + `,
periods AS (
  SELECT generate_series(date_trunc($5::text, $2::text::date::timestamp),
                         $3::text::date::timestamp - interval '1 microsecond',
                         ('1 ' || $5::text)::interval) AS period_local
),
agg AS (
  SELECT p.period_local,
         count(o.kind) AS cnt,
         coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('credit', 'transfer_in')), 0) AS credits,
         coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('debit', 'transfer_out')), 0) AS debits
    FROM periods p LEFT JOIN ops o USING (period_local)
   GROUP BY p.period_local
)
SELECT to_char(period_local, 'YYYY-MM-DD'), cnt, credits::text, debits::text, (credits - debits)::text
  FROM agg ORDER BY period_local`

	selectTotals = subtreeOps + `,
agg AS (
  SELECT count(kind) AS cnt,
         coalesce(sum(amount) FILTER (WHERE kind IN ('credit', 'transfer_in')), 0) AS credits,
         coalesce(sum(amount) FILTER (WHERE kind IN ('debit', 'transfer_out')), 0) AS debits
    FROM ops
)
SELECT cnt, credits::text, debits::text, (credits - debits)::text FROM agg`

	// One row per visible direct child: totals over its whole subtree and the
	// balance of that subtree. $1 scope, $2 from, $3 to, $4 zone,
	// $5 include inactive, $6/$7 cursor (NULL for the first page), $8 limit.
	// The effective activity of the scope (itself and its ancestors) is
	// computed once; a child is active when it and the scope are.
	selectChildren = `
WITH scope AS (SELECT coalesce(tb_node_effective_active($1::uuid), false) AS eff),
bounds AS (
  SELECT ($2::text::date)::timestamp AT TIME ZONE $4::text AS t_from,
         ($3::text::date)::timestamp AT TIME ZONE $4::text AS t_to
),
agg AS (
  SELECT c.id, c.name, c.name_key, c.type,
         (c.status = 'active' AND (SELECT eff FROM scope)) AS active,
         count(o.id) AS cnt,
         coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('credit', 'transfer_in')), 0) AS credits,
         coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('debit', 'transfer_out')), 0) AS debits,
         (SELECT coalesce(sum(b.amount), 0)
            FROM balances b JOIN nodes bd ON bd.id = b.node_id
           WHERE bd.path <@ c.path) AS balance
    FROM nodes c
   CROSS JOIN bounds
    LEFT JOIN nodes d ON d.path <@ c.path
    LEFT JOIN operations o ON o.node_id = d.id
         AND o.created_at >= bounds.t_from AND o.created_at < bounds.t_to
   WHERE c.parent_id = $1::uuid
     AND ($5::boolean OR c.status = 'active')
     AND ($6::text IS NULL OR (c.name_key, c.id) > ($6::text, $7::uuid))
   GROUP BY c.id
   ORDER BY c.name_key, c.id
   LIMIT $8::int
)
SELECT id, name, name_key, type, active, cnt, credits::text, debits::text, (credits - debits)::text, balance::text
  FROM agg ORDER BY name_key, id`

	selectZone = `SELECT value FROM tb_settings_chain($1) WHERE key = 'display_timezone' ORDER BY depth DESC LIMIT 1`
)

// ReportRepo implements port.ReportRepo.
type ReportRepo struct{ log *slog.Logger }

var _ port.ReportRepo = (*ReportRepo)(nil)

// NewReportRepo builds the repository.
func NewReportRepo(log *slog.Logger) *ReportRepo { return &ReportRepo{log: log} }

func (r *ReportRepo) mapErr(op string, err error) error {
	if pe, ok := pgError(err); ok && pe.Code == codeInvalidParameter {
		return apperr.InvalidPeriodError()
	}
	return mapError(r.log, op, resourceReport, err)
}

// kindArg is the filter parameter: NULL (all kinds) or the list of kinds.
func kindArg(q port.SeriesQuery) any {
	if len(q.Kinds) == 0 {
		return nil
	}
	out := make([]string, len(q.Kinds))
	for i, k := range q.Kinds {
		out[i] = string(k)
	}
	return out
}

// EffectiveTimezone implements port.ReportRepo.
func (r *ReportRepo) EffectiveTimezone(ctx context.Context, tx port.Tx, scope node.ID) (string, error) {
	t, err := unwrap(tx)
	if err != nil {
		return "", err
	}
	var raw []byte
	err = t.QueryRow(ctx, selectZone, uuid.UUID(scope)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings.DefaultTimezone, nil
	}
	if err != nil {
		return "", r.mapErr("effective timezone", err)
	}
	var zone string
	if err := json.Unmarshal(raw, &zone); err != nil {
		return "", fmt.Errorf("postgres: display_timezone is not a string: %w", err)
	}
	return zone, nil
}

// Series implements port.ReportRepo.
func (r *ReportRepo) Series(ctx context.Context, tx port.Tx, q port.SeriesQuery) ([]report.PeriodTotals, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, selectSeries, uuid.UUID(q.Scope),
		q.Period.From.String(), q.Period.To.String(), q.Period.TZ, string(q.Period.Granularity), kindArg(q))
	if err != nil {
		return nil, r.mapErr("series", err)
	}
	defer rows.Close()
	out := []report.PeriodTotals{}
	for rows.Next() {
		var start, credits, debits, net string
		var pt report.PeriodTotals
		if err := rows.Scan(&start, &pt.Count, &credits, &debits, &net); err != nil {
			return nil, r.mapErr("scan series", err)
		}
		if pt.PeriodStart, err = report.ParseDate(start); err != nil {
			return nil, fmt.Errorf("postgres: period start %q: %w", start, err)
		}
		if pt.Credits, pt.Debits, pt.Net, err = sums(credits, debits, net); err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, r.mapErr("read series", err)
	}
	return out, nil
}

// Totals implements port.ReportRepo.
func (r *ReportRepo) Totals(ctx context.Context, tx port.Tx, q port.SeriesQuery) (report.Totals, error) {
	t, err := unwrap(tx)
	if err != nil {
		return report.Totals{}, err
	}
	var tot report.Totals
	var credits, debits, net string
	err = t.QueryRow(ctx, selectTotals, uuid.UUID(q.Scope),
		q.Period.From.String(), q.Period.To.String(), q.Period.TZ, string(q.Period.Granularity), kindArg(q)).
		Scan(&tot.Count, &credits, &debits, &net)
	if err != nil {
		return report.Totals{}, r.mapErr("totals", err)
	}
	if tot.Credits, tot.Debits, tot.Net, err = sums(credits, debits, net); err != nil {
		return report.Totals{}, err
	}
	return tot, nil
}

// Children implements port.ReportRepo.
func (r *ReportRepo) Children(ctx context.Context, tx port.Tx, q port.ChildrenQuery) ([]port.ChildReportRow, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	var key *string
	var after *uuid.UUID
	if q.After != nil {
		k, id := q.After.NameKey, uuid.UUID(q.After.ID)
		key, after = &k, &id
	}
	rows, err := t.Query(ctx, selectChildren, uuid.UUID(q.Scope),
		q.Period.From.String(), q.Period.To.String(), q.Period.TZ, q.IncludeInactive, key, after, q.Limit)
	if err != nil {
		return nil, r.mapErr("children report", err)
	}
	defer rows.Close()
	out := []port.ChildReportRow{}
	for rows.Next() {
		var (
			id                        uuid.UUID
			typ                       string
			credits, debits, net, bal string
			row                       port.ChildReportRow
		)
		if err := rows.Scan(&id, &row.Name, &row.NameKey, &typ, &row.Active, &row.Totals.Count, &credits, &debits, &net, &bal); err != nil {
			return nil, r.mapErr("scan children report", err)
		}
		row.NodeID = node.ID(id)
		if row.Type, err = node.ParseType(typ); err != nil {
			return nil, fmt.Errorf("postgres: node %s: %w", id, err)
		}
		if row.Totals.Credits, row.Totals.Debits, row.Totals.Net, err = sums(credits, debits, net); err != nil {
			return nil, err
		}
		if row.Balance, err = report.NewSum(bal); err != nil {
			return nil, fmt.Errorf("postgres: balance of %s: %w", id, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, r.mapErr("read children report", err)
	}
	return out, nil
}

func sums(credits, debits, net string) (c, d, n report.Sum, err error) {
	if c, err = report.NewSum(credits); err != nil {
		return "", "", "", fmt.Errorf("postgres: credits: %w", err)
	}
	if d, err = report.NewSum(debits); err != nil {
		return "", "", "", fmt.Errorf("postgres: debits: %w", err)
	}
	if n, err = report.NewSum(net); err != nil {
		return "", "", "", fmt.Errorf("postgres: net: %w", err)
	}
	return c, d, n, nil
}
