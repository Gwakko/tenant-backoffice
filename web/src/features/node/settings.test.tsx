import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import type { SettingItem } from '@/api/resources';
import { baseUrl, problemResponse, resetAuthState } from '@/test/harness';
import { ids, makeState, readHandlers, record, renderApp } from '@/test/nodeBackend';

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

const path = `/nodes/${ids.node}/settings`;

const settings: SettingItem[] = [
  { key: 'display_timezone', own: 'Europe/Berlin', effective: 'Europe/Berlin', sourceNodeId: ids.node },
  { key: 'operations_enabled', own: null, effective: true, sourceNodeId: ids.root },
  { key: 'max_operation_amount', own: null, effective: '500000', sourceNodeId: null },
];

describe('settings', () => {
  it('shows the effective value with its source: own, inherited from a named node, inherited', async () => {
    const state = makeState({ settings });
    server.use(...readHandlers(state));
    renderApp(path);

    expect((await screen.findByTestId('source-display_timezone')).textContent).toBe('(own)');
    expect(screen.getByTestId('value-display_timezone').textContent).toBe('Europe/Berlin');
    expect(screen.getByTestId('source-operations_enabled').textContent).toBe('(inherited from Root Partner)');
    expect(screen.getByTestId('value-operations_enabled').textContent).toBe('Enabled');
    expect(screen.getByTestId('source-max_operation_amount').textContent).toBe('(inherited)');
    expect(screen.getByTestId('value-max_operation_amount').textContent).toBe('5,000.00 EUR');
  });

  it('sets an override: the money value goes as minor units', async () => {
    const state = makeState({ settings });
    server.use(
      http.put(`${baseUrl}/nodes/:id/settings/:key`, async ({ request, params }) => {
        const entry = await record(state, request, 'PUT setting');
        const next = settings.map((item) =>
          item.key === params.key ? { ...item, own: (entry.body as { value: string }).value, sourceNodeId: ids.node } : item,
        );
        state.settings = next;
        return HttpResponse.json(next.find((item) => item.key === params.key));
      }),
      ...readHandlers(state),
    );
    renderApp(path);

    const form = await screen.findByRole('form', { name: 'Edit Maximum amount of one operation' });
    const field = within(form).getByLabelText('Maximum amount of one operation value');
    await userEvent.clear(field);
    await userEvent.type(field, '12.5');
    await userEvent.click(within(form).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(state.hits['PUT setting']).toBe(1);
    });
    expect(state.requests.find((r) => r.method === 'PUT')?.body).toEqual({ value: '1250' });
    await waitFor(() => {
      expect(screen.getByTestId('source-max_operation_amount').textContent).toBe('(own)');
    });
  });

  it('a value the server refuses lands in the form as a field error', async () => {
    const state = makeState({ settings });
    server.use(
      http.put(`${baseUrl}/nodes/:id/settings/:key`, () =>
        HttpResponse.json(
          {
            type: 'about:blank',
            title: 'invalid',
            status: 422,
            code: 'invalid_setting_value',
            requestId: 'req-test-0001',
            errors: [{ field: 'value', code: 'invalid_setting_value', message: 'Unknown time zone.' }],
          },
          { status: 422, headers: { 'Content-Type': 'application/problem+json' } },
        ),
      ),
      ...readHandlers(state),
    );
    renderApp(path);

    const form = await screen.findByRole('form', { name: 'Edit Display time zone' });
    const field = within(form).getByLabelText('Display time zone value');
    await userEvent.clear(field);
    await userEvent.type(field, 'Mars/Base');
    await userEvent.click(within(form).getByRole('button', { name: 'Save' }));

    expect(await within(form).findByText('Unknown time zone.')).toBeTruthy();
  });

  it('resets an own override; there is no reset for an inherited value', async () => {
    const state = makeState({ settings });
    server.use(
      http.delete(`${baseUrl}/nodes/:id/settings/:key`, async ({ request }) => {
        await record(state, request, 'DELETE setting');
        state.settings = settings.map((item) =>
          item.key === 'display_timezone' ? { ...item, own: null, effective: 'UTC', sourceNodeId: null } : item,
        );
        return HttpResponse.json(state.settings[0]);
      }),
      ...readHandlers(state),
    );
    renderApp(path);

    const form = await screen.findByRole('form', { name: 'Edit Display time zone' });
    expect(screen.getAllByRole('button', { name: 'Reset to inherited' })).toHaveLength(1);
    await userEvent.click(within(form).getByRole('button', { name: 'Reset to inherited' }));

    await waitFor(() => {
      expect(screen.getByTestId('source-display_timezone').textContent).toBe('(inherited)');
    });
    expect(state.hits['DELETE setting']).toBe(1);
  });

  it('[T-W-01] without settings.write the values are shown and there is nothing to edit; a 403 on save shows an Alert', async () => {
    const readOnly = makeState({ settings, role: 'viewer', permissions: ['node.read', 'settings.read', 'balance.read'] });
    server.use(...readHandlers(readOnly));
    const first = renderApp(path);
    await screen.findByTestId('value-display_timezone');
    await waitFor(() => {
      expect(readOnly.hits['GET perms']).toBeGreaterThan(0);
    });
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    first.view.unmount();
    first.unwire();
    server.resetHandlers();

    const state = makeState({ settings });
    server.use(
      http.put(`${baseUrl}/nodes/:id/settings/:key`, () => problemResponse(403, 'forbidden')),
      ...readHandlers(state),
    );
    renderApp(path);
    const form = await screen.findByRole('form', { name: 'Edit Display time zone' });
    await userEvent.click(within(form).getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toContain('You do not have permission for this action');
    expect(screen.getByTestId('value-display_timezone').textContent).toBe('Europe/Berlin');
  });
});
