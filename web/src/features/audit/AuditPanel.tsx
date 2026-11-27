import { useInfiniteQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { keys } from '@/api/keys';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SelectField, type SelectOption } from '@/components/ui/SelectField';
import { Table, Td, Th } from '@/components/ui/Table';
import { TextField } from '@/components/ui/TextField';
import { QueryProblem } from '@/hooks/QueryProblem';
import { useApi } from '@/hooks/useApi';
import { useCursorReset } from '@/hooks/useCursorReset';
import { daysBetween, formatUtc } from '@/lib/dates';
import { uniqueById } from '@/lib/list';
import { previewJson, truncate } from '@/lib/text';

import { fetchAuditPage, type AuditEntry } from './queries';
import { AUDIT_ACTIONS, auditActionLabels, type AuditSearch } from './search';

interface AuditPanelProps {
  /** The scope node, taken from the route. */
  nodeId: string;
  filters: AuditSearch;
  onFiltersChange: (patch: Partial<AuditSearch>) => void;
}

const actionOptions: SelectOption[] = [
  { value: '', label: 'All actions' },
  ...AUDIT_ACTIONS.map((value) => ({ value, label: auditActionLabels[value] })),
];

export function AuditPanel({ nodeId, filters, onFiltersChange }: AuditPanelProps) {
  const api = useApi();
  const { node, action, from, to } = filters;
  const flat = useMemo(() => ({ node, action, from, to }), [node, action, from, to]);
  const queryKey = useMemo(() => keys.audit(nodeId, flat), [nodeId, flat]);
  const periodInvalid = from !== undefined && to !== undefined && (daysBetween(from, to) ?? 1) <= 0;

  const list = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchAuditPage(api, nodeId, flat, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled: !periodInvalid,
  });
  useCursorReset(list, queryKey);

  const entries = useMemo(() => uniqueById(list.data?.pages.flatMap((page) => page.items) ?? []), [list.data]);
  const filtered = node !== undefined || action !== undefined || from !== undefined || to !== undefined;

  return (
    <section aria-labelledby="audit-title" className="space-y-4">
      <h1 id="audit-title" className="text-xl font-semibold">
        Audit
      </h1>

      <form
        aria-label="Audit filters"
        className="grid gap-3 sm:grid-cols-3"
        onSubmit={(event) => {
          event.preventDefault();
        }}
      >
        <SelectField
          label="Action"
          options={actionOptions}
          value={action ?? ''}
          onChange={(event) => {
            onFiltersChange({ action: AUDIT_ACTIONS.find((value) => value === event.target.value) });
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
            onFiltersChange({ node: undefined, action: undefined, from: undefined, to: undefined });
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
      {list.isSuccess && entries.length === 0 && <EmptyState>No audit entries for these filters.</EmptyState>}
      {entries.length > 0 && (
        <Table caption="Audit entries">
          <thead>
            <tr>
              <Th>Time</Th>
              <Th>Action</Th>
              <Th>Actor</Th>
              <Th>Node</Th>
              <Th>Target</Th>
              <Th>Changes</Th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => (
              <AuditRow key={entry.id} entry={entry} />
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

function AuditRow({ entry }: { entry: AuditEntry }) {
  const before = previewJson(entry.before);
  const after = previewJson(entry.after);
  return (
    <tr>
      <Td className="whitespace-nowrap">
        <time dateTime={entry.occurredAt}>{formatUtc(entry.occurredAt)}</time>
      </Td>
      <Td>{auditActionLabels[entry.action]}</Td>
      <Td>
        <span className="block max-w-48 truncate" title={entry.actorEmail ?? undefined}>
          {entry.actorEmail ?? 'Hidden user'}
        </span>
      </Td>
      <Td>
        <span className="block max-w-48 truncate" title={entry.nodeName}>
          {entry.nodeName}
        </span>
      </Td>
      <Td>
        <span className="block max-w-48 truncate" title={entry.targetId}>
          {entry.targetType}: {truncate(entry.targetId, 40)}
        </span>
      </Td>
      <Td>
        {before !== '' && (
          <>
            <p className="text-xs font-medium text-slate-600">Before</p>
            <pre className="max-w-96 overflow-x-auto whitespace-pre-wrap break-words text-xs">{before}</pre>
          </>
        )}
        {after !== '' && (
          <>
            <p className="text-xs font-medium text-slate-600">After</p>
            <pre className="max-w-96 overflow-x-auto whitespace-pre-wrap break-words text-xs">{after}</pre>
          </>
        )}
      </Td>
    </tr>
  );
}
