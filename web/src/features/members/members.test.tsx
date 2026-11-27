import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { baseUrl, problemResponse, resetAuthState } from '@/test/harness';
import { ids, makeState, readHandlers, record, renderApp, type BackendState } from '@/test/nodeBackend';

import { canManage, grantableRoles } from './roles';

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
  vi.restoreAllMocks();
});
afterAll(() => {
  server.close();
});

const path = `/nodes/${ids.node}/members`;
const GOOD_PASSWORD = 'correct horse battery';

function member(email: string, role: BackendState['members'][number]['role'], inheritedFrom: string | null = null) {
  return {
    userId: `0190f2a0-0000-7000-8000-0000000d${String(email.length).padStart(4, '0')}`,
    email,
    role,
    nodeId: inheritedFrom ?? ids.node,
    inheritedFrom,
    grantedAt: '2026-01-01T00:00:00Z',
  };
}

function roleOptions(select: HTMLElement): string[] {
  return Array.from(select.querySelectorAll('option')).map((o) => o.value);
}

function addHandler(state: BackendState, answer: () => Response) {
  return http.post(`${baseUrl}/nodes/:id/members`, async ({ request }) => {
    await record(state, request, 'POST member');
    return answer();
  });
}

function submitButton(): HTMLElement {
  const button = document.querySelector<HTMLElement>('dialog form button[type=submit]');
  if (button === null) throw new Error('no form is open');
  return button;
}

async function openAdd() {
  await userEvent.click(await screen.findByRole('button', { name: 'Add member' }));
  return screen.findByLabelText('Email');
}

describe('role rules', () => {
  it('offers only roles up to the own one; an assignment above the own role is not manageable', () => {
    expect(grantableRoles('admin')).toEqual(['viewer', 'analyst', 'admin']);
    expect(grantableRoles('owner')).toEqual(['viewer', 'analyst', 'admin', 'owner']);
    expect(grantableRoles('viewer')).toEqual(['viewer']);
    expect(grantableRoles(undefined)).toEqual([]);
    expect(canManage('admin', 'owner')).toBe(false);
    expect(canManage('admin', 'admin')).toBe(true);
  });
});

