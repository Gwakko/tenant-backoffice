import type { Api } from '@/api/client';
import type { components } from '@/api/schema';
import { unwrap } from '@/api/result';
import { utcStartOfDay } from '@/lib/dates';

import type { OperationsSearch } from './search';

export type OperationPage = components['schemas']['OperationPage'];
export type OperationItem = components['schemas']['OperationListItem'];

export const PAGE_SIZE = 50;

// The scope node comes from the route, never from the filters: the filters can only narrow the
// list inside that subtree (the server answers 404 for a node outside it).
export async function fetchOperationsPage(
  api: Api,
  scopeNodeId: string,
  filters: OperationsSearch,
  cursor: string | undefined,
): Promise<OperationPage> {
  const query: {
    scopeNodeId: string;
    limit: number;
    nodeId?: string;
    kind?: components['schemas']['OperationKind'][];
    from?: string;
    to?: string;
    cursor?: string;
  } = { scopeNodeId, limit: PAGE_SIZE };
  if (filters.node !== undefined) query.nodeId = filters.node;
  if (filters.kind !== undefined) query.kind = [filters.kind];
  if (filters.from !== undefined) query.from = utcStartOfDay(filters.from);
  if (filters.to !== undefined) query.to = utcStartOfDay(filters.to);
  if (cursor !== undefined) query.cursor = cursor;
  return (await unwrap(api.client.GET('/operations', { params: { query } }))).data;
}
