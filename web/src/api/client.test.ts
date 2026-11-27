import { http, HttpResponse, delay } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';

import { baseUrl, fakeSession, problemResponse, testUser, tokenBody } from '@/test/harness';

import { createApiClient } from './client';
import type { components } from './schema';

type Problem = components['schemas']['Problem'];

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
});
afterEach(() => {
  server.resetHandlers();
});
afterAll(() => {
  server.close();
});

const meBody = { id: testUser.id, email: testUser.email, accessRoots: [] };

describe('api client', () => {
  it('parses an application/problem+json error into the generated Problem type', async () => {
    const problem: Problem = {
      type: 'https://example.invalid/problems/not-found',
      title: 'Not found or no access',
      status: 404,
      code: 'not_found',
      requestId: 'req-1',
    };
    server.use(
      http.get(`${baseUrl}/me`, () =>
        HttpResponse.json(problem, { status: 404, headers: { 'Content-Type': 'application/problem+json' } }),
      ),
    );

    const { data, error, response } = await createApiClient({ baseUrl, session: fakeSession('t') }).client.GET('/me');

    expect(data).toBeUndefined();
    expect(response.status).toBe(404);
    expect(response.headers.get('Content-Type')).toBe('application/problem+json');
    expect(error?.status).toBe(404);
    expect(error?.title).toBe('Not found or no access');
    expect(error?.code).toBe('not_found');
  });

  it('sends requests under the configured base URL, with the Bearer token read from memory', async () => {
    let requested = '';
    let authorization: string | null = null;
    let requestId: string | null = null;
    server.use(
      http.get(`${baseUrl}/me`, ({ request }) => {
        requested = new URL(request.url).pathname;
        authorization = request.headers.get('Authorization');
        requestId = request.headers.get('X-Request-Id');
        return HttpResponse.json(meBody);
      }),
    );
    const session = fakeSession('memory-token');

    const { data } = await createApiClient({ baseUrl, session }).client.GET('/me');

    expect(data?.email).toBe(testUser.email);
    expect(requested).toBe('/api/v1/me');
    expect(authorization).toBe('Bearer memory-token');
    expect(requestId).toMatch(/^[A-Za-z0-9-]{8,64}$/);
  });

  it('uses cookie credentials only for /auth/*, and sends no Bearer on login or refresh', async () => {
    const seen: Record<string, { credentials: string; authorization: string | null }> = {};
    server.use(
      http.get(`${baseUrl}/me`, () => HttpResponse.json(meBody)),
      http.post(`${baseUrl}/auth/login`, () => HttpResponse.json(tokenBody('a'))),
      http.post(`${baseUrl}/auth/refresh`, () => HttpResponse.json(tokenBody('b'))),
    );
    // The request as the client builds it, before the (mocked) network sees it.
    const api = createApiClient({
      baseUrl,
      session: fakeSession('t'),
      fetch: (request) => {
        const name = new URL(request.url).pathname.replace('/api/v1/', '');
        seen[name] = { credentials: request.credentials, authorization: request.headers.get('Authorization') };
        return globalThis.fetch(request);
      },
    });

    await api.client.GET('/me');
    await api.client.POST('/auth/login', { body: { email: testUser.email, password: 'x' } });
    await api.refreshSession();

    expect(seen.me).toEqual({ credentials: 'omit', authorization: 'Bearer t' });
    expect(seen['auth/login']).toEqual({ credentials: 'include', authorization: null });
    expect(seen['auth/refresh']).toEqual({ credentials: 'include', authorization: null });
  });

  it('sends the CSRF header on refresh and logout, and only there', async () => {
    const csrf: Record<string, string | null> = {};
    server.use(
      http.post(`${baseUrl}/auth/refresh`, ({ request }) => {
        csrf.refresh = request.headers.get('X-TB-CSRF');
        return HttpResponse.json(tokenBody('b'));
      }),
      http.post(`${baseUrl}/auth/logout`, ({ request }) => {
        csrf.logout = request.headers.get('X-TB-CSRF');
        return new HttpResponse(null, { status: 204 });
      }),
      http.get(`${baseUrl}/me`, ({ request }) => {
        csrf.me = request.headers.get('X-TB-CSRF');
        return HttpResponse.json(meBody);
      }),
    );
    const api = createApiClient({ baseUrl, session: fakeSession('t') });

    await api.refreshSession();
    await api.client.POST('/auth/logout');
    await api.client.GET('/me');

    expect(csrf).toEqual({ refresh: '1', logout: '1', me: null });
  });
});

