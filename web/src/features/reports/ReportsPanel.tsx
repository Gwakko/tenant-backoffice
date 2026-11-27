import { zodResolver } from '@hookform/resolvers/zod';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { keys } from '@/api/keys';
import { problemMessages } from '@/api/problem';
import { fetchBalance } from '@/api/resources';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SelectField } from '@/components/ui/SelectField';
import { Table, Td, Th } from '@/components/ui/Table';
import { TextField } from '@/components/ui/TextField';
import { QueryProblem } from '@/hooks/QueryProblem';
import { useApi } from '@/hooks/useApi';
import { useCursorReset } from '@/hooks/useCursorReset';
import { checkPeriod, type PeriodProblem } from '@/lib/dates';
import { uniqueById } from '@/lib/list';
import { browserTimeZone, isKnownTimeZone, supportedTimeZones } from '@/lib/timezones';

import { formatSum } from './format';
import { fetchChildrenReport, fetchDisplayTimezone, fetchSummary, type ChildTotals, type ReportParams } from './queries';
import { GRANULARITIES, resolvePeriod, type Granularity, type ReportsSearch } from './search';
import { SeriesChart } from './SeriesChart';

const periodDetails: Record<PeriodProblem, string> = {
  invalid_date: 'Enter real dates as YYYY-MM-DD.',
  not_ordered: 'The start date must be before the end date.',
  too_long: 'The period can be at most 366 days.',
};

// The same text as the server's `invalid_period`, with the reason, so a rejected form and a
// rejected request read alike.
function periodMessage(problem: PeriodProblem): string {
  return `${problemMessages.invalid_period} ${periodDetails[problem]}`;
}

const formSchema = z
  .object({
    from: z.string(),
    to: z.string(),
    tz: z.string(),
    granularity: z.enum(GRANULARITIES),
  })
  .superRefine((value, ctx) => {
    const problem = checkPeriod(value.from, value.to);
    if (problem !== null) ctx.addIssue({ code: 'custom', path: ['to'], message: periodMessage(problem) });
    if (!isKnownTimeZone(value.tz)) ctx.addIssue({ code: 'custom', path: ['tz'], message: 'Choose a time zone from the list.' });
  });
type ReportForm = z.infer<typeof formSchema>;

const granularityOptions = GRANULARITIES.map((value) => ({ value, label: `${value.slice(0, 1).toUpperCase()}${value.slice(1)}` }));

interface ReportsPanelProps {
  /** The node of the route: the report always covers its subtree. */
  nodeId: string;
  search: ReportsSearch;
  onApply: (next: { from: string; to: string; tz: string; granularity: Granularity }) => void;
}

