import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { delay, http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { getAccessToken } from '@/auth/session';
import { baseUrl, createTestApp, problemResponse, resetAuthState, tokenBody } from '@/test/harness';

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
  localStorage.clear();
  sessionStorage.clear();
});
afterAll(() => {
  server.close();
});

const EMAIL = 'ann@example.test';
const PASSWORD = 'correct horse battery';

function renderLogin(path = '/login') {
  const app = createTestApp(path);
  render(
    <QueryClientProvider client={app.queryClient}>
      <RouterProvider router={app.router} />
    </QueryClientProvider>,
  );
  return app;
}

async function fillAndSubmit(email = EMAIL, password = PASSWORD) {
  await userEvent.type(await screen.findByLabelText('Email'), email);
  await userEvent.type(screen.getByLabelText('Password'), password);
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

function storageDump(): string {
  const local: Record<string, string | null> = {};
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (key !== null) local[key] = localStorage.getItem(key);
  }
  const session: Record<string, string | null> = {};
  for (let i = 0; i < sessionStorage.length; i++) {
    const key = sessionStorage.key(i);
    if (key !== null) session[key] = sessionStorage.getItem(key);
  }
  return JSON.stringify({ local, session, cookie: document.cookie, url: window.location.href });
}

describe('login', () => {
  it('[T-W-08] after sign-in the access token is not in localStorage, sessionStorage, cookies or the URL', async () => {
    const token = 'eyJ.secret-access-token.sig';
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    let authorization: string | null = null;
    server.use(
      http.post(`${baseUrl}/auth/login`, () => HttpResponse.json(tokenBody(token))),
      http.get(`${baseUrl}/me`, ({ request }) => {
        authorization = request.headers.get('Authorization');
        return HttpResponse.json({ id: 'x', email: EMAIL, accessRoots: [] });
      }),
    );

    const app = renderLogin('/login');
    await fillAndSubmit();

    expect(await screen.findByText(/Signed in as ann@example.test/)).toBeTruthy();
    expect(getAccessToken()).toBe(token);
    const dump = storageDump();
    expect(dump).not.toContain(token);
    expect(dump).not.toContain(PASSWORD);
    expect(setItem).not.toHaveBeenCalled();
    expect(app.router.state.location.href).not.toContain(token);

    // The token is used from memory.
    await app.api.client.GET('/me');
    expect(authorization).toBe(`Bearer ${token}`);
  });

  it('goes to the redirect target after sign-in', async () => {
    server.use(http.post(`${baseUrl}/auth/login`, () => HttpResponse.json(tokenBody('t'))));

    const app = renderLogin('/login?redirect=%2F%3Ftab%3Dops');
    await fillAndSubmit();

    await screen.findByText(/Signed in as/);
    expect(app.router.state.location.pathname).toBe('/');
    expect(app.router.state.location.search).toEqual({ tab: 'ops' });
  });

  it.each(['//evil.test', '/\\evil.test', 'https://evil.test/x', '%2F%2Fevil.test'])(
    'ignores the unsafe redirect %s and lands on /',
    async (unsafe) => {
      server.use(http.post(`${baseUrl}/auth/login`, () => HttpResponse.json(tokenBody('t'))));

      const app = renderLogin(`/login?redirect=${encodeURIComponent(unsafe)}`);
      await fillAndSubmit();

      await screen.findByText(/Signed in as/);
      expect(app.router.state.location.pathname).toBe('/');
      expect(app.router.state.location.href).toBe('/');
    },
  );

  it('uses the right autocomplete hints and a password field', async () => {
    renderLogin();

    const email = await screen.findByLabelText('Email');
    const password = screen.getByLabelText('Password');

    expect(email.getAttribute('autocomplete')).toBe('username');
    expect(password.getAttribute('autocomplete')).toBe('current-password');
    expect(password.getAttribute('type')).toBe('password');
  });

  it('validates the form before sending anything', async () => {
    let requests = 0;
    server.use(
      http.post(`${baseUrl}/auth/login`, () => {
        requests += 1;
        return HttpResponse.json(tokenBody('t'));
      }),
    );
    renderLogin();

    await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }));
    expect(await screen.findByText('Enter your email.')).toBeTruthy();
    expect(screen.getByText('Enter your password.')).toBeTruthy();

    await userEvent.type(screen.getByLabelText('Email'), 'not-an-email');
    await userEvent.type(screen.getByLabelText('Password'), 'x');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByText('Enter a valid email.')).toBeTruthy();
    expect(requests).toBe(0);
  });

  it('shows one identical message for a wrong password, an unknown email and an inactive user, and clears the password', async () => {
    server.use(http.post(`${baseUrl}/auth/login`, () => problemResponse(401, 'invalid_credentials')));
    renderLogin();

    const messages: string[] = [];
    for (const email of ['ann@example.test', 'nobody@example.test', 'inactive@example.test']) {
      await userEvent.clear(await screen.findByLabelText('Email'));
      await userEvent.type(screen.getByLabelText('Email'), email);
      await userEvent.type(screen.getByLabelText('Password'), 'wrong-password');
      await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
      const alert = await screen.findByRole('alert');
      await waitFor(() => {
        expect(screen.getByLabelText<HTMLInputElement>('Password').value).toBe('');
      });
      messages.push(alert.textContent);
    }

    expect(new Set(messages)).toEqual(new Set(['Invalid email or password.']));
    expect(getAccessToken()).toBeNull();
  });

  it('shows Retry-After on 429', async () => {
    server.use(http.post(`${baseUrl}/auth/login`, () => problemResponse(429, 'rate_limited', { 'Retry-After': '42' })));
    renderLogin();

    await fillAndSubmit();

    expect((await screen.findByRole('alert')).textContent).toBe('Too many requests. Try again in 42 seconds.');
  });

  it('shows a clear message on 503 and on a network failure', async () => {
    server.use(http.post(`${baseUrl}/auth/login`, () => problemResponse(503, 'db_unavailable')));
    renderLogin();
    await fillAndSubmit();
    expect((await screen.findByRole('alert')).textContent).toContain('temporarily unavailable');

    server.use(http.post(`${baseUrl}/auth/login`, () => HttpResponse.error()));
    await userEvent.type(screen.getByLabelText('Password'), PASSWORD);
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => {
      expect(screen.getByRole('alert').textContent).toContain('Cannot reach the server');
    });
  });

  it('shows server text as text, never as markup', async () => {
    server.use(
      http.post(`${baseUrl}/auth/login`, () =>
        problemResponse(409, 'brand_new_code', {}, '<img src=x onerror="window.pwned=1"><b>bold</b>'),
      ),
    );
    renderLogin();

    await fillAndSubmit();

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toBe('<img src=x onerror="window.pwned=1"><b>bold</b>');
    expect(alert.querySelector('img, b')).toBeNull();
  });

  it('blocks the button while the request runs and sends one request on a double submit', async () => {
    let requests = 0;
    server.use(
      http.post(`${baseUrl}/auth/login`, async () => {
        requests += 1;
        await delay(80);
        return HttpResponse.json(tokenBody('t'));
      }),
    );
    renderLogin();
    await userEvent.type(await screen.findByLabelText('Email'), EMAIL);
    await userEvent.type(screen.getByLabelText('Password'), PASSWORD);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await user.click(screen.getByRole('button', { name: /Signing in/ }));
    await user.keyboard('{Enter}');

    expect(screen.getByRole('button', { name: /Signing in/ }).hasAttribute('disabled')).toBe(true);
    await screen.findByText(/Signed in as/);
    expect(requests).toBe(1);
  });

  it('does not keep the password after a successful submit', async () => {
    server.use(http.post(`${baseUrl}/auth/login`, () => HttpResponse.json(tokenBody('t'))));
    renderLogin();

    await fillAndSubmit();

    await screen.findByText(/Signed in as/);
    expect(storageDump()).not.toContain(PASSWORD);
    expect(document.body.innerHTML).not.toContain(PASSWORD);
  });
});
