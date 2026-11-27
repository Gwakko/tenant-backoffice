import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { keyPrefixes } from '@/api/keys';
import { problemResponse, resetAuthState } from '@/test/harness';
import { listHandler, operationItem, page, uuidN } from '@/test/lists';
import { ids, makeState, readHandlers, renderApp } from '@/test/nodeBackend';

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

const path = `/nodes/${ids.node}/operations`;

function rows(): HTMLElement[] {
  return within(screen.getByRole('table', { name: 'Operations' })).getAllByRole('row').slice(1);
}

describe('operations history', () => {
  it('[T-W-07] filters are written to the URL, requests carry them, and "back" restores the previous ones', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, () => page([operationItem(1)]));
    server.use(ops.handler, ...readHandlers(state));
    const app = renderApp(path);
    await screen.findByRole('table', { name: 'Operations' });
    expect(app.router.state.location.search).toEqual({});

    await userEvent.selectOptions(screen.getByLabelText('Kind'), 'Credit');
    await waitFor(() => {
      expect(app.router.state.location.search).toEqual({ kind: 'credit' });
    });
    await userEvent.selectOptions(screen.getByLabelText('Kind'), 'Debit');
    await waitFor(() => {
      expect(app.router.state.location.search).toEqual({ kind: 'debit' });
    });
    expect(ops.queries.at(-1)?.getAll('kind')).toEqual(['debit']);
    expect(ops.queries.at(-1)?.get('scopeNodeId')).toBe(ids.node);

    act(() => {
      app.router.history.back();
    });
    await waitFor(() => {
      expect(app.router.state.location.search).toEqual({ kind: 'credit' });
    });
    expect(screen.getByLabelText<HTMLSelectElement>('Kind').value).toBe('credit');

    act(() => {
      app.router.history.back();
    });
    await waitFor(() => {
      expect(app.router.state.location.search).toEqual({});
    });
    expect(screen.getByLabelText<HTMLSelectElement>('Kind').value).toBe('');
  });

  it('[T-W-07] a period filter is kept in the URL and sent as UTC timestamps', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, () => page([operationItem(1)]));
    server.use(ops.handler, ...readHandlers(state));
    const app = renderApp(path);
    await screen.findByRole('table', { name: 'Operations' });

    await userEvent.type(screen.getByLabelText('From (UTC date)'), '2026-02-01');
    await userEvent.type(screen.getByLabelText('To (UTC date, exclusive)'), '2026-03-01');
    await waitFor(() => {
      expect(app.router.state.location.search).toEqual({ from: '2026-02-01', to: '2026-03-01' });
    });
    await waitFor(() => {
      expect(ops.queries.at(-1)?.get('from')).toBe('2026-02-01T00:00:00Z');
    });
    expect(ops.queries.at(-1)?.get('to')).toBe('2026-03-01T00:00:00Z');
  });

  it('[T-W-07] garbage in the search params gives the defaults, not a crash, and never reaches the request', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, () => page([operationItem(1)]));
    server.use(ops.handler, ...readHandlers(state));
    renderApp(`${path}?kind=evil&node=not-a-uuid&from=2026-02-31&to=%5B1%2C2%5D&scopeNodeId=${uuidN(99)}&x=1`);

    await screen.findByRole('table', { name: 'Operations' });
    expect(screen.getByLabelText<HTMLSelectElement>('Kind').value).toBe('');
    expect(screen.getByLabelText<HTMLSelectElement>('Node').value).toBe('');
    const query = ops.queries[0];
    expect(query?.get('scopeNodeId')).toBe(ids.node);
    for (const name of ['kind', 'nodeId', 'from', 'to']) expect(query?.has(name), name).toBe(false);
  });

  it('[T-W-07] a search param that is not even a string does not break the screen', async () => {
    const state = makeState();
    server.use(listHandler('/operations', state, () => page([])).handler, ...readHandlers(state));
    renderApp(`${path}?kind=%5B%22credit%22%5D&from=12345&node=%7B%7D`);
    expect(await screen.findByText('No operations for these filters.')).toBeTruthy();
  });

  it('a start date after the end date is shown as an error and sends no request', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, () => page([operationItem(1)]));
    server.use(ops.handler, ...readHandlers(state));
    renderApp(`${path}?from=2026-03-01&to=2026-02-01`);
    expect(await screen.findByText('The start date must be before the end date.')).toBeTruthy();
    await waitFor(() => {
      expect(state.hits['GET balance']).toBeGreaterThan(0);
    });
    expect(ops.queries).toHaveLength(0);
  });

  it('shows amounts above 2^53 exactly, the kind, the counterparty and the time with its zone', async () => {
    const state = makeState();
    server.use(
      listHandler('/operations', state, () =>
        page([
          operationItem(1, { kind: 'credit', amount: '9007199254740993', balanceAfter: '9223372036854775807', comment: 'Top up' }),
          operationItem(2, {
            kind: 'transfer_out',
            amount: '500',
            balanceAfter: '100',
            counterpartyNodeId: ids.sibling,
            transferId: uuidN(7),
          }),
        ]),
      ).handler,
      ...readHandlers(state),
    );
    renderApp(path);
    await screen.findByRole('table', { name: 'Operations' });

    const [first, second = document.body] = rows();
    expect(first?.textContent).toContain('+90,071,992,547,409.93');
    expect(first?.textContent).toContain('92,233,720,368,547,758.07');
    expect(first?.textContent).toContain('Credit');
    expect(first?.textContent).toContain('Top up');
    expect(first?.textContent).toContain('2026-03-01 10:20:30 UTC');
    expect(second.textContent).toContain('-5.00');
    expect(second.textContent).toContain('Transfer out');
    expect(within(second).getByRole('link', { name: 'Beta Unit' }).getAttribute('href')).toBe(`/nodes/${ids.sibling}`);
    expect(screen.getByRole('columnheader', { name: 'Amount (EUR)' })).toBeTruthy();
  });

  it('"Load more" appends the next page; an overlapping page and a re-request show no duplicates', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, (query) =>
      query.get('cursor') === null
        ? page([operationItem(1, { comment: 'op 1' }), operationItem(2, { comment: 'op 2' })], 'c1')
        : page([operationItem(2, { comment: 'op 2' }), operationItem(3, { comment: 'op 3' })]),
    );
    server.use(ops.handler, ...readHandlers(state));
    const app = renderApp(path);
    await screen.findByRole('table', { name: 'Operations' });
    expect(rows()).toHaveLength(2);

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => {
      expect(rows()).toHaveLength(3);
    });
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();

    // A repeated request (the list is invalidated, both loaded pages are fetched again).
    const before = ops.queries.length;
    await act(async () => {
      await app.queryClient.invalidateQueries({ queryKey: keyPrefixes.operations() });
    });
    expect(ops.queries.length).toBeGreaterThan(before);
    expect(rows()).toHaveLength(3);
    const ids3 = rows().map((row) => row.textContent);
    expect(new Set(ids3).size).toBe(3);
  });

  it('an invalid cursor resets the list and loads it again from the first page', async () => {
    const state = makeState();
    let rejected = false;
    const ops = listHandler('/operations', state, (query) => {
      if (query.get('cursor') === null) return page([operationItem(1), operationItem(2)], 'stale');
      rejected = true;
      return problemResponse(400, 'invalid_cursor');
    });
    server.use(ops.handler, ...readHandlers(state));
    renderApp(path);
    await screen.findByRole('table', { name: 'Operations' });

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => {
      expect(rejected).toBe(true);
    });
    // The list is requested again without a cursor and is on screen again (no white screen).
    await waitFor(() => {
      expect(ops.queries.filter((q) => q.get('cursor') === null).length).toBeGreaterThanOrEqual(2);
    });
    expect(await screen.findByRole('table', { name: 'Operations' })).toBeTruthy();
    expect(rows()).toHaveLength(2);
  });

  it('a 403 shows the permission Alert and reads the permissions again', async () => {
    const state = makeState({ permissions: ['node.read'] });
    server.use(listHandler('/operations', state, () => problemResponse(403, 'forbidden')).handler, ...readHandlers(state));
    renderApp(path);
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('You do not have permission for this action.');
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThanOrEqual(2);
    });
  });

  it('a 429 names the Retry-After wait', async () => {
    const state = makeState();
    server.use(listHandler('/operations', state, () => problemResponse(429, 'rate_limited', { 'Retry-After': '7' })).handler, ...readHandlers(state));
    renderApp(path);
    expect((await screen.findByRole('alert')).textContent).toContain('Try again in 7 seconds.');
  });

  it('a 503 shows an Alert with Retry that loads the list', async () => {
    const state = makeState();
    const ops = listHandler('/operations', state, (_query, n) =>
      n === 1 ? problemResponse(503, 'db_unavailable') : page([operationItem(1)]),
    );
    server.use(ops.handler, ...readHandlers(state));
    renderApp(path);
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('temporarily unavailable');
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('table', { name: 'Operations' })).toBeTruthy();
  });

  it('a network failure shows an Alert with Retry', async () => {
    const state = makeState();
    server.use(
      listHandler('/operations', state, () => HttpResponse.error()).handler,
      ...readHandlers(state),
    );
    renderApp(path);
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('Cannot reach the server');
    expect(within(alert).getByRole('button', { name: 'Retry' })).toBeTruthy();
  });
});
