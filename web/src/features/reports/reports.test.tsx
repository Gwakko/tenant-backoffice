import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { problemResponse, resetAuthState } from '@/test/harness';
import { listHandler, page } from '@/test/lists';
import { ids, makeState, readHandlers, renderApp, type BackendState } from '@/test/nodeBackend';

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

const path = `/nodes/${ids.node}/reports`;
const period = 'from=2026-03-01&to=2026-04-01&tz=UTC';

const BIG = '9007199254740993'; // 2^53 + 1: a float would show ...992
const BIG_NEGATIVE = '-9007199254740993';

function summaryBody(over: Record<string, unknown> = {}) {
  return {
    scopeNodeId: ids.node,
    tz: 'UTC',
    totals: { count: 3, credits: BIG, debits: '9007199254740994', net: BIG_NEGATIVE },
    series: [
      { periodStart: '2026-03-01', count: 2, credits: BIG, debits: '100', net: '5' },
      { periodStart: '2026-03-02', count: 1, credits: '0', debits: '9007199254740994', net: BIG_NEGATIVE },
    ],
    ...over,
  };
}

function childItem(n: number, over: Record<string, unknown> = {}) {
  return {
    nodeId: `0190f2a0-0000-7000-8000-0000000d${String(n).padStart(4, '0')}`,
    name: `Child ${String(n)}`,
    type: 'unit',
    active: true,
    count: 1,
    credits: '100',
    debits: '40',
    net: '60',
    balance: '1000',
    ...over,
  };
}

function install(state: BackendState, over: { summary?: () => Response; children?: (q: URLSearchParams) => Response } = {}) {
  const summary = listHandler('/nodes/:id/reports/summary', state, () => over.summary?.() ?? HttpResponse.json(summaryBody()));
  const children = listHandler('/nodes/:id/reports/children', state, (q) => over.children?.(q) ?? page([childItem(1)]));
  server.use(summary.handler, children.handler, ...readHandlers(state));
  return { summary, children };
}

function setDates(from: string, to: string) {
  fireEvent.change(screen.getByLabelText('From'), { target: { value: from } });
  fireEvent.change(screen.getByLabelText('To (exclusive)'), { target: { value: to } });
}

