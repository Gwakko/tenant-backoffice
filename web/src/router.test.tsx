import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { delay, http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { logout } from '@/auth/service';
import { getAccessToken, hasSession, setSession } from '@/auth/session';
import { baseUrl, createTestApp, problemResponse, resetAuthState, testUser, tokenBody } from '@/test/harness';

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

function renderApp(path: string) {
  const app = createTestApp(path);
  render(
    <QueryClientProvider client={app.queryClient}>
      <RouterProvider router={app.router} />
    </QueryClientProvider>,
  );
  return app;
}

describe('routing and session', () => {
  it('renders the root layout and the index route for a signed-in user', async () => {
    setSession(tokenBody('t1'));

    renderApp('/');

    expect(await screen.findByRole('heading', { name: 'Tenant Backoffice' })).toBeTruthy();
    expect(screen.getAllByText('Tenant Backoffice').length).toBeGreaterThan(1);
    expect(screen.getByText(/Signed in as ann@example.test/)).toBeTruthy();
  });

  it('restores the session on load through POST /auth/refresh (cookie), not from storage', async () => {
    const seen: { csrf: string | null; authorization: string | null }[] = [];
    server.use(
      http.post(`${baseUrl}/auth/refresh`, ({ request }) => {
        seen.push({ csrf: request.headers.get('X-TB-CSRF'), authorization: request.headers.get('Authorization') });
        return HttpResponse.json(tokenBody('restored-token'));
      }),
    );

    renderApp('/');

    expect(await screen.findByText(/Signed in as ann@example.test/)).toBeTruthy();
    expect(seen).toEqual([{ csrf: '1', authorization: null }]);
    expect(getAccessToken()).toBe('restored-token');
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it('sends a visitor without a session to /login?redirect= and asks the cookie only once per load', async () => {
    let refreshes = 0;
    server.use(
      http.post(`${baseUrl}/auth/refresh`, () => {
        refreshes += 1;
        return problemResponse(401, 'refresh_invalid');
      }),
    );

    const app = renderApp('/?tab=ops');

    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy();
    expect(app.router.state.location.pathname).toBe('/login');
    expect(app.router.state.location.search).toEqual({ redirect: '/?tab=ops' });

    await app.router.navigate({ to: '/' });
    await waitFor(() => {
      expect(app.router.state.location.pathname).toBe('/login');
    });
    expect(refreshes).toBe(1);
  });

  it('[T-W-03] a refresh that fails after a 401 sends the user to /login?redirect= and drops session and cache', async () => {
    let refreshes = 0;
    let meCalls = 0;
    server.use(
      http.get(`${baseUrl}/me`, () => {
        meCalls += 1;
        return problemResponse(401, 'token_expired');
      }),
      http.post(`${baseUrl}/auth/refresh`, async () => {
        refreshes += 1;
        await delay(20);
        return problemResponse(401, 'refresh_invalid');
      }),
    );
    setSession(tokenBody('stale-token'));
    const app = renderApp('/?tab=ops');
    await screen.findByText(/Signed in as/);
    app.queryClient.setQueryData(['me'], { id: testUser.id });
    expect(app.queryClient.getQueryCache().getAll()).toHaveLength(1);

    const results = await Promise.all([app.api.client.GET('/me'), app.api.client.GET('/me'), app.api.client.GET('/me')]);

    expect(results.map((r) => r.response.status)).toEqual([401, 401, 401]);
    expect(refreshes).toBe(1);
    expect(meCalls).toBe(3);
    await waitFor(() => {
      expect(app.router.state.location.pathname).toBe('/login');
    });
    expect(app.router.state.location.search).toEqual({ redirect: '/?tab=ops' });
    expect(hasSession()).toBe(false);
    expect(app.queryClient.getQueryCache().getAll()).toHaveLength(0);
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy();
  });

  it('signs out: calls POST /auth/logout with Bearer and CSRF, clears memory and the Query cache, opens /login', async () => {
    const seen: { authorization: string | null; csrf: string | null }[] = [];
    server.use(
      http.post(`${baseUrl}/auth/logout`, ({ request }) => {
        seen.push({ authorization: request.headers.get('Authorization'), csrf: request.headers.get('X-TB-CSRF') });
        return new HttpResponse(null, { status: 204 });
      }),
    );
    setSession(tokenBody('live-token'));
    const app = renderApp('/');
    await screen.findByText(/Signed in as/);
    app.queryClient.setQueryData(['node', 'n1'], { name: 'Secret node' });

    await userEvent.click(screen.getByRole('button', { name: 'Sign out' }));

    await waitFor(() => {
      expect(app.router.state.location.pathname).toBe('/login');
    });
    expect(seen).toEqual([{ authorization: 'Bearer live-token', csrf: '1' }]);
    expect(hasSession()).toBe(false);
    expect(app.queryClient.getQueryCache().getAll()).toHaveLength(0);
    expect(app.queryClient.getQueryData(['node', 'n1'])).toBeUndefined();
  });

  it('clears the local session even when the logout request cannot reach the server', async () => {
    server.use(http.post(`${baseUrl}/auth/logout`, () => HttpResponse.error()));
    setSession(tokenBody('live-token'));
    const app = createTestApp('/');
    app.queryClient.setQueryData(['node', 'n1'], { name: 'Secret node' });

    await logout(app.api, app.queryClient);

    expect(hasSession()).toBe(false);
    expect(app.queryClient.getQueryCache().getAll()).toHaveLength(0);
  });
});
