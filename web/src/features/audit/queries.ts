import type { Api } from '@/api/client';
import { unwrap } from '@/api/result';
import type { components } from '@/api/schema';
import { utcStartOfDay } from '@/lib/dates';

import type { AuditSearch } from './search';

export type AuditPage = components['schemas']['AuditPage'];
export type AuditEntry = components['schemas']['AuditEntry'];

export const PAGE_SIZE = 50;

// The scope node comes from the route; the filters only narrow the list inside its subtree.
export async function fetchAuditPage(
  api: Api,
  scopeNodeId: string,
  filters: AuditSearch,
  cursor: string | undefined,
): Promise<AuditPage> {
  const query: {
    scopeNodeId: string;
    limit: number;
    nodeId?: string;
    action?: NonNullable<AuditSearch['action']>;
    from?: string;
    to?: string;
    cursor?: string;
  } = { scopeNodeId, limit: PAGE_SIZE };
  if (filters.node !== undefined) query.nodeId = filters.node;
  if (filters.action !== undefined) query.action = filters.action;
  if (filters.from !== undefined) query.from = utcStartOfDay(filters.from);
  if (filters.to !== undefined) query.to = utcStartOfDay(filters.to);
  if (cursor !== undefined) query.cursor = cursor;
  return (await unwrap(api.client.GET('/audit', { params: { query } }))).data;
}
