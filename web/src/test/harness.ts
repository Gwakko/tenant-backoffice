import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { HttpResponse } from 'msw';

import { createApiClient, type SessionPort } from '@/api/client';
import { resetRestoreAttempt, sessionPort } from '@/auth/service';
import { clearSession } from '@/auth/session';
import { wireSessionLoss } from '@/auth/wire';
import { createAppRouter } from '@/router';

// An absolute base URL: Node's Request does not accept a relative one.
export const baseUrl = new URL('/api/v1', window.location.origin).toString();

export const testUser = { id: '0190f2a0-0000-7000-8000-000000000001', email: 'ann@example.test' };

export function tokenBody(accessToken: string) {
  return { accessToken, expiresIn: 600, user: testUser };
}

export function problemResponse(status: number, code: string, headers: Record<string, string> = {}, detail?: string) {
  return HttpResponse.json(
    { type: 'about:blank', title: code, status, code, requestId: 'req-test-0001', ...(detail ? { detail } : {}) },
    { status, headers: { 'Content-Type': 'application/problem+json', ...headers } },
  );
}

export interface FakeSession extends SessionPort {
  token: string | null;
  lost: number;
}

// A session port backed by a plain variable, for tests of the client alone.
export function fakeSession(initialToken: string | null): FakeSession {
  const session: FakeSession = {
    token: initialToken,
    lost: 0,
    getAccessToken: () => session.token,
    onTokens: (tokens) => {
      session.token = tokens.accessToken;
    },
    onSessionLost: () => {
      session.token = null;
      session.lost += 1;
    },
  };
  return session;
}

export function resetAuthState(): void {
  clearSession();
  resetRestoreAttempt();
}

// The real app wiring (router, Query cache, API client, in-memory session) on a memory history.
export function createTestApp(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const api = createApiClient({ baseUrl, session: sessionPort });
  const router = createAppRouter(queryClient, api, createMemoryHistory({ initialEntries: [path] }));
  const unwire = wireSessionLoss(queryClient, router);
  return { queryClient, api, router, unwire };
}
