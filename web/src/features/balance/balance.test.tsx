import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { delay, http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { baseUrl, problemResponse, resetAuthState } from '@/test/harness';
import {
  ids,
  makeState,
  operationBody,
  readHandlers,
  record,
  renderApp,
  type BackendState,
} from '@/test/nodeBackend';

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

function keysSent(state: BackendState): (string | null)[] {
  return state.requests.filter((r) => r.method === 'POST').map((r) => r.headers.get('Idempotency-Key'));
}

async function openDebit(amount: string) {
  await userEvent.click(await screen.findByRole('button', { name: 'Debit' }));
  await userEvent.type(await screen.findByLabelText(/^Amount/), amount);
}

function debitHandler(state: BackendState, answer: () => Response | Promise<Response>) {
  return http.post(`${baseUrl}/nodes/:id/debits`, async ({ request }) => {
    await record(state, request, 'POST debit');
    return answer();
  });
}

describe('balance operations', () => {
  it('[T-W-04] a double click on the confirm button sends exactly one request', async () => {
    const state = makeState();
    server.use(
      debitHandler(state, async () => {
        await delay(60);
        return HttpResponse.json(operationBody('debit', ids.node, '1050'), { status: 201 });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await openDebit('10.50');
    await userEvent.dblClick(screen.getByRole('button', { name: 'Confirm debit' }));

    expect(await screen.findByText('Debit completed.')).toBeTruthy();
    expect(state.hits['POST debit']).toBe(1);
    expect(state.requests.find((r) => r.method === 'POST')?.body).toEqual({ amount: '1050' });
    expect(keysSent(state)[0]).toMatch(/^[A-Za-z0-9_\-:.]{8,128}$/);
  });

  it('[T-W-04] a resend after a network failure carries the same Idempotency-Key', async () => {
    const state = makeState();
    let calls = 0;
    server.use(
      debitHandler(state, () => {
        calls += 1;
        return calls === 1 ? HttpResponse.error() : HttpResponse.json(operationBody('debit', ids.node, '1050'), { status: 201 });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await openDebit('10.50');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('Cannot reach the server');

    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Debit completed.')).toBeTruthy();

    const sent = keysSent(state);
    expect(sent).toHaveLength(2);
    expect(sent[0]).toBe(sent[1]);
  });

  it('[T-W-04] a changed amount after a failure is another attempt with another key', async () => {
    const state = makeState();
    let calls = 0;
    server.use(
      debitHandler(state, () => {
        calls += 1;
        return calls === 1 ? HttpResponse.error() : HttpResponse.json(operationBody('debit', ids.node, '2000'), { status: 201 });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await openDebit('10.50');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    await screen.findByRole('alert');
    const amount = screen.getByLabelText(/^Amount/);
    await userEvent.clear(amount);
    await userEvent.type(amount, '20');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    await screen.findByText('Debit completed.');

    const sent = keysSent(state);
    expect(sent).toHaveLength(2);
    expect(sent[0]).not.toBe(sent[1]);
  });

  it('[T-W-04] a new opening of the dialog starts a new attempt with a new key', async () => {
    const state = makeState();
    server.use(
      debitHandler(state, () => HttpResponse.json(operationBody('debit', ids.node, '1000'), { status: 201 })),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await openDebit('10');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    await screen.findByText('Debit completed.');
    await waitFor(() => {
      expect(screen.queryByLabelText(/^Amount/)).toBeNull();
    });
    await openDebit('10');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    await waitFor(() => {
      expect(state.hits['POST debit']).toBe(2);
    });

    const sent = keysSent(state);
    expect(sent[0]).not.toBe(sent[1]);
  });

  it('[T-W-04] a replayed answer is a success, not a duplicate warning', async () => {
    const state = makeState();
    server.use(
      debitHandler(state, () =>
        HttpResponse.json(operationBody('debit', ids.node, '1050'), { status: 201, headers: { 'Idempotent-Replayed': 'true' } }),
      ),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await openDebit('10.50');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));

    expect(await screen.findByText('Debit completed.')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('[T-W-04] 422 idempotency_key_reused shows a clear message', async () => {
    const state = makeState();
    server.use(debitHandler(state, () => problemResponse(422, 'idempotency_key_reused')), ...readHandlers(state));
    renderApp(nodePath);

    await openDebit('10');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('already used with different data');
  });

  it('[T-W-04] 429 tells how long to wait', async () => {
    const state = makeState();
    server.use(debitHandler(state, () => problemResponse(429, 'rate_limited', { 'Retry-After': '7' })), ...readHandlers(state));
    renderApp(nodePath);

    await openDebit('10');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));

    expect((await screen.findByRole('alert')).textContent).toContain('7 seconds');
  });

  it('[T-W-04] the amount is checked in the form: "10.505" and zero never reach the server', async () => {
    const state = makeState();
    server.use(debitHandler(state, () => HttpResponse.json(operationBody('debit', ids.node, '1'), { status: 201 })), ...readHandlers(state));
    renderApp(nodePath);

    await openDebit('10.505');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    expect(await screen.findByText('Use at most two digits after the decimal point.')).toBeTruthy();
    const amount = screen.getByLabelText(/^Amount/);
    await userEvent.clear(amount);
    await userEvent.type(amount, '0');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));
    expect(await screen.findByText('The amount must be greater than zero.')).toBeTruthy();
    expect(state.hits['POST debit']).toBeUndefined();
  });

  it('shows the balance with the currency from the answer', async () => {
    const state = makeState({ amount: '123456', currency: 'USD' });
    server.use(...readHandlers(state));
    renderApp(nodePath);

    expect((await screen.findByTestId('balance-amount')).textContent).toContain('1,234.56');
    expect(screen.getByTestId('balance-amount').textContent).toContain('USD');
  });

  it('a transfer picks the recipient from the list and sends its id with one key', async () => {
    const state = makeState();
    server.use(
      http.post(`${baseUrl}/transfers`, async ({ request }) => {
        await record(state, request, 'POST transfer');
        return HttpResponse.json(
          { transferId: ids.user, out: operationBody('debit', ids.node, '500'), in: operationBody('credit', ids.sibling, '500') },
          { status: 201 },
        );
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Transfer' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Select Beta Unit' }));
    expect(screen.getByTestId('recipient-selected').textContent).toContain('Beta Unit');
    await userEvent.type(screen.getByLabelText(/^Amount/), '5');
    await userEvent.type(screen.getByLabelText('Comment'), 'rent');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm transfer' }));

    expect(await screen.findByText('Transfer completed.')).toBeTruthy();
    const sent = state.requests.find((r) => r.path.endsWith('/transfers'));
    expect(sent?.body).toEqual({ fromNodeId: ids.node, toNodeId: ids.sibling, amount: '500', comment: 'rent' });
    expect(sent?.headers.get('Idempotency-Key')).toBeTruthy();
  });

  it('the recipient list loads the next page on "Load more" and keeps the first page', async () => {
    const state = makeState();
    const item = (n: number) => ({ id: `00000000-0000-4000-8000-0000000001${String(n).padStart(2, '0')}`, type: 'unit', name: `Unit ${String(n)}`, status: 'active', childrenCount: 0 });
    server.use(
      http.get(`${baseUrl}/nodes/:id/children`, ({ request }) => {
        const cursor = new URL(request.url).searchParams.get('cursor');
        return cursor === null
          ? HttpResponse.json({ items: [item(1), item(2)], nextCursor: 'page-2' })
          : HttpResponse.json({ items: [item(3)], nextCursor: null });
      }),
      ...readHandlers(state),
    );
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Transfer' }));
    expect(await screen.findByRole('button', { name: 'Select Unit 2' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Select Unit 3' })).toBeNull();

    // The tree behind the dialog also lists children, so the button is looked up inside the dialog.
    const dialog = screen.getByRole('dialog');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Load more' }));
    expect(await screen.findByRole('button', { name: 'Select Unit 3' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Select Unit 1' })).toBeTruthy();
    expect(within(dialog).queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('a transfer without a recipient is not sent', async () => {
    const state = makeState();
    server.use(...readHandlers(state));
    renderApp(nodePath);

    await userEvent.click(await screen.findByRole('button', { name: 'Transfer' }));
    await userEvent.type(await screen.findByLabelText(/^Amount/), '5');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm transfer' }));

    expect(await screen.findByText('Choose a recipient.')).toBeTruthy();
    expect(state.hits['POST transfer']).toBeUndefined();
  });

  it('[T-W-01] the money buttons are hidden without the right (the server stays the authority)', async () => {
    const state = makeState({ role: 'viewer', permissions: ['node.read', 'balance.read', 'balance.credit'] });
    server.use(...readHandlers(state));
    renderApp(nodePath);

    expect(await screen.findByRole('button', { name: 'Credit' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Debit' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Transfer' })).toBeNull();
  });

  it('[T-W-01] a 403 from the server shows an Alert, leaves the data unchanged and reads the permissions again', async () => {
    const state = makeState();
    server.use(debitHandler(state, () => problemResponse(403, 'forbidden')), ...readHandlers(state));
    renderApp(nodePath);

    await openDebit('10');
    const nodeReads = state.hits['GET node'] ?? 0;
    const balanceReads = state.hits['GET balance'] ?? 0;
    const permsReads = state.hits['GET perms'] ?? 0;
    await userEvent.click(screen.getByRole('button', { name: 'Confirm debit' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('You do not have permission for this action');
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(permsReads);
    });
    expect(state.hits['GET node']).toBe(nodeReads);
    expect(state.hits['GET balance']).toBe(balanceReads);
    expect(screen.getByTestId('balance-amount').textContent).toContain('10.50');
  });
});
