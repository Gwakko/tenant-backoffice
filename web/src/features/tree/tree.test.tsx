import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { delay, http } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { setSession } from '@/auth/session';
import { baseUrl, problemResponse, resetAuthState, tokenBody } from '@/test/harness';

import { NodeTree } from './NodeTree';
import { nodeIdFromPath } from './path';
import { installBackend, node, renderFullApp, renderInApp, uuid, type FakeNode } from './testkit';

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
});
beforeEach(() => {
  resetAuthState();
  setSession(tokenBody('t1'));
});
afterEach(() => {
  server.resetHandlers();
  resetAuthState();
});
afterAll(() => {
  server.close();
});

const R1 = 1;
const A = 2;
const B = 3;
const C = 4;
const A1 = 5;
const A2 = 6;
const R2 = 7;

function fixture(): FakeNode[] {
  return [
    node(R1, 'Root One', null),
    node(A, 'Alpha', R1),
    node(B, 'Beta', R1, { status: 'inactive' }),
    node(C, 'Gamma', R1),
    node(A1, 'Alpha One', A),
    node(A2, 'Alpha Two', A),
    node(R2, 'Root Two', null),
  ];
}

const item = (name: string) => screen.getByRole('treeitem', { name });
const expand = (name: string) => userEvent.click(within(item(name)).getByRole('button', { name: `Expand ${name}` }));

