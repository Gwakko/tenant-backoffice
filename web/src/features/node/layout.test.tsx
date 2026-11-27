import { act, screen, waitFor } from '@testing-library/react';
import { http } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { keys } from '@/api/keys';
import { baseUrl, problemResponse, resetAuthState } from '@/test/harness';
import { ALL_PERMISSIONS, ids, makeState, readHandlers, record, renderApp } from '@/test/nodeBackend';

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
});
beforeEach(() => {
  resetAuthState();
});
afterEach(() => {
  server.resetHandlers();
  resetAuthState();
});
afterAll(() => {
  server.close();
});

const nodePath = `/nodes/${ids.node}`;

function tab(name: string): HTMLElement | null {
  const nav = screen.getByRole('navigation', { name: 'Node sections' });
  return Array.from(nav.querySelectorAll<HTMLElement>('a')).find((a) => a.textContent === name) ?? null;
}

describe('node layout', () => {
  it('[T-W-02] a 404 on the node route shows "Not found or no access" and reads `me` again', async () => {
    const state = makeState();
    let withdrawn = false;
    server.use(
      http.get(`${baseUrl}/nodes/${ids.node}`, async ({ request }) => {
        await record(state, request, 'GET node');
        return withdrawn ? problemResponse(404, 'not_found') : undefined;
      }),
      ...readHandlers(state),
    );
    const app = renderApp(nodePath);
    await screen.findByRole('heading', { name: 'Alpha Company' });
    await waitFor(() => {
      expect(state.hits['GET me']).toBeGreaterThanOrEqual(1);
    });
    const meBefore = state.hits['GET me'] ?? 0;

    // The access is withdrawn while the page is open (EC-118): the next read of the node is 404.
    withdrawn = true;
    await act(async () => {
      await app.queryClient.invalidateQueries({ queryKey: keys.node(ids.node) });
    });

    expect(await screen.findByRole('heading', { name: 'Not found or no access' })).toBeTruthy();
    await waitFor(() => {
      expect(state.hits['GET me']).toBeGreaterThan(meBefore);
    });
    expect(await screen.findByRole('link', { name: 'Go to Root Partner' })).toBeTruthy();
  });

  it('the Reports and Audit tabs follow the rights; Operations is always there', async () => {
    const state = makeState({ role: 'viewer', permissions: ['node.read', 'operation.read'] });
    server.use(...readHandlers(state));
    renderApp(nodePath);
    await screen.findByRole('heading', { name: 'Alpha Company' });
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(0);
    });
    expect(tab('Operations')).not.toBeNull();
    expect(tab('Reports')).toBeNull();
    expect(tab('Audit')).toBeNull();
  });

  it('the Reports tab shows with report.read and the Audit tab with audit.read', async () => {
    const withReport = makeState({ permissions: ALL_PERMISSIONS.filter((p) => p !== 'audit.read') });
    server.use(...readHandlers(withReport));
    renderApp(nodePath);
    await screen.findByRole('heading', { name: 'Alpha Company' });
    await waitFor(() => {
      expect(tab('Reports')).not.toBeNull();
    });
    expect(tab('Audit')).toBeNull();
  });

  it('the Audit tab shows with audit.read alone', async () => {
    const withAudit = makeState({ permissions: ['node.read', 'audit.read'] });
    server.use(...readHandlers(withAudit));
    renderApp(nodePath);
    await screen.findByRole('heading', { name: 'Alpha Company' });
    await waitFor(() => {
      expect(tab('Audit')).not.toBeNull();
    });
    expect(tab('Reports')).toBeNull();
  });
});