describe('report', () => {
  it('[T-W-05] sums above 2^53 and negative sums are shown exactly (BigInt, no Number)', async () => {
    const state = makeState();
    install(state);
    renderApp(`${path}?${period}`);
    const totals = await screen.findByRole('list', { name: 'Totals' }).catch(() => screen.findByLabelText('Totals'));

    expect(totals.textContent).toContain('90,071,992,547,409.93');
    expect(totals.textContent).toContain('90,071,992,547,409.94');
    expect(totals.textContent).toContain('-90,071,992,547,409.93');
    const table = screen.getByRole('table', { name: 'Totals by period' });
    const [first, second] = within(table).getAllByRole('row').slice(1);
    expect(first?.textContent).toContain('90,071,992,547,409.93');
    expect(second?.textContent).toContain('-90,071,992,547,409.93');
  });

  it('shows the series as a table and as an SVG chart with a text alternative; the period reaches the request', async () => {
    const state = makeState();
    const { summary } = install(state);
    renderApp(`${path}?${period}&granularity=week`);
    const chart = await screen.findByRole('img');

    expect(chart.getAttribute('aria-label')).toContain('2 periods');
    expect(chart.tagName.toLowerCase()).toBe('svg');
    expect(screen.getByRole('table', { name: 'Totals by period' })).toBeTruthy();
    expect(screen.getByText(/Transfers inside the subtree are counted on both sides/)).toBeTruthy();
    const query = summary.queries[0];
    expect(query?.get('from')).toBe('2026-03-01');
    expect(query?.get('to')).toBe('2026-04-01');
    expect(query?.get('tz')).toBe('UTC');
    expect(query?.get('granularity')).toBe('week');
  });

  it('by default the zone is the effective display_timezone of the node', async () => {
    const state = makeState({
      settings: [{ key: 'display_timezone', own: 'Europe/Berlin', effective: 'Europe/Berlin', sourceNodeId: ids.node }],
    });
    const { summary } = install(state);
    renderApp(`${path}?from=2026-03-01&to=2026-04-01`);
    await screen.findByRole('img');
    expect(summary.queries[0]?.get('tz')).toBe('Europe/Berlin');
    expect(screen.getByLabelText<HTMLSelectElement>('Time zone').value).toBe('Europe/Berlin');
  });

  it('a period of 367 days is rejected in the form with the invalid_period text and sends nothing new', async () => {
    const state = makeState();
    const { summary } = install(state);
    const app = renderApp(`${path}?${period}`);
    await screen.findByRole('img');
    const before = summary.queries.length;

    setDates('2026-01-01', '2027-01-03'); // 367 days
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    const message = await screen.findByText(/The selected period is not valid\. The period can be at most 366 days\./);
    expect(message).toBeTruthy();
    expect(summary.queries).toHaveLength(before);
    expect(app.router.state.location.search).toMatchObject({ from: '2026-03-01', to: '2026-04-01' });
  });

  it('366 days are accepted and written to the URL', async () => {
    const state = makeState();
    const { summary } = install(state);
    const app = renderApp(`${path}?${period}`);
    await screen.findByRole('img');

    setDates('2026-01-01', '2027-01-02'); // 366 days
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => {
      expect(app.router.state.location.search).toMatchObject({ from: '2026-01-01', to: '2027-01-02' });
    });
    await waitFor(() => {
      expect(summary.queries.at(-1)?.get('to')).toBe('2027-01-02');
    });
  });

  it('a start date that is not before the end date is rejected in the form', async () => {
    const state = makeState();
    const { summary } = install(state);
    renderApp(`${path}?${period}`);
    await screen.findByRole('img');
    const before = summary.queries.length;

    setDates('2026-03-05', '2026-03-05');
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(await screen.findByText(/The start date must be before the end date\./)).toBeTruthy();
    expect(summary.queries).toHaveLength(before);
  });

  it('an invalid period in the URL is shown as an error and sends no request', async () => {
    const state = makeState();
    const { summary, children } = install(state);
    renderApp(`${path}?from=2026-04-01&to=2026-03-01&tz=UTC`);
    expect(await screen.findByText(/The selected period is not valid\. The start date must be before the end date\./)).toBeTruthy();
    await waitFor(() => {
      expect(state.hits['GET balance']).toBeGreaterThan(0);
    });
    expect(summary.queries).toHaveLength(0);
    expect(children.queries).toHaveLength(0);
  });

  it('garbage in the search params falls back to the defaults', async () => {
    const state = makeState();
    const { summary } = install(state);
    renderApp(`${path}?from=banana&to=%5B%5D&tz=Mars%2FOlympus&granularity=year`);
    await screen.findByRole('img');
    const query = summary.queries[0];
    expect(query?.get('granularity')).toBe('day');
    expect(query?.get('tz')).not.toBe('Mars/Olympus');
    expect(query?.get('from')).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('a server invalid_period is shown near the form', async () => {
    const state = makeState();
    install(state, { summary: () => problemResponse(400, 'invalid_period') });
    renderApp(`${path}?${period}`);
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('The selected period is not valid.');
  });

  it('a 403 shows the permission Alert and the screen stays alive', async () => {
    const state = makeState({ permissions: ['node.read'] });
    install(state, { summary: () => problemResponse(403, 'forbidden'), children: () => problemResponse(403, 'forbidden') });
    renderApp(`${path}?${period}`);
    const alerts = await screen.findAllByRole('alert');
    expect(alerts[0]?.textContent).toContain('You do not have permission for this action.');
    expect(screen.getByRole('heading', { name: 'Reports' })).toBeTruthy();
  });

  it('a 503 offers Retry', async () => {
    const state = makeState();
    let calls = 0;
    install(state, {
      summary: () => {
        calls += 1;
        return calls === 1 ? problemResponse(503, 'db_unavailable') : HttpResponse.json(summaryBody());
      },
    });
    renderApp(`${path}?${period}`);
    const alert = await screen.findByText(/temporarily unavailable/);
    await userEvent.click(within(alert.closest<HTMLElement>('[role=alert]') ?? document.body).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('img')).toBeTruthy();
  });

  it('by child: exact sums, "Load more" with a cursor, and an invalid cursor reloads from the first page', async () => {
    const state = makeState();
    let rejected = false;
    const { children } = install(state, {
      children: (query) => {
        if (query.get('cursor') === null) return page([childItem(1, { balance: BIG }), childItem(2, { net: BIG_NEGATIVE })], 'next');
        rejected = true;
        return problemResponse(400, 'invalid_cursor');
      },
    });
    renderApp(`${path}?${period}`);
    const table = await screen.findByRole('table', { name: 'Totals by child' });
    expect(table.textContent).toContain('90,071,992,547,409.93');
    expect(table.textContent).toContain('-90,071,992,547,409.93');

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => {
      expect(rejected).toBe(true);
    });
    await waitFor(() => {
      expect(children.queries.filter((q) => q.get('cursor') === null).length).toBeGreaterThanOrEqual(2);
    });
    expect(await screen.findByRole('table', { name: 'Totals by child' })).toBeTruthy();
    expect(within(screen.getByRole('table', { name: 'Totals by child' })).getAllByRole('row')).toHaveLength(3);
  });

  it('the node id of the requests is the route one', async () => {
    const state = makeState();
    install(state);
    renderApp(`${path}?${period}`);
    await screen.findByRole('img');
    await act(async () => {
      await Promise.resolve();
    });
    const paths = state.requests.map((r) => r.path).filter((p) => p.includes('/reports/'));
    expect(paths.length).toBeGreaterThan(0);
    for (const p of paths) expect(p).toContain(`/nodes/${ids.node}/reports/`);
  });
});