describe('node tree', () => {
  it('lists the access roots of /me and loads children only when a node is expanded', async () => {
    const backend = installBackend(server, { nodes: fixture(), roots: [uuid(R1), uuid(R2)] });
    renderInApp(<NodeTree />);

    expect(await screen.findByRole('treeitem', { name: 'Root One' })).toBeTruthy();
    expect(screen.getByRole('treeitem', { name: 'Root Two' })).toBeTruthy();
    expect(backend.find('GET', `/nodes/${uuid(R1)}/children`)).toHaveLength(0);

    await expand('Root One');

    expect(await screen.findByRole('treeitem', { name: 'Alpha' })).toBeTruthy();
    expect(item('Beta').textContent).toContain('inactive');
    const [call] = backend.find('GET', `/nodes/${uuid(R1)}/children`);
    expect(call?.query).toMatchObject({ status: 'all', limit: '50' });
    expect(backend.find('GET', `/nodes/${uuid(R2)}/children`)).toHaveLength(0);
    // The sub-levels are lazy too: a child is not read before it is opened.
    expect(backend.find('GET', `/nodes/${uuid(A)}/children`)).toHaveLength(0);
  });

  it('"Load more" asks for the next page with the cursor and keeps the pages already shown', async () => {
    const backend = installBackend(server, { nodes: fixture(), roots: [uuid(R1)], pageSize: 2 });
    renderInApp(<NodeTree />);
    await screen.findByRole('treeitem', { name: 'Root One' });
    await expand('Root One');
    await screen.findByRole('treeitem', { name: 'Alpha' });
    expect(screen.queryByRole('treeitem', { name: 'Gamma' })).toBeNull();

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));

    expect(await screen.findByRole('treeitem', { name: 'Gamma' })).toBeTruthy();
    expect(screen.getAllByRole('treeitem', { name: 'Alpha' })).toHaveLength(1);
    expect(screen.getByRole('treeitem', { name: 'Beta' })).toBeTruthy();
    const calls = backend.find('GET', `/nodes/${uuid(R1)}/children`);
    expect(calls.map((c) => c.query.cursor)).toEqual([undefined, 'offset:2']);
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('the status filter reads the children again with that status', async () => {
    const backend = installBackend(server, { nodes: fixture(), roots: [uuid(R1)], pageSize: 10 });
    renderInApp(<NodeTree />);
    await screen.findByRole('treeitem', { name: 'Root One' });
    await expand('Root One');
    await screen.findByRole('treeitem', { name: 'Alpha' });

    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'inactive');

    await waitFor(() => {
      expect(screen.queryByRole('treeitem', { name: 'Alpha' })).toBeNull();
    });
    expect(screen.getByRole('treeitem', { name: 'Beta' })).toBeTruthy();
    expect(backend.find('GET', `/nodes/${uuid(R1)}/children`).map((c) => c.query.status)).toEqual(['all', 'inactive']);
  });

  it('works from the keyboard: arrows, expand and collapse, Enter opens the node', async () => {
    installBackend(server, { nodes: fixture(), roots: [uuid(R1), uuid(R2)], pageSize: 10 });
    const app = renderInApp(<NodeTree />);
    await screen.findByRole('treeitem', { name: 'Root One' });
    expect(screen.getByRole('tree')).toBeTruthy();

    await userEvent.tab(); // the status filter comes first
    await userEvent.tab();
    expect(document.activeElement).toBe(item('Root One'));
    expect(item('Root One').getAttribute('aria-expanded')).toBe('false');

    await userEvent.keyboard('{ArrowRight}');
    expect(await screen.findByRole('treeitem', { name: 'Alpha' })).toBeTruthy();
    expect(item('Root One').getAttribute('aria-expanded')).toBe('true');

    await userEvent.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(item('Alpha'));
    await userEvent.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(item('Beta'));
    await userEvent.keyboard('{ArrowUp}{ArrowLeft}');
    expect(document.activeElement).toBe(item('Root One'));

    await userEvent.keyboard('{End}');
    expect(document.activeElement).toBe(item('Root Two'));
    await userEvent.keyboard('{Home}{ArrowLeft}');
    expect(screen.queryByRole('treeitem', { name: 'Alpha' })).toBeNull();
    expect(item('Root One').getAttribute('aria-expanded')).toBe('false');

    await userEvent.keyboard('{Enter}');
    expect(app.router.history.location.pathname).toBe(`/nodes/${uuid(R1)}`);
  });

  it('marks the current node and opens its ancestors, taking them from the breadcrumbs', async () => {
    installBackend(server, { nodes: fixture(), roots: [uuid(R1)], pageSize: 10 });
    renderInApp(<NodeTree currentNodeId={uuid(A1)} />);

    const current = await screen.findByRole('treeitem', { name: 'Alpha One' });
    expect(current.getAttribute('aria-current')).toBe('page');
    expect(current.getAttribute('aria-selected')).toBe('true');
    expect(item('Alpha').getAttribute('aria-expanded')).toBe('true');
    expect(item('Alpha').getAttribute('aria-current')).toBeNull();
  });

  it('[T-W-06] the parent of an access root is not taken from parentId, which is null there', async () => {
    // A hostile or buggy answer: the access root claims a parent, a child claims a foreign one.
    const ghost = uuid(900);
    const backend = installBackend(server, {
      nodes: fixture(),
      roots: [uuid(R1)],
      pageSize: 10,
      parentIdOverride: { [uuid(R1)]: ghost, [uuid(A)]: ghost },
    });
    renderInApp(<NodeTree currentNodeId={uuid(A)} />);

    await screen.findByRole('treeitem', { name: 'Alpha' });
    expect(item('Root One').getAttribute('aria-expanded')).toBe('true');
    expect(backend.calls.some((c) => c.path.includes(ghost))).toBe(false);
  });

  it('keeps the expanded state when the route changes, without reading the children again', async () => {
    const backend = installBackend(server, { nodes: fixture(), roots: [uuid(R1)], pageSize: 10 });
    const app = renderFullApp('/');
    await screen.findByRole('treeitem', { name: 'Root One' });
    await expand('Root One');
    await screen.findByRole('treeitem', { name: 'Alpha' });

    await act(async () => {
      await app.router.navigate({ to: '/', search: { page: '2' } as never });
    });

    expect(app.router.state.location.search).toEqual({ page: '2' });
    expect(screen.getByRole('treeitem', { name: 'Alpha' })).toBeTruthy();
    expect(backend.find('GET', `/nodes/${uuid(R1)}/children`)).toHaveLength(1);
  });

  it('the layout shows the tree for a signed-in user', async () => {
    installBackend(server, { nodes: fixture(), roots: [uuid(R1)], pageSize: 10 });
    renderFullApp('/');

    await screen.findByRole('treeitem', { name: 'Root One' });
    expect(within(screen.getByRole('complementary', { name: 'Node tree' })).getByRole('tree')).toBeTruthy();
  });

  it('reads the current node from the path of the node card and its tabs only', () => {
    expect(nodeIdFromPath(`/nodes/${uuid(A1)}`)).toBe(uuid(A1));
    expect(nodeIdFromPath(`/nodes/${uuid(A1)}/members`)).toBe(uuid(A1));
    expect(nodeIdFromPath('/nodes/not-an-id')).toBeUndefined();
    expect(nodeIdFromPath(`/other/nodes/${uuid(A1)}`)).toBeUndefined();
    expect(nodeIdFromPath('/')).toBeUndefined();
  });

  it('shows a dictionary message and a retry when the children cannot be read', async () => {
    installBackend(server, { nodes: fixture(), roots: [uuid(R1)] });
    server.use(
      http.get(`${baseUrl}/nodes/${uuid(R1)}/children`, async () => {
        await delay(5);
        return problemResponse(500, 'internal', {}, 'SELECT * FROM secrets /nodes/other-id');
      }),
    );
    renderInApp(<NodeTree />);
    await screen.findByRole('treeitem', { name: 'Root One' });
    await expand('Root One');

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toBe('Something went wrong on the server. Try again.');
    expect(document.body.textContent).not.toContain('SELECT');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
  });

  it('[T-W-09] a 100-character name with right-to-left and combining characters is shown as text with a title', async () => {
    const hostile = `\u05E9\u05DC\u05D5\u05DD<img src=x onerror=alert(1)>${'e\u0301\u0323'.repeat(10)}${'\u0645\u0631\u062D\u0628\u0627'.repeat(8)}`;
    const name = Array.from(hostile).slice(0, 100).join('');
    expect(Array.from(name)).toHaveLength(100);
    installBackend(server, {
      nodes: [node(R1, name, null), node(A, name, R1)],
      roots: [uuid(R1)],
      pageSize: 10,
    });
    const { container } = renderInApp(<NodeTree />);
    await screen.findByRole('treeitem', { name });
    await expand(name);
    await waitFor(() => {
      expect(screen.getAllByTitle(name)).toHaveLength(2);
    });

    for (const label of screen.getAllByTitle(name)) {
      expect(label.textContent).toBe(name);
      expect(label.getAttribute('dir')).toBe('auto');
      expect(label.classList.contains('truncate')).toBe(true);
      expect(label.children).toHaveLength(0);
    }
    // The markup in the name is text: no element was created from it.
    expect(container.querySelector('img')).toBeNull();
  });
});