export function ReportsPanel({ nodeId, search, onApply }: ReportsPanelProps) {
  const api = useApi();
  const [now] = useState(() => new Date());
  const { from: periodFrom, to: periodTo } = resolvePeriod(search, now);
  const granularity = search.granularity ?? 'day';

  // Zone: the URL, else the effective display_timezone of the node, else the browser's zone.
  const zoneSetting = useQuery({
    queryKey: [...keys.settings(nodeId), 'display_timezone'],
    queryFn: () => fetchDisplayTimezone(api, nodeId),
    enabled: search.tz === undefined,
    retry: false,
  });
  const fromSetting = typeof zoneSetting.data === 'string' && isKnownTimeZone(zoneSetting.data) ? zoneSetting.data : undefined;
  const tz = search.tz ?? fromSetting ?? browserTimeZone();
  const zoneReady = search.tz !== undefined || !zoneSetting.isPending;

  const periodProblem = checkPeriod(periodFrom, periodTo);
  const enabled = zoneReady && periodProblem === null;
  const params: ReportParams = { from: periodFrom, to: periodTo, tz };

  const summary = useQuery({
    queryKey: keys.reportSummary(nodeId, { ...params, granularity }),
    queryFn: () => fetchSummary(api, nodeId, params, granularity),
    enabled,
  });
  const childrenKey = keys.reportChildren(nodeId, { from: periodFrom, to: periodTo, tz });
  const children = useInfiniteQuery({
    queryKey: childrenKey,
    queryFn: ({ pageParam }) => fetchChildrenReport(api, nodeId, params, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled,
  });
  useCursorReset(children, childrenKey);
  const balance = useQuery({ queryKey: keys.balance(nodeId), queryFn: () => fetchBalance(api, nodeId) });
  const currency = balance.data?.currency;
  const rows = useMemo(() => uniqueById(children.data?.pages.flatMap((page) => page.items.map((item) => ({ ...item, id: item.nodeId }))) ?? []), [children.data]);

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<ReportForm>({
    resolver: zodResolver(formSchema),
    values: { from: periodFrom, to: periodTo, tz, granularity },
  });

  return (
    <section aria-labelledby="reports-title" className="space-y-4">
      <h1 id="reports-title" className="text-xl font-semibold">
        Reports
      </h1>

      <form
        aria-label="Report period"
        noValidate
        className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5 lg:items-start"
        onSubmit={(event) => {
          void handleSubmit((value) => {
            onApply(value);
          })(event);
        }}
      >
        <TextField label="From" type="date" error={errors.from?.message} {...register('from')} />
        <TextField label="To (exclusive)" type="date" error={errors.to?.message} {...register('to')} />
        <SelectField
          label="Time zone"
          options={supportedTimeZones().map((zone) => ({ value: zone, label: zone }))}
          error={errors.tz?.message}
          {...register('tz')}
        />
        <SelectField label="Granularity" options={granularityOptions} {...register('granularity')} />
        <div className="lg:pt-6">
          <Button type="submit">Apply</Button>
        </div>
      </form>

      {periodProblem !== null && <Alert>{periodMessage(periodProblem)}</Alert>}
      {summary.isError && (
        <QueryProblem
          error={summary.error}
          permsNodeId={nodeId}
          onRetry={() => {
            void summary.refetch();
          }}
        />
      )}
      {summary.isPending && enabled && <p className="text-sm text-slate-600">Loading...</p>}

      {summary.isSuccess && (
        <>
          <dl aria-label="Totals" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat label="Operations" value={new Intl.NumberFormat('en').format(summary.data.totals.count)} />
            <Stat label={`Credits${currency ? ` (${currency})` : ''}`} value={formatSum(summary.data.totals.credits)} />
            <Stat label={`Debits${currency ? ` (${currency})` : ''}`} value={formatSum(summary.data.totals.debits)} />
            <Stat label={`Net${currency ? ` (${currency})` : ''}`} value={formatSum(summary.data.totals.net)} />
          </dl>
          <p className="text-xs text-slate-600">
            Periods follow the {summary.data.tz} time zone. Transfers inside the subtree are counted on both sides.
          </p>
          <SeriesChart series={summary.data.series} currency={currency} />
          {summary.data.series.length === 0 ? (
            <EmptyState>No operations in this period.</EmptyState>
          ) : (
            <Table caption="Totals by period">
              <thead>
                <tr>
                  <Th>Period start</Th>
                  <Th className="text-right">Operations</Th>
                  <Th className="text-right">Credits</Th>
                  <Th className="text-right">Debits</Th>
                  <Th className="text-right">Net</Th>
                </tr>
              </thead>
              <tbody>
                {summary.data.series.map((point) => (
                  <tr key={point.periodStart}>
                    <Td>{point.periodStart}</Td>
                    <Td className="text-right tabular-nums">{new Intl.NumberFormat('en').format(point.count)}</Td>
                    <Td className="text-right tabular-nums">{formatSum(point.credits)}</Td>
                    <Td className="text-right tabular-nums">{formatSum(point.debits)}</Td>
                    <Td className="text-right tabular-nums">{formatSum(point.net)}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </>
      )}

      <h2 className="text-lg font-semibold">By child</h2>
      {children.isError && (
        <QueryProblem
          error={children.error}
          permsNodeId={nodeId}
          onRetry={() => {
            void children.refetch();
          }}
        />
      )}
      {children.isSuccess && rows.length === 0 && <EmptyState>This node has no children.</EmptyState>}
      {rows.length > 0 && (
        <Table caption="Totals by child">
          <thead>
            <tr>
              <Th>Name</Th>
              <Th>Type</Th>
              <Th className="text-right">Operations</Th>
              <Th className="text-right">Credits</Th>
              <Th className="text-right">Debits</Th>
              <Th className="text-right">Net</Th>
              <Th className="text-right">Balance</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <ChildRow key={row.nodeId} row={row} />
            ))}
          </tbody>
        </Table>
      )}
      {children.hasNextPage && (
        <Button
          variant="secondary"
          busy={children.isFetchingNextPage}
          onClick={() => {
            void children.fetchNextPage();
          }}
        >
          Load more
        </Button>
      )}
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-slate-200 bg-white p-3">
      <dt className="text-xs text-slate-600">{label}</dt>
      <dd className="mt-1 text-lg font-semibold tabular-nums">{value}</dd>
    </div>
  );
}

function ChildRow({ row }: { row: ChildTotals }) {
  return (
    <tr>
      <Td>
        <span className="block max-w-56 truncate" title={row.name}>
          {row.name}
        </span>
        {!row.active && <span className="text-xs text-slate-600">inactive</span>}
      </Td>
      <Td>{row.type}</Td>
      <Td className="text-right tabular-nums">{new Intl.NumberFormat('en').format(row.count)}</Td>
      <Td className="text-right tabular-nums">{formatSum(row.credits)}</Td>
      <Td className="text-right tabular-nums">{formatSum(row.debits)}</Td>
      <Td className="text-right tabular-nums">{formatSum(row.net)}</Td>
      <Td className="text-right tabular-nums">{formatSum(row.balance)}</Td>
    </tr>
  );
}
