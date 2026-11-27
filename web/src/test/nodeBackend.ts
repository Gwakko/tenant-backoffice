import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { render } from '@testing-library/react';
import { createElement } from 'react';
import { http, HttpResponse } from 'msw';

import type { Node, Permission, Role, SettingItem } from '@/api/resources';
import { setSession } from '@/auth/session';

import { baseUrl, createTestApp, problemResponse, tokenBody } from './harness';

// A small in-memory backend for the node card tests (MSW handlers + counters of the requests).
export const ids = {
  root: '0190f2a0-0000-7000-8000-00000000a001',
  node: '0190f2a0-0000-7000-8000-00000000a002',
  sibling: '0190f2a0-0000-7000-8000-00000000a003',
  user: '0190f2a0-0000-7000-8000-00000000b001',
} as const;

export const ALL_PERMISSIONS: Permission[] = [
  'node.read', 'settings.read', 'balance.read', 'operation.read', 'report.read', 'audit.read', 'member.read',
  'node.create', 'node.rename', 'node.move', 'node.deactivate', 'settings.write', 'balance.credit',
  'balance.debit', 'balance.transfer', 'member.manage', 'node.delete',
];

export interface RecordedRequest {
  method: string;
  path: string;
  headers: Headers;
  body: unknown;
}

export interface BackendState {
  role: Role;
  permissions: Permission[];
  nodeName: string;
  nodeType: Node['type'];
  amount: string;
  currency: string;
  settings: SettingItem[];
  members: { userId: string; email: string; role: Role; nodeId: string; inheritedFrom: string | null; grantedAt: string }[];
  hits: Record<string, number>;
  requests: RecordedRequest[];
}

export function makeState(overrides: Partial<BackendState> = {}): BackendState {
  return {
    role: 'owner',
    permissions: ALL_PERMISSIONS,
    nodeName: 'Alpha Company',
    nodeType: 'company',
    amount: '1050',
    currency: 'EUR',
    settings: [],
    members: [],
    hits: {},
    requests: [],
    ...overrides,
  };
}

export function nodeBody(state: BackendState, id: string = ids.node): Node {
  const isRoot = id === ids.root;
  return {
    id,
    type: isRoot ? 'partner' : state.nodeType,
    parentId: isRoot ? null : ids.root,
    name: isRoot ? 'Root Partner' : state.nodeName,
    status: 'active',
    effectiveActive: true,
    childrenCount: 0,
    breadcrumbs: isRoot
      ? [{ id: ids.root, name: 'Root Partner', type: 'partner' }]
      : [
          { id: ids.root, name: 'Root Partner', type: 'partner' },
          { id, name: state.nodeName, type: state.nodeType },
        ],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  };
}

async function record(state: BackendState, request: Request, key: string): Promise<RecordedRequest> {
  state.hits[key] = (state.hits[key] ?? 0) + 1;
  const text = await request.clone().text();
  const entry: RecordedRequest = {
    method: request.method,
    path: new URL(request.url).pathname,
    headers: request.headers,
    body: text === '' ? null : (JSON.parse(text) as unknown),
  };
  state.requests.push(entry);
  return entry;
}

export function operationBody(kind: 'credit' | 'debit', nodeId: string, amount: string) {
  return {
    id: '0190f2a0-0000-7000-8000-00000000c001',
    nodeId,
    kind,
    amount,
    balanceAfter: '1000',
    transferId: null,
    counterpartyNodeId: null,
    comment: null,
    actorId: ids.user,
    createdAt: '2026-01-02T00:00:00Z',
  };
}

// Reads that every node screen makes. Tests add their own handlers for the writes (they win
// because they are listed first with server.use).
export function readHandlers(state: BackendState) {
  return [
    http.get(`${baseUrl}/nodes/:id`, async ({ request, params }) => {
      await record(state, request, 'GET node');
      return HttpResponse.json(nodeBody(state, String(params.id)));
    }),
    http.get(`${baseUrl}/me/permissions`, async ({ request }) => {
      await record(state, request, 'GET perms');
      const nodeId = new URL(request.url).searchParams.get('nodeId') ?? ids.node;
      return HttpResponse.json({ nodeId, role: state.role, permissions: state.permissions });
    }),
    http.get(`${baseUrl}/nodes/:id/balance`, async ({ request, params }) => {
      await record(state, request, 'GET balance');
      return HttpResponse.json({
        nodeId: String(params.id),
        amount: state.amount,
        currency: state.currency,
        updatedAt: '2026-01-02T00:00:00Z',
      });
    }),
    http.get(`${baseUrl}/nodes/:id/children`, async ({ request }) => {
      await record(state, request, 'GET children');
      return HttpResponse.json({
        items: [{ id: ids.sibling, type: 'unit', name: 'Beta Unit', status: 'active', childrenCount: 0 }],
        nextCursor: null,
      });
    }),
    http.get(`${baseUrl}/nodes/:id/settings`, async ({ request }) => {
      await record(state, request, 'GET settings');
      return HttpResponse.json({ items: state.settings });
    }),
    http.get(`${baseUrl}/nodes/:id/members`, async ({ request }) => {
      await record(state, request, 'GET members');
      const inherited = new URL(request.url).searchParams.get('inherited') === 'true';
      return HttpResponse.json({
        items: state.members.filter((m) => inherited || m.inheritedFrom === null),
        nextCursor: null,
      });
    }),
    http.get(`${baseUrl}/me`, async ({ request }) => {
      await record(state, request, 'GET me');
      return HttpResponse.json({
        id: ids.user,
        email: 'ann@example.test',
        accessRoots: [{ nodeId: ids.root, name: 'Root Partner', type: 'partner', role: state.role }],
      });
    }),
  ];
}

export { record, problemResponse };

export function renderApp(path: string) {
  setSession(tokenBody('t1'));
  const app = createTestApp(path);
  const view = render(
    createElement(QueryClientProvider, { client: app.queryClient }, createElement(RouterProvider, { router: app.router })),
  );
  return { ...app, view };
}
