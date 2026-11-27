import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useMemo } from 'react';

import { keys } from '@/api/keys';
import { fetchBalance, fetchChildrenPage } from '@/api/resources';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SelectField, type SelectOption } from '@/components/ui/SelectField';
import { Table, Td, Th } from '@/components/ui/Table';
import { TextField } from '@/components/ui/TextField';
import { Alert } from '@/components/ui/Alert';
import { QueryProblem } from '@/hooks/QueryProblem';
import { useApi } from '@/hooks/useApi';
import { useCursorReset } from '@/hooks/useCursorReset';
import { daysBetween, formatUtc } from '@/lib/dates';
import { uniqueById } from '@/lib/list';
import { formatMinor } from '@/lib/money';

import { fetchOperationsPage, type OperationItem } from './queries';
import { OPERATION_KINDS, type OperationKind, type OperationsSearch } from './search';

const kindLabels: Record<OperationKind, string> = {
  credit: 'Credit',
  debit: 'Debit',
  transfer_out: 'Transfer out',
  transfer_in: 'Transfer in',
};

const incoming: ReadonlySet<OperationKind> = new Set(['credit', 'transfer_in']);

interface OperationsPanelProps {
  /** The scope node, taken from the route. */
  nodeId: string;
  filters: OperationsSearch;
  onFiltersChange: (patch: Partial<OperationsSearch>) => void;
}

export function OperationsPanel({ nodeId, filters, onFiltersChange }: OperationsPanelProps) {
  const api = useApi();
  const { node, kind, from, to } = filters;
  const flat = useMemo(() => ({ node, kind, from, to }), [node, kind, from, to]);
  const queryKey = useMemo(() => keys.operations(nodeId, flat), [nodeId, flat]);

  const periodInvalid = from !== undefined && to !== undefined && (daysBetween(from, to) ?? 1) <= 0;

  const list = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchOperationsPage(api, nodeId, flat, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled: !periodInvalid,
  });
  useCursorReset(list, queryKey);

  const balance = useQuery({ queryKey: keys.balance(nodeId), queryFn: () => fetchBalance(api, nodeId) });
  const children = useQuery({
    queryKey: keys.children(nodeId, { purpose: 'filter' }),
    queryFn: () => fetchChildrenPage(api, nodeId, { limit: 50 }),
  });

  const items = useMemo(() => uniqueById(list.data?.pages.flatMap((page) => page.items) ?? []), [list.data]);
  const names = useMemo(() => {
    const map = new Map<string, string>();
    for (const child of children.data?.items ?? []) map.set(child.id, child.name);
    for (const item of items) map.set(item.nodeId, item.nodeName);
    return map;
  }, [children.data, items]);

  const nodeOptions: SelectOption[] = [{ value: '', label: 'This node and its subtree' }];
  for (const child of children.data?.items ?? []) nodeOptions.push({ value: child.id, label: child.name });
  if (node !== undefined && !nodeOptions.some((option) => option.value === node)) {
    nodeOptions.push({ value: node, label: names.get(node) ?? `Node ${node.slice(0, 8)}` });
  }
  const kindOptions: SelectOption[] = [
    { value: '', label: 'All kinds' },
    ...OPERATION_KINDS.map((value) => ({ value, label: kindLabels[value] })),
  ];

  const currency = balance.data?.currency;
  const filtered = node !== undefined || kind !== undefined || from !== undefined || to !== undefined;

  return (
    <section aria-labelledby="operations-title" className="space-y-4">
      <h1 id="operations-title" className="text-xl font-semibold">
        Operations
      </h1>

      <form
        aria-label="Operation filters"
        className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
        onSubmit={(event) => {
          event.preventDefault();
        }}
      >
        <SelectField
          label="Node"
          options={nodeOptions}
          value={node ?? ''}
          onChange={(event) => {
            onFiltersChange({ node: event.target.value || undefined });
          }}
        />
        <SelectField
          label="Kind"
          options={kindOptions}
          value={kind ?? ''}
          onChange={(event) => {
            onFiltersChange({ kind: OPERATION_KINDS.find((value) => value === event.target.value) });
          }}
        />
        <TextField
          label="From (UTC date)"
          type="date"
          value={from ?? ''}
          onChange={(event) => {
            onFiltersChange({ from: event.target.value || undefined });
          }}
        />
        <TextField
          label="To (UTC date, exclusive)"
          type="date"
          value={to ?? ''}
          onChange={(event) => {
            onFiltersChange({ to: event.target.value || undefined });
          }}
        />
      </form>
      {filtered && (
        <Button
          variant="secondary"
          onClick={() => {
            onFiltersChange({ node: undefined, kind: undefined, from: undefined, to: undefined });
          }}
        >
          Reset filters
        </Button>
      )}

      {periodInvalid && <Alert>The start date must be before the end date.</Alert>}
      {list.isPending && !periodInvalid && <p className="text-sm text-slate-600">Loading...</p>}
      {list.isError && (
        <QueryProblem
          error={list.error}
          permsNodeId={nodeId}
          onRetry={() => {
            void list.refetch();
          }}
        />
      )}
      {list.isSuccess && items.length === 0 && <EmptyState>No operations for these filters.</EmptyState>}
      {items.length > 0 && (
        <Table caption="Operations">
          <thead>
            <tr>
              <Th>Time</Th>
              <Th>Node</Th>
              <Th>Kind</Th>
              <Th className="text-right">Amount{currency ? ` (${currency})` : ''}</Th>
              <Th className="text-right">Balance after</Th>
              <Th>Comment</Th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <OperationRow key={item.id} item={item} names={names} />
            ))}
          </tbody>
        </Table>
      )}
      {list.hasNextPage && (
        <Button
          variant="secondary"
          busy={list.isFetchingNextPage}
          onClick={() => {
            void list.fetchNextPage();
          }}
        >
          Load more
        </Button>
      )}
    </section>
  );
}

function OperationRow({ item, names }: { item: OperationItem; names: ReadonlyMap<string, string> }) {
  const sign = incoming.has(item.kind) ? '+' : '-';
  const counterparty = item.counterpartyNodeId;
  const isTransfer = item.kind === 'transfer_in' || item.kind === 'transfer_out';
  return (
    <tr>
      <Td className="whitespace-nowrap">
        <time dateTime={item.createdAt}>{formatUtc(item.createdAt)}</time>
      </Td>
      <Td>
        <span className="block max-w-48 truncate" title={item.nodeName}>
          {item.nodeName}
        </span>
      </Td>
      <Td>
        {kindLabels[item.kind]}
        {isTransfer && counterparty !== null && (
          <span className="block text-xs text-slate-600">
            {item.kind === 'transfer_out' ? 'to ' : 'from '}
            <Link to="/nodes/$nodeId" params={{ nodeId: counterparty }} className="underline">
              {names.get(counterparty) ?? `node ${counterparty.slice(0, 8)}`}
            </Link>
          </span>
        )}
      </Td>
      <Td className="whitespace-nowrap text-right tabular-nums">
        {sign}
        {formatMinor(item.amount)}
      </Td>
      <Td className="whitespace-nowrap text-right tabular-nums">{formatMinor(item.balanceAfter)}</Td>
      <Td>
        {item.comment !== null && (
          <span className="block max-w-64 truncate" title={item.comment}>
            {item.comment}
          </span>
        )}
      </Td>
    </tr>
  );
}
