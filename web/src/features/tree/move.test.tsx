import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { delay, http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { setSession } from '@/auth/session';
import { baseUrl, problemResponse, resetAuthState, tokenBody } from '@/test/harness';

import { MoveNodeDialog } from './MoveNodeDialog';
import { NodeTree } from './NodeTree';
import { installBackend, node, renderInApp, uuid, type FakeNode } from './testkit';

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
const C = 4;
const A1 = 5;
const A2 = 6;
const R2 = 7;
const C1 = 8;
const D = 9;

// Alpha One (A1) lives under Alpha (A). The server allows only Gamma and Delta as new parents:
// Alpha Two and Root Two exist, but are not valid targets.
function fixture(): FakeNode[] {
  return [
    node(R1, 'Root One', null),
    node(A, 'Alpha', R1),
    node(C, 'Gamma', R1),
    node(D, 'Delta', R1),
    node(A1, 'Alpha One', A),
    node(A2, 'Alpha Two', A),
    node(C1, 'Gamma One', C),
    node(R2, 'Root Two', null),
  ];
}

const targets = { [uuid(A1)]: [uuid(C), uuid(D)] };
const moveUrl = `${baseUrl}/nodes/${uuid(A1)}/move`;

function setup(extra: Partial<Parameters<typeof installBackend>[1]> = {}) {
  const backend = installBackend(server, {
    nodes: fixture(),
    roots: [uuid(R1), uuid(R2)],
    pageSize: 10,
    moveTargets: targets,
    ...extra,
  });
  const onClose = vi.fn();
  return { backend, onClose };
}

function renderDialog(onClose: () => void) {
  return renderInApp(<MoveNodeDialog nodeId={uuid(A1)} open onClose={onClose} />);
}

async function openAndPick(name: string) {
  const dialog = await screen.findByRole('dialog');
  await userEvent.click(await within(dialog).findByRole('radio', { name: new RegExp(name) }));
  return dialog;
}

describe('move dialog', () => {
  it('[T-W-06] offers only the targets of GET /nodes/{id}/move-targets', async () => {
    const { backend, onClose } = setup();
    renderDialog(onClose);

    const dialog = await screen.findByRole('dialog', { name: 'Move node' });
    await within(dialog).findByRole('radio', { name: /Gamma/ });
    const names = within(dialog)
      .getAllByRole('radio')
      .map((radio) => radio.closest('label')?.textContent);
    expect(names).toEqual(['Gammacompany', 'Deltacompany']);
    expect(screen.queryByText('Alpha Two')).toBeNull();
    expect(screen.queryByText('Root Two')).toBeNull();
    expect(backend.find('GET', `/nodes/${uuid(A1)}/move-targets`)).toHaveLength(1);
    // No list of nodes is read to build the choice.
    expect(backend.calls.filter((c) => c.path.endsWith('/children'))).toHaveLength(0);
    expect(within(dialog).getByRole('button', { name: 'Move' }).hasAttribute('disabled')).toBe(true);
  });

  it('searches the targets on the server with the entered text', async () => {
    const { backend, onClose } = setup();
    renderDialog(onClose);
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByRole('radio', { name: /Gamma/ });

    await userEvent.type(within(dialog).getByRole('searchbox', { name: 'Search new parent' }), 'del');

    await waitFor(() => {
      expect(within(dialog).queryByRole('radio', { name: /Gamma/ })).toBeNull();
    });
    expect(await within(dialog).findByRole('radio', { name: /Delta/ })).toBeTruthy();
    expect(backend.find('GET', `/nodes/${uuid(A1)}/move-targets`).map((c) => c.query.q)).toEqual([undefined, 'del']);
  });

  it('sends only the chosen parent, closes and reads the old and new parents again', async () => {
    const { backend, onClose } = setup();
    let body: unknown;
    server.use(
      http.post(moveUrl, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: uuid(A1) });
      }),
    );
    renderInApp(
      <>
        <NodeTree />
        <MoveNodeDialog nodeId={uuid(A1)} open onClose={onClose} />
      </>,
    );
    await userEvent.click(await screen.findByRole('button', { name: 'Expand Root One' }));
    // The modal dialog hides the page behind it from the accessibility tree: query with hidden.
    const tree = await screen.findByRole('tree', { hidden: true });
    await userEvent.click(await within(tree).findByRole('button', { name: 'Expand Alpha', hidden: true }));
    await userEvent.click(await within(tree).findByRole('button', { name: 'Expand Gamma', hidden: true }));
    await within(tree).findByRole('treeitem', { name: 'Gamma One', hidden: true });
    const dialog = await openAndPick('Gamma');
    expect(within(dialog).getByText('Move under').parentElement?.textContent).toContain('Gamma');

    await userEvent.click(within(dialog).getByRole('button', { name: 'Move' }));

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    expect(body).toEqual({ newParentId: uuid(C) });
    await waitFor(() => {
      expect(backend.find('GET', `/nodes/${uuid(A)}/children`)).toHaveLength(2);
      expect(backend.find('GET', `/nodes/${uuid(C)}/children`)).toHaveLength(2);
    });
    expect(backend.find('GET', `/nodes/${uuid(A1)}`).length).toBeGreaterThanOrEqual(2);
  });

  it.each([
    [422, 'move_cycle', 'A node cannot be moved into itself or its descendants.'],
    [409, 'name_taken', 'This name is already used here.'],
  ])('[T-W-06] %i %s shows the dictionary message and reads the tree again', async (status, code, text) => {
    const ghost = uuid(900);
    // `parentId` lies; the parent of the node is the entry before the last breadcrumb.
    const { backend, onClose } = setup({ parentIdOverride: { [uuid(A1)]: ghost } });
    server.use(http.post(moveUrl, () => problemResponse(status, code, {}, `internal detail ${uuid(901)} SELECT`)));
    renderInApp(
      <>
        <NodeTree />
        <MoveNodeDialog nodeId={uuid(A1)} open onClose={onClose} />
      </>,
    );
    await userEvent.click(await screen.findByRole('button', { name: 'Expand Root One' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Expand Alpha', hidden: true }));
    await screen.findByRole('treeitem', { name: 'Alpha Two', hidden: true });
    await waitFor(() => {
      expect(backend.find('GET', `/nodes/${uuid(A1)}`)).toHaveLength(1);
    });
    const dialog = await openAndPick('Delta');

    await userEvent.click(within(dialog).getByRole('button', { name: 'Move' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toBe(text);
    expect(document.body.textContent).not.toContain('SELECT');
    expect(document.body.textContent).not.toContain(uuid(901));
    expect(onClose).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(backend.find('GET', `/nodes/${uuid(A)}/children`)).toHaveLength(2);
    });
    expect(backend.calls.some((c) => c.path.includes(ghost))).toBe(false);
    // The dialog stays usable after a refusal.
    expect(within(dialog).getByRole('button', { name: 'Move' }).hasAttribute('disabled')).toBe(false);
  });

  it('[T-W-06] a double click on Move sends one request', async () => {
    const { onClose } = setup();
    let posts = 0;
    server.use(
      http.post(moveUrl, async () => {
        posts += 1;
        await delay(80);
        return HttpResponse.json({ id: uuid(A1) });
      }),
    );
    renderDialog(onClose);
    const dialog = await openAndPick('Gamma');

    await userEvent.dblClick(within(dialog).getByRole('button', { name: 'Move' }));

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    expect(posts).toBe(1);
  });

  it('shows the refusal of the target list (a root cannot be moved) and offers no choice', async () => {
    const { onClose } = setup();
    server.use(
      http.get(`${baseUrl}/nodes/${uuid(A1)}/move-targets`, () => problemResponse(422, 'root_immutable')),
    );
    renderDialog(onClose);

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toBe('A root node cannot be moved or deleted.');
    expect(screen.queryAllByRole('radio')).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Move' }).hasAttribute('disabled')).toBe(true);
  });

  it('Cancel closes without a request', async () => {
    const { backend, onClose } = setup();
    renderDialog(onClose);
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByRole('radio', { name: /Gamma/ });

    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(backend.calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('renders nothing while closed and asks for no targets', () => {
    const { backend, onClose } = setup();
    renderInApp(<MoveNodeDialog nodeId={uuid(A1)} open={false} onClose={onClose} />);

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(backend.calls).toHaveLength(0);
  });

  it('[T-W-09] a long target name is shown as text with the full name in title', async () => {
    const name = Array.from(`שלום<b>x</b>${'á̖'.repeat(30)}`).slice(0, 100).join('');
    const nodes = [...fixture().filter((n) => n.id !== uuid(D)), node(D, name, R1)];
    const { onClose } = setup({ nodes });
    const { container } = renderDialog(onClose);

    const label = await screen.findByTitle(name);
    expect(label.textContent).toBe(name);
    expect(label.classList.contains('truncate')).toBe(true);
    expect(container.querySelector('b')).toBeNull();
    expect(document.body.querySelector('dialog b')).toBeNull();
  });
});
