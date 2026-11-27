import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { problemResponse, resetAuthState } from '@/test/harness';
import { auditItem, listHandler, page } from '@/test/lists';
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

const path = `/nodes/${ids.node}/audit`;

describe('audit', () => {
  it('lists entries with time, action, actor, node and target; a hidden actor gets a neutral label', async () => {
    const state = makeState();
    server.use(
      listHandler('/audit', state, () =>
        page([auditItem(1), auditItem(2, { actorEmail: null, action: 'balance.credited', targetType: 'balance' })]),
      ).handler,
      ...readHandlers(state),
    );
    renderApp(path);
    const table = await screen.findByRole('table', { name: 'Audit entries' });
    const [first, second] = within(table).getAllByRole('row').slice(1);
    expect(first?.textContent).toContain('2026-03-01 10:20:30 UTC');
    expect(first?.textContent).toContain('Node renamed');
    expect(first?.textContent).toContain('ann@example.test');
    expect(second?.textContent).toContain('Hidden user');
    expect(second?.textContent).toContain('Balance credited');
  });

  it('[T-W-07] the action and period filters live in the URL; garbage gives the defaults', async () => {
    const state = makeState();
    const audit = listHandler('/audit', state, () => page([auditItem(1)]));
    server.use(audit.handler, ...readHandlers(state));
    const app = renderApp(`${path}?action=drop-table&from=nope`);
    await screen.findByRole('table', { name: 'Audit entries' });
    expect(screen.getByLabelText<HTMLSelectElement>('Action').value).toBe('');
    expect(audit.queries[0]?.has('action')).toBe(false);
    expect(audit.queries[0]?.has('from')).toBe(false);

    await userEvent.selectOptions(screen.getByLabelText('Action'), 'Node moved');
    await waitFor(() => {
      expect(app.router.state.location.search).toMatchObject({ action: 'node.moved' });
    });
    await waitFor(() => {
      expect(audit.queries.at(-1)?.get('action')).toBe('node.moved');
    });
    expect(audit.queries.at(-1)?.get('scopeNodeId')).toBe(ids.node);
  });

  it('before and after are shown as text: markup in them is never interpreted as HTML', async () => {
    const state = makeState();
    const hostile = '<img src=x onerror="alert(1)"><script>window.hacked = 1</script>';
    server.use(
      listHandler('/audit', state, () =>
        page([auditItem(1, { before: { name: hostile }, after: { name: 'b'.repeat(500) } })]),
      ).handler,
      ...readHandlers(state),
    );
    renderApp(path);
    const table = await screen.findByRole('table', { name: 'Audit entries' });

    expect(table.querySelector('img')).toBeNull();
    expect(table.querySelector('script')).toBeNull();
    const [before, after] = Array.from(table.querySelectorAll('pre'));
    expect(before?.textContent).toContain(hostile.replace(/"/g, '\\"'));
    expect(after?.textContent).toContain('...');
    expect((after?.textContent ?? '').length).toBeLessThan(500);
    expect('hacked' in window).toBe(false);
  });

  it('"Load more" appends the next page without duplicates', async () => {
    const state = makeState();
    server.use(
      listHandler('/audit', state, (query) =>
        query.get('cursor') === null ? page([auditItem(1), auditItem(2)], 'c1') : page([auditItem(2), auditItem(3)]),
      ).handler,
      ...readHandlers(state),
    );
    renderApp(path);
    await screen.findByRole('table', { name: 'Audit entries' });
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => {
      expect(within(screen.getByRole('table', { name: 'Audit entries' })).getAllByRole('row')).toHaveLength(4);
    });
  });

  it('an invalid cursor reloads the list from the first page', async () => {
    const state = makeState();
    const audit = listHandler('/audit', state, (query) =>
      query.get('cursor') === null ? page([auditItem(1)], 'stale') : problemResponse(400, 'invalid_cursor'),
    );
    server.use(audit.handler, ...readHandlers(state));
    renderApp(path);
    await screen.findByRole('table', { name: 'Audit entries' });
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => {
      expect(audit.queries.filter((q) => q.get('cursor') === null).length).toBeGreaterThanOrEqual(2);
    });
    expect(await screen.findByRole('table', { name: 'Audit entries' })).toBeTruthy();
  });

  it('opening the tab without the right gives a 403 Alert and the screen stays alive', async () => {
    const state = makeState({ permissions: ['node.read'] });
    server.use(listHandler('/audit', state, () => problemResponse(403, 'forbidden')).handler, ...readHandlers(state));
    renderApp(path);
    expect((await screen.findByRole('alert')).textContent).toContain('You do not have permission for this action.');
    expect(screen.getByRole('heading', { name: 'Audit' })).toBeTruthy();
  });
});
