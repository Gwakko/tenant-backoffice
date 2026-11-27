import { z } from 'zod';

import type { components } from '@/api/schema';
import { dateParam, enumParam, parseSearch, uuidParam } from '@/lib/searchParams';

export type OperationKind = components['schemas']['OperationKind'];

export const OPERATION_KINDS = ['credit', 'debit', 'transfer_out', 'transfer_in'] as const satisfies readonly OperationKind[];

// Filters of the history live in the URL (EC-123). Every field is optional: a missing or invalid
// value means "no filter", so a damaged URL shows the full list instead of failing.
export const operationsSearchSchema = z.object({
  node: uuidParam,
  kind: enumParam(OPERATION_KINDS),
  from: dateParam,
  to: dateParam,
});

export type OperationsSearch = z.infer<typeof operationsSearchSchema>;

export function parseOperationsSearch(raw: unknown): OperationsSearch {
  return parseSearch(operationsSearchSchema, raw);
}
