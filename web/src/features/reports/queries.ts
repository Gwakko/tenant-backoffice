import type { Api } from '@/api/client';
import { unwrap } from '@/api/result';
import type { components } from '@/api/schema';

import type { Granularity } from './search';

export type SubtreeSummary = components['schemas']['SubtreeSummary'];
export type ChildTotalsPage = components['schemas']['ChildTotalsPage'];
export type ChildTotals = components['schemas']['ChildTotals'];
export type PeriodTotals = components['schemas']['PeriodTotals'];

export interface ReportParams {
  from: string;
  to: string;
  tz: string;
}

export const CHILDREN_PAGE_SIZE = 50;

// The node id is the route's: the report never takes a node from user input.
export async function fetchSummary(api: Api, id: string, params: ReportParams, granularity: Granularity): Promise<SubtreeSummary> {
  const query = { from: params.from, to: params.to, tz: params.tz, granularity };
  return (await unwrap(api.client.GET('/nodes/{id}/reports/summary', { params: { path: { id }, query } }))).data;
}

export async function fetchChildrenReport(
  api: Api,
  id: string,
  params: ReportParams,
  cursor: string | undefined,
): Promise<ChildTotalsPage> {
  const query: { from: string; to: string; tz: string; limit: number; cursor?: string } = {
    from: params.from,
    to: params.to,
    tz: params.tz,
    limit: CHILDREN_PAGE_SIZE,
  };
  if (cursor !== undefined) query.cursor = cursor;
  return (await unwrap(api.client.GET('/nodes/{id}/reports/children', { params: { path: { id }, query } }))).data;
}

/** The effective `display_timezone` of a node, from its settings list; null when absent. */
export async function fetchDisplayTimezone(api: Api, id: string): Promise<string | null> {
  const items = (await unwrap(api.client.GET('/nodes/{id}/settings', { params: { path: { id } } }))).data.items;
  const value = items.find((item) => item.key === 'display_timezone')?.effective;
  return typeof value === 'string' ? value : null;
}
