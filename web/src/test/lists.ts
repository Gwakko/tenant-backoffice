import { http, HttpResponse } from 'msw';

import { baseUrl } from './harness';
import { ids, record, type BackendState } from './nodeBackend';

// Helpers for the tests of the list screens (operations, audit, reports): fake rows and a handler
// that records every request and lets the test answer by query string.

export function uuidN(n: number): string {
  return `0190f2a0-0000-7000-8000-${n.toString(16).padStart(12, '0')}`;
}

export function operationItem(n: number, over: Record<string, unknown> = {}) {
  return {
    id: uuidN(0x1000 + n),
    nodeId: ids.node,
    nodeName: 'Alpha Company',
    kind: 'credit',
    amount: '1050',
    balanceAfter: '1050',
    transferId: null,
    counterpartyNodeId: null,
    comment: null,
    actorId: ids.user,
    createdAt: '2026-03-01T10:20:30Z',
    ...over,
  };
}

export function auditItem(n: number, over: Record<string, unknown> = {}) {
  return {
    id: uuidN(0x2000 + n),
    occurredAt: '2026-03-01T10:20:30Z',
    actorId: ids.user,
    actorEmail: 'ann@example.test',
    nodeId: ids.node,
    nodeName: 'Alpha Company',
    action: 'node.renamed',
    targetType: 'node',
    targetId: ids.node,
    before: { name: 'Old' },
    after: { name: 'New' },
    requestId: 'req-1',
    ...over,
  };
}

export function page(items: unknown[], nextCursor: string | null = null) {
  return HttpResponse.json({ items, nextCursor });
}

export interface ListHandler {
  handler: ReturnType<typeof http.get>;
  /** The query strings of the requests received, in order. */
  queries: URLSearchParams[];
}

/** GET handler for `path` that records every request and lets the test answer by its query. */
export function listHandler(path: string, state: BackendState, answer: (query: URLSearchParams, n: number) => Response): ListHandler {
  const queries: URLSearchParams[] = [];
  const handler = http.get(`${baseUrl}${path}`, async ({ request }) => {
    await record(state, request, `GET ${path}`);
    const query = new URL(request.url).searchParams;
    queries.push(query);
    return answer(query, queries.length);
  });
  return { handler, queries };
}
