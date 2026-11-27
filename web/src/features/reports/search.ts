import { z } from 'zod';

import { addDays, utcDate } from '@/lib/dates';
import { dateParam, enumParam, parseSearch } from '@/lib/searchParams';
import { isKnownTimeZone } from '@/lib/timezones';

export const GRANULARITIES = ['day', 'week', 'month'] as const;
export type Granularity = (typeof GRANULARITIES)[number];

const tzParam = z.string().refine(isKnownTimeZone).optional().catch(undefined);

// The period and the zone of the report live in the URL. Missing or invalid values become
// `undefined` and are replaced by the defaults below.
export const reportsSearchSchema = z.object({
  from: dateParam,
  to: dateParam,
  tz: tzParam,
  granularity: enumParam(GRANULARITIES),
});

export type ReportsSearch = z.infer<typeof reportsSearchSchema>;

export function parseReportsSearch(raw: unknown): ReportsSearch {
  return parseSearch(reportsSearchSchema, raw);
}

export const DEFAULT_PERIOD_DAYS = 30;

export interface ResolvedPeriod {
  from: string;
  to: string;
}

/** The last 30 days up to and including today (UTC) unless the URL says otherwise. */
export function resolvePeriod(search: ReportsSearch, now: Date): ResolvedPeriod {
  const to = search.to ?? addDays(search.from ?? utcDate(now), search.from === undefined ? 1 : DEFAULT_PERIOD_DAYS);
  const from = search.from ?? addDays(to, -DEFAULT_PERIOD_DAYS);
  return { from, to };
}
