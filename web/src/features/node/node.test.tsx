import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { baseUrl, problemResponse, resetAuthState } from '@/test/harness';
import { ids, makeState, nodeBody, readHandlers, record, renderApp, type BackendState } from '@/test/nodeBackend';

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

// The submit button of the form inside the open dialog.
function submitButton(): HTMLElement {
  const button = document.querySelector<HTMLElement>('dialog form button[type=submit]');
  if (button === null) throw new Error('no form is open');
  return button;
}

function patchHandler(state: BackendState, answer: (name: string) => Response) {
  return http.patch(`${baseUrl}/nodes/:id`, async ({ request }) => {
    const entry = await record(state, request, 'PATCH node');
    return answer((entry.body as { name: string }).name);
  });
}

describe('node card', () => {
  it('shows the data of the node and the breadcrumbs from the answer', async () => {
    const state = makeState();
    server.use(...readHandlers(state));
    renderApp(nodePath);

    expect((await screen.findByRole('heading', { name: 'Alpha Company' })).getAttribute('title')).toBe('Alpha Company');
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumbs' });
    expect(crumbs.textContent).toContain('Root Partner');
    expect(crumbs.querySelector('a')?.getAttribute('href')).toBe(`/nodes/${ids.root}`);
  });

  it('[T-W-01] the action buttons are hidden without the right', async () => {
    const state = makeState({ role: 'viewer', permissions: ['node.read', 'balance.read', 'settings.read'] });
    server.use(...readHandlers(state));
    renderApp(nodePath);

    await screen.findByRole('heading', { name: 'Alpha Company' });
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(0);
    });
    for (const name of [/^Create/, 'Rename', 'Move', 'Deactivate', 'Delete', 'Credit', 'Debit', 'Transfer']) {
      expect(screen.queryByRole('button', { name }), String(name)).toBeNull();
    }
  });

  it('Delete follows the rights on the parent and is absent on an access root (its parentId is null)', async () => {
    const state = makeState();
    server.use(
      http.get(`${baseUrl}/nodes/${ids.root}`, async ({ request }) => {
        await record(state, request, 'GET node');
        return HttpResponse.json({ ...nodeBody(state, ids.root), parentId: null });
      }),
      ...readHandlers(state),
    );
    const root = renderApp(`/nodes/${ids.root}`);
    await screen.findByRole('heading', { name: 'Root Partner' });
    await screen.findByRole('button', { name: 'Rename' });
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    root.view.unmount();
    root.unwire();

    renderApp(nodePath);
    expect(await screen.findByRole('button', { name: 'Delete' })).toBeTruthy();
  });

  it('[T-W-01] a 403 on rename shows an Alert, keeps the data and reads the permissions again', async () => {
    const state = makeState();
    server.use(patchHandler(state, () => problemResponse(403, 'forbidden')), ...readHandlers(state));
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }));
    const field = await screen.findByLabelText('Name');
    await userEvent.clear(field);
    await userEvent.type(field, 'Gamma');
    const nodeReads = state.hits['GET node'] ?? 0;
    const permsReads = state.hits['GET perms'] ?? 0;
    await userEvent.click(submitButton());

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('You do not have permission for this action');
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(permsReads);
    });
    expect(state.hits['GET node']).toBe(nodeReads);
    expect(screen.getByRole('heading', { name: 'Alpha Company' })).toBeTruthy();
  });

  it('renames: the name is trimmed and the card shows the server answer after a re-read', async () => {
    const state = makeState();
    server.use(
      patchHandler(state, (name) => {
        state.nodeName = name;
        return HttpResponse.json({ ...nodeBody(state), name });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }));
    const field = await screen.findByLabelText('Name');
    await userEvent.clear(field);
    await userEvent.type(field, '  Gamma  ');
    await userEvent.click(submitButton());

    expect(await screen.findByRole('heading', { name: 'Gamma' })).toBeTruthy();
    expect(state.requests.find((r) => r.method === 'PATCH')?.body).toEqual({ name: 'Gamma' });
  });

  it('creates a child of the allowed type only; the name is 1..100 after trimming', async () => {
    const state = makeState();
    server.use(
      http.post(`${baseUrl}/nodes/:id/children`, async ({ request }) => {
        await record(state, request, 'POST child');
        return HttpResponse.json({ ...nodeBody(state, ids.sibling), type: 'unit', name: 'Delta' }, { status: 201 });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Create unit' }));
    const type = await screen.findByLabelText('Type');
    expect(Array.from(type.querySelectorAll('option')).map((o) => o.value)).toEqual(['unit']);

    await userEvent.type(screen.getByLabelText('Name'), '   ');
    await userEvent.click(submitButton());
    expect(await screen.findByText('Enter a name.')).toBeTruthy();

    await userEvent.clear(screen.getByLabelText('Name'));
    await userEvent.type(screen.getByLabelText('Name'), 'x'.repeat(101));
    await userEvent.click(submitButton());
    expect(await screen.findByText('The name is at most 100 characters.')).toBeTruthy();
    expect(state.hits['POST child']).toBeUndefined();

    await userEvent.clear(screen.getByLabelText('Name'));
    await userEvent.type(screen.getByLabelText('Name'), ' Delta ');
    await userEvent.click(submitButton());
    await waitFor(() => {
      expect(state.hits['POST child']).toBe(1);
    });
    expect(state.requests.find((r) => r.method === 'POST')?.body).toEqual({ type: 'unit', name: 'Delta' });
  });

  it('a unit cannot have children: no create button', async () => {
    const state = makeState({ nodeType: 'unit' });
    server.use(...readHandlers(state));
    renderApp(nodePath);

    await screen.findByRole('heading', { name: 'Alpha Company' });
    await screen.findByRole('button', { name: 'Rename' });
    expect(screen.queryByRole('button', { name: /^Create/ })).toBeNull();
  });

  it('a name conflict is shown with the text of its code', async () => {
    const state = makeState();
    server.use(patchHandler(state, () => problemResponse(409, 'name_taken')), ...readHandlers(state));
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }));
    await screen.findByLabelText('Name');
    await userEvent.click(submitButton());

    expect((await screen.findByRole('alert')).textContent).toContain('This name is already used here.');
  });

  it('the Move button opens the dialog supplied by the route', async () => {
    const state = makeState();
    server.use(
      http.get(`${baseUrl}/nodes/:id/move-targets`, () => HttpResponse.json({ items: [], nextCursor: null })),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Move' }));
    expect(await screen.findByRole('dialog')).toBeTruthy();
  });

  it('a 404 on the node shows "Not found or no access" and reads the user again', async () => {
    const state = makeState();
    server.use(
      http.get(`${baseUrl}/nodes/:id`, async ({ request }) => {
        await record(state, request, 'GET node');
        return problemResponse(404, 'not_found');
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    expect(await screen.findByRole('heading', { name: 'Not found or no access' })).toBeTruthy();
    await waitFor(() => {
      expect(state.hits['GET me']).toBeGreaterThanOrEqual(1);
    });
    expect(await screen.findByRole('link', { name: 'Go to Root Partner' })).toBeTruthy();
  });
});