describe('members', () => {
  it('lists direct members and, on the other tab, inherited ones with their source', async () => {
    const state = makeState({ members: [member('direct@example.test', 'analyst'), member('top@example.test', 'owner', ids.root)] });
    server.use(...readHandlers(state));
    renderApp(path);

    expect(await screen.findByText('direct@example.test')).toBeTruthy();
    expect(screen.queryByText('top@example.test')).toBeNull();

    await userEvent.click(screen.getByRole('button', { name: 'Including inherited' }));
    expect(await screen.findByText('top@example.test')).toBeTruthy();
    expect(screen.getByText(/inherited from Root Partner/)).toBeTruthy();
    expect(screen.queryByLabelText('Role of top@example.test')).toBeNull();
  });

  it('[T-W-01] the role select holds only roles not above the own role', async () => {
    const state = makeState({ role: 'admin', members: [member('low@example.test', 'analyst'), member('boss@example.test', 'owner')] });
    server.use(...readHandlers(state));
    renderApp(path);

    const select = await screen.findByLabelText('Role of low@example.test');
    expect(roleOptions(select)).toEqual(['viewer', 'analyst', 'admin']);
    // An owner is above an admin: no controls for that row at all.
    expect(screen.queryByLabelText('Role of boss@example.test')).toBeNull();

    const roleInForm = (await openAdd(), screen.getByLabelText('Role'));
    expect(roleOptions(roleInForm)).toEqual(['viewer', 'analyst', 'admin']);
  });

  it('[T-W-01] without member.manage there is no add button and no row controls', async () => {
    const state = makeState({ role: 'analyst', permissions: ['node.read', 'member.read'], members: [member('low@example.test', 'viewer')] });
    server.use(...readHandlers(state));
    renderApp(path);

    await screen.findByText('low@example.test');
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(0);
    });
    expect(screen.queryByRole('button', { name: 'Add member' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
  });

  it('adds a member with an initial password: password field, no echo, not in the URL, cleared afterwards', async () => {
    const state = makeState();
    const added = member('new@example.test', 'viewer');
    server.use(addHandler(state, () => HttpResponse.json(added, { status: 201 })), ...readHandlers(state));
    const spies = [vi.spyOn(console, 'log'), vi.spyOn(console, 'info'), vi.spyOn(console, 'warn'), vi.spyOn(console, 'error')];
    renderApp(path);

    await openAdd();
    const password = screen.getByLabelText('Initial password');
    expect(password.getAttribute('type')).toBe('password');
    expect(password.getAttribute('autocomplete')).toBe('new-password');
    await userEvent.type(screen.getByLabelText('Email'), ' new@example.test ');
    await userEvent.type(password, GOOD_PASSWORD);
    await userEvent.click(submitButton());

    await waitFor(() => {
      expect(state.hits['POST member']).toBe(1);
    });
    const sent = state.requests.find((r) => r.method === 'POST');
    expect(sent?.body).toEqual({ email: 'new@example.test', role: 'viewer', initialPassword: GOOD_PASSWORD });
    expect(sent?.path).not.toContain(GOOD_PASSWORD);
    for (const spy of spies) expect(JSON.stringify(spy.mock.calls)).not.toContain(GOOD_PASSWORD);
    await waitFor(() => {
      expect(screen.queryByLabelText('Initial password')).toBeNull();
    });
  });

  it('keeps no password after a refused request and omits it when empty', async () => {
    const state = makeState();
    server.use(addHandler(state, () => problemResponse(409, 'user_inactive')), ...readHandlers(state));
    renderApp(path);

    await openAdd();
    await userEvent.type(screen.getByLabelText('Email'), 'gone@example.test');
    await userEvent.click(submitButton());
    expect((await screen.findByRole('alert')).textContent).toContain('The user is inactive.');
    expect(state.requests.find((r) => r.method === 'POST')?.body).toEqual({ email: 'gone@example.test', role: 'viewer' });

    await userEvent.type(screen.getByLabelText('Initial password'), GOOD_PASSWORD);
    await userEvent.click(submitButton());
    await waitFor(() => {
      expect(state.hits['POST member']).toBe(2);
    });
    await waitFor(() => {
      expect(screen.getByLabelText<HTMLInputElement>('Initial password').value).toBe('');
    });
  });

  it('a short password and a wrong email never reach the server', async () => {
    const state = makeState();
    server.use(addHandler(state, () => HttpResponse.json(member('x@example.test', 'viewer'), { status: 201 })), ...readHandlers(state));
    renderApp(path);

    await openAdd();
    await userEvent.type(screen.getByLabelText('Email'), 'not-an-email');
    await userEvent.type(screen.getByLabelText('Initial password'), 'short');
    await userEvent.click(submitButton());

    expect(await screen.findByText('Enter a valid email.')).toBeTruthy();
    expect(screen.getByText('Use at least 12 characters.')).toBeTruthy();
    expect(state.hits['POST member']).toBeUndefined();
  });

  it('[T-W-01] a 403 role_not_grantable on add shows the permission Alert and reads the permissions again', async () => {
    const state = makeState();
    server.use(addHandler(state, () => problemResponse(403, 'role_not_grantable')), ...readHandlers(state));
    renderApp(path);

    await openAdd();
    await userEvent.type(screen.getByLabelText('Email'), 'a@example.test');
    const permsReads = state.hits['GET perms'] ?? 0;
    await userEvent.click(submitButton());

    expect((await screen.findByRole('alert')).textContent).toContain('You do not have permission for this action');
    await waitFor(() => {
      expect(state.hits['GET perms']).toBeGreaterThan(permsReads);
    });
  });

  it('changes a role and removes a member after confirmation', async () => {
    const state = makeState({ members: [member('low@example.test', 'viewer')] });
    const target = state.members[0];
    server.use(
      http.put(`${baseUrl}/nodes/:id/members/:userId`, async ({ request }) => {
        await record(state, request, 'PUT member');
        return HttpResponse.json({ ...target, role: 'admin' });
      }),
      http.delete(`${baseUrl}/nodes/:id/members/:userId`, async ({ request }) => {
        await record(state, request, 'DELETE member');
        state.members = [];
        return new HttpResponse(null, { status: 204 });
      }),
      ...readHandlers(state),
    );
    renderApp(path);

    await userEvent.selectOptions(await screen.findByLabelText('Role of low@example.test'), 'admin');
    await userEvent.click(screen.getByRole('button', { name: 'Save role' }));
    await waitFor(() => {
      expect(state.hits['PUT member']).toBe(1);
    });
    expect(state.requests.find((r) => r.method === 'PUT')?.body).toEqual({ role: 'admin' });

    await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));
    await waitFor(() => {
      expect(state.hits['DELETE member']).toBe(1);
    });
    expect(await screen.findByText('No members.')).toBeTruthy();
  });
});
