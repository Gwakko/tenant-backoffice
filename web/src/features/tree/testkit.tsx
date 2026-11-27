import { QueryClientProvider } from '@tanstack/react-query';
import { RouterContextProvider, RouterProvider } from '@tanstack/react-router';
import { render } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import type { SetupServer } from 'msw/node';
import type { ReactNode } from 'react';

import type { components } from '@/api/schema';
import { baseUrl, createTestApp } from '@/test/harness';

// Test helpers of the tree feature: an in-memory backend behind MSW and render functions.

type NodeType = components['schemas']['NodeType'];

export interface FakeNode {
  id: string;
  name: string;
  type: NodeType;
  status: 'active' | 'inactive';
  parentId: string | null;
}

export interface Call {
  method: string;
  path: string;
  query: Readonly<Record<string, string>>;
}

export interface BackendOptions {
  nodes: FakeNode[];
  /** Ids of the access roots of the signed-in user. */
  roots: string[];
  pageSize?: number;
  /** Valid new parents per node, as the server would answer `move-targets`. */
  moveTargets?: Readonly<Record<string, string[]>>;
  /** `parentId` values to send in `GET /nodes/{id}` instead of the real ones (a hostile or buggy server). */
  parentIdOverride?: Readonly<Record<string, string | null>>;
}

export interface Backend {
  calls: Call[];
  count: (method: string, path: string) => number;
  find: (method: string, path: string) => Call[];
}

export const uuid = (n: number): string => `00000000-0000-4000-8000-${n.toString(16).padStart(12, '0')}`;

export function node(n: number, name: string, parent: number | null, extra: Partial<FakeNode> = {}): FakeNode {
  return { id: uuid(n), name, type: 'company', status: 'active', parentId: parent === null ? null : uuid(parent), ...extra };
}

function pageOf<T>(items: T[], cursor: string | null, size: number) {
  const offset = cursor === null ? 0 : Number(cursor.replace('offset:', ''));
  const next = offset + size;
  return { items: items.slice(offset, next), nextCursor: next < items.length ? `offset:${String(next)}` : null };
}

export function installBackend(server: SetupServer, options: BackendOptions): Backend {
  const { nodes, roots } = options;
  const size = options.pageSize ?? 2;
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const calls: Call[] = [];
  const record = (request: Request) => {
    const url = new URL(request.url);
    calls.push({ method: request.method, path: url.pathname.replace(/^\/api\/v1/, ''), query: Object.fromEntries(url.searchParams) });
  };
  const summary = (n: FakeNode) => ({
    id: n.id,
    type: n.type,
    name: n.name,
    status: n.status,
    childrenCount: nodes.filter((c) => c.parentId === n.id).length,
  });
  const crumbs = (n: FakeNode) => {
    const chain: FakeNode[] = [];
    for (let cur: FakeNode | undefined = n; cur !== undefined; cur = cur.parentId === null ? undefined : byId.get(cur.parentId)) {
      chain.unshift(cur);
      if (roots.includes(cur.id)) break;
    }
    return chain.map((c) => ({ id: c.id, name: c.name, type: c.type }));
  };
  const missing = () =>
    HttpResponse.json(
      { type: 'about:blank', title: 'not_found', status: 404, code: 'not_found', requestId: 'req-test-0001' },
      { status: 404, headers: { 'Content-Type': 'application/problem+json' } },
    );

  server.use(
    http.get(`${baseUrl}/me`, ({ request }) => {
      record(request);
      return HttpResponse.json({
        id: uuid(9999),
        email: 'ann@example.test',
        accessRoots: roots.flatMap((id) => {
          const n = byId.get(id);
          return n ? [{ nodeId: n.id, name: n.name, type: n.type, role: 'owner' }] : [];
        }),
      });
    }),
    http.get(`${baseUrl}/nodes/:id/children`, ({ request, params }) => {
      record(request);
      const url = new URL(request.url);
      const status = url.searchParams.get('status') ?? 'all';
      const list = nodes.filter((n) => n.parentId === params.id && (status === 'all' || n.status === status));
      return HttpResponse.json(pageOf(list.map(summary), url.searchParams.get('cursor'), size));
    }),
    http.get(`${baseUrl}/nodes/:id/move-targets`, ({ request, params }) => {
      record(request);
      const url = new URL(request.url);
      const q = (url.searchParams.get('q') ?? '').toLowerCase();
      const ids = options.moveTargets?.[String(params.id)] ?? [];
      const list = ids.flatMap((id) => byId.get(id) ?? []).filter((n) => n.name.toLowerCase().includes(q));
      return HttpResponse.json(pageOf(list.map(summary), url.searchParams.get('cursor'), size));
    }),
    http.get(`${baseUrl}/nodes/:id`, ({ request, params }) => {
      record(request);
      const n = byId.get(String(params.id));
      if (!n) return missing();
      const override = options.parentIdOverride;
      const parentId = override && Object.hasOwn(override, n.id) ? (override[n.id] ?? null) : roots.includes(n.id) ? null : n.parentId;
      return HttpResponse.json({
        id: n.id,
        type: n.type,
        parentId,
        name: n.name,
        status: n.status,
        effectiveActive: n.status === 'active',
        childrenCount: nodes.filter((c) => c.parentId === n.id).length,
        breadcrumbs: crumbs(n),
        createdAt: '2026-01-01T00:00:00Z',
        updatedAt: '2026-01-01T00:00:00Z',
      });
    }),
  );

  return {
    calls,
    count: (method, path) => calls.filter((c) => c.method === method && c.path === path).length,
    find: (method, path) => calls.filter((c) => c.method === method && c.path === path),
  };
}

// The real router and Query cache, but only the given component is rendered (no routes below `/`).
export function renderInApp(ui: ReactNode, path = '/') {
  const app = createTestApp(path);
  const view = render(
    <QueryClientProvider client={app.queryClient}>
      <RouterContextProvider router={app.router}>{ui}</RouterContextProvider>
    </QueryClientProvider>,
  );
  return { ...app, ...view };
}

// The whole application, with the sidebar of the root layout.
export function renderFullApp(path: string) {
  const app = createTestApp(path);
  render(
    <QueryClientProvider client={app.queryClient}>
      <RouterProvider router={app.router} />
    </QueryClientProvider>,
  );
  return app;
}
