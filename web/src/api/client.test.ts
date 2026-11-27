import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';

import { createApiClient } from './client';
import type { components } from './schema';

type Problem = components['schemas']['Problem'];

const baseUrl = new URL('/api/v1', window.location.origin).toString();
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

    const { data, error, response } = await createApiClient(baseUrl).GET('/me');

    expect(data).toBeUndefined();
    expect(response.status).toBe(404);
    expect(response.headers.get('Content-Type')).toBe('application/problem+json');
    expect(error?.status).toBe(404);
    expect(error?.title).toBe('Not found or no access');
    expect(error?.code).toBe('not_found');
  });

  it('sends requests under the configured base URL', async () => {
    let requested = '';
    server.use(
      http.get(`${baseUrl}/me`, ({ request }) => {
        requested = new URL(request.url).pathname;
        return HttpResponse.json(
          { type: 'about:blank', title: 'Unauthorized', status: 401, code: 'unauthorized', requestId: 'req-2' },
          { status: 401, headers: { 'Content-Type': 'application/problem+json' } },
        );
      }),
    );

    const { error } = await createApiClient(baseUrl).GET('/me');

    expect(requested).toBe('/api/v1/me');
    expect(error?.status).toBe(401);
  });
});