describe('api client: refresh on 401', () => {
  function countingHandlers(options: { refreshStatus?: number; meAlwaysExpired?: boolean } = {}) {
    const counts = { refresh: 0, me: 0 };
    const tokensSeen: (string | null)[] = [];
    server.use(
      http.post(`${baseUrl}/auth/refresh`, async () => {
        counts.refresh += 1;
        await delay(25);
        if ((options.refreshStatus ?? 200) !== 200) return problemResponse(401, 'refresh_invalid');
        return HttpResponse.json(tokenBody('fresh-token'));
      }),
      http.get(`${baseUrl}/me`, ({ request }) => {
        counts.me += 1;
        const token = request.headers.get('Authorization');
        tokensSeen.push(token);
        if (options.meAlwaysExpired === true || token !== 'Bearer fresh-token') {
          return problemResponse(401, 'token_expired');
        }
        return HttpResponse.json(meBody);
      }),
    );
    return { counts, tokensSeen };
  }

  it('[T-W-03] parallel 401 responses cause exactly one refresh and every request is repeated once', async () => {
    const { counts, tokensSeen } = countingHandlers();
    const session = fakeSession('stale-token');
    const api = createApiClient({ baseUrl, session });

    const results = await Promise.all([api.client.GET('/me'), api.client.GET('/me'), api.client.GET('/me')]);

    expect(counts.refresh).toBe(1);
    expect(counts.me).toBe(6);
    expect(results.map((r) => r.response.status)).toEqual([200, 200, 200]);
    expect(results.every((r) => r.data?.email === testUser.email)).toBe(true);
    expect(tokensSeen.filter((t) => t === 'Bearer fresh-token')).toHaveLength(3);
    expect(session.token).toBe('fresh-token');
    expect(session.lost).toBe(0);
  });

  it('[T-W-03] a request that fails after the refresh already finished reuses the new token without a second refresh', async () => {
    const counts = { refresh: 0 };
    let calls = 0;
    server.use(
      http.post(`${baseUrl}/auth/refresh`, async () => {
        counts.refresh += 1;
        await delay(25);
        return HttpResponse.json(tokenBody('fresh-token'));
      }),
      http.get(`${baseUrl}/me`, async ({ request }) => {
        const call = calls++;
        // The second request answers 401 (stale token) only after the first one has refreshed.
        if (call === 1) await delay(120);
        return request.headers.get('Authorization') === 'Bearer fresh-token'
          ? HttpResponse.json(meBody)
          : problemResponse(401, 'token_expired');
      }),
    );
    const api = createApiClient({ baseUrl, session: fakeSession('stale-token') });

    const [, slow] = await Promise.all([api.client.GET('/me'), api.client.GET('/me')]);

    expect(counts.refresh).toBe(1);
    expect(slow.response.status).toBe(200);
    expect(calls).toBe(4);
  });

  it('[T-W-03] a failed refresh clears the session once, repeats nothing and returns the original 401', async () => {
    const { counts } = countingHandlers({ refreshStatus: 401 });
    const session = fakeSession('stale-token');
    const api = createApiClient({ baseUrl, session });

    const results = await Promise.all([api.client.GET('/me'), api.client.GET('/me')]);

    expect(counts.refresh).toBe(1);
    expect(counts.me).toBe(2);
    expect(results.map((r) => r.response.status)).toEqual([401, 401]);
    expect(results.map((r) => r.error?.code)).toEqual(['token_expired', 'token_expired']);
    expect(session.lost).toBe(1);
    expect(session.token).toBeNull();
  });

  it('[T-W-03] a repeated request is final: a second 401 does not start another refresh or another repeat', async () => {
    const { counts } = countingHandlers({ meAlwaysExpired: true });
    const session = fakeSession('stale-token');
    const api = createApiClient({ baseUrl, session });

    const { response, error } = await api.client.GET('/me');

    expect(response.status).toBe(401);
    expect(error?.code).toBe('token_expired');
    expect(counts.refresh).toBe(1);
    expect(counts.me).toBe(2);
    expect(session.lost).toBe(0);
  });

  it('does not refresh on other 401 codes or on a failed login', async () => {
    let refreshes = 0;
    server.use(
      http.post(`${baseUrl}/auth/refresh`, () => {
        refreshes += 1;
        return HttpResponse.json(tokenBody('x'));
      }),
      http.get(`${baseUrl}/me`, () => problemResponse(401, 'token_missing')),
      http.post(`${baseUrl}/auth/login`, () => problemResponse(401, 'invalid_credentials')),
    );
    const api = createApiClient({ baseUrl, session: fakeSession('t') });

    const me = await api.client.GET('/me');
    const login = await api.client.POST('/auth/login', { body: { email: testUser.email, password: 'x' } });

    expect(me.response.status).toBe(401);
    expect(login.response.status).toBe(401);
    expect(refreshes).toBe(0);
  });

  it('repeats a request with its body intact and the same idempotency header', async () => {
    const bodies: string[] = [];
    const keys: (string | null)[] = [];
    server.use(
      http.post(`${baseUrl}/auth/refresh`, () => HttpResponse.json(tokenBody('fresh-token'))),
      http.post(`${baseUrl}/auth/password`, async ({ request }) => {
        bodies.push(await request.text());
        keys.push(request.headers.get('X-Request-Id'));
        return request.headers.get('Authorization') === 'Bearer fresh-token'
          ? new HttpResponse(null, { status: 204 })
          : problemResponse(401, 'token_invalid');
      }),
    );
    const api = createApiClient({ baseUrl, session: fakeSession('stale-token') });

    const { response } = await api.client.POST('/auth/password', {
      body: { currentPassword: 'old-secret-1', newPassword: 'new-secret-123' },
    });

    expect(response.status).toBe(204);
    expect(bodies).toHaveLength(2);
    expect(bodies[1]).toBe(bodies[0]);
    expect(bodies[1]).toContain('new-secret-123');
    expect(keys[1]).toBe(keys[0]);
  });

  it('treats a network failure of the refresh call as a lost session', async () => {
    server.use(
      http.get(`${baseUrl}/me`, () => problemResponse(401, 'token_expired')),
      http.post(`${baseUrl}/auth/refresh`, () => HttpResponse.error()),
    );
    const session = fakeSession('stale-token');
    const api = createApiClient({ baseUrl, session });

    const { response } = await api.client.GET('/me');

    expect(response.status).toBe(401);
    expect(session.lost).toBe(1);
  });
});
