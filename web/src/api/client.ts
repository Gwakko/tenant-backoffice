import createClient, { type Client, type Middleware } from 'openapi-fetch';

import { randomUuid } from '@/lib/uuid';

import { apiBaseUrl } from './config';
import type { components, paths } from './schema';

export type TokenResponse = components['schemas']['TokenResponse'];

export const CSRF_HEADER = 'X-TB-CSRF';

// The client does not know about React or the session store; the app hands it this port.
export interface SessionPort {
  /** The current access token, read from memory on every request. */
  getAccessToken(): string | null;
  /** A refresh succeeded: remember the new tokens in memory. */
  onTokens(tokens: TokenResponse): void;
  /** The session cannot be restored (refresh failed): drop it and send the user to /login. */
  onSessionLost(): void;
}

export interface ApiOptions {
  baseUrl?: string;
  session: SessionPort;
  fetch?: (input: Request) => Promise<Response>;
}

export interface Api {
  client: Client<paths>;
  /** Single-flight refresh through the HttpOnly cookie; resolves false when it fails. */
  refreshSession(): Promise<boolean>;
}

const REFRESHABLE_CODES: ReadonlySet<string> = new Set(['token_expired', 'token_invalid']);

function isAuthPath(schemaPath: string): boolean {
  return schemaPath.startsWith('/auth/');
}

// Login and refresh are never retried through a refresh (a failed refresh must not start another).
function isRefreshExempt(schemaPath: string): boolean {
  return schemaPath === '/auth/login' || schemaPath === '/auth/refresh';
}

function needsCsrfHeader(schemaPath: string): boolean {
  return schemaPath === '/auth/refresh' || schemaPath === '/auth/logout';
}

async function isRefreshableUnauthorized(response: Response): Promise<boolean> {
  if (response.status !== 401) return false;
  try {
    const body: unknown = await response.clone().json();
    if (typeof body !== 'object' || body === null || !('code' in body)) return false;
    return typeof body.code === 'string' && REFRESHABLE_CODES.has(body.code);
  } catch {
    return false;
  }
}

// Shared by the main client and the bare client used for refresh: cookie credentials only for
// /auth/*, the CSRF header on refresh and logout, a request id on everything.
function prepareRequest(request: Request, schemaPath: string): Request {
  const prepared = isAuthPath(schemaPath) ? new Request(request, { credentials: 'include' }) : request;
  if (needsCsrfHeader(schemaPath)) prepared.headers.set(CSRF_HEADER, '1');
  if (!prepared.headers.has('X-Request-Id')) prepared.headers.set('X-Request-Id', randomUuid());
  return prepared;
}

interface PendingRetry {
  copy: Request;
  tokenSent: string | null;
}

export function createApiClient(options: ApiOptions): Api {
  const { session } = options;
  const baseUrl = options.baseUrl ?? apiBaseUrl;
  const send = (request: Request): Promise<Response> =>
    options.fetch ? options.fetch(request) : globalThis.fetch(request);
  const transport: Middleware = {
    onRequest: ({ request, schemaPath }) => prepareRequest(request, schemaPath),
  };

  // The refresh call goes through a client without the auth middleware, so it cannot recurse.
  const bare = createClient<paths>({ baseUrl, credentials: 'omit', fetch: send });
  bare.use(transport);

  let refreshFlight: Promise<boolean> | null = null;
  function refreshSession(): Promise<boolean> {
    refreshFlight ??= (async () => {
      try {
        const { data } = await bare.POST('/auth/refresh');
        if (!data) return false;
        session.onTokens(data);
        return true;
      } catch {
        return false;
      } finally {
        refreshFlight = null;
      }
    })();
    return refreshFlight;
  }

  // The session is reported lost once per refresh flight, however many requests were waiting.
  let lossFlight: Promise<boolean> | null = null;
  function refreshOrLoseSession(): Promise<boolean> {
    lossFlight ??= refreshSession()
      .then((ok) => {
        if (!ok) session.onSessionLost();
        return ok;
      })
      .finally(() => {
        lossFlight = null;
      });
    return lossFlight;
  }

  const pending = new Map<string, PendingRetry>();

  const auth: Middleware = {
    onRequest({ request, schemaPath, id }) {
      const prepared = prepareRequest(request, schemaPath);
      if (isRefreshExempt(schemaPath)) return prepared;
      const token = session.getAccessToken();
      if (token !== null) prepared.headers.set('Authorization', `Bearer ${token}`);
      // The copy is taken after all headers are set, before the body is consumed by the send.
      pending.set(id, { copy: prepared.clone(), tokenSent: token });
      return prepared;
    },

    async onResponse({ response, schemaPath, id }) {
      const retry = pending.get(id);
      pending.delete(id);
      if (!retry || isRefreshExempt(schemaPath)) return undefined;
      if (!(await isRefreshableUnauthorized(response))) return undefined;

      const current = session.getAccessToken();
      if (current !== retry.tokenSent) {
        // The token changed while this request was in flight: another request already refreshed
        // (or the session was lost). Do not refresh a second time.
        if (current === null) return undefined;
      } else if (!(await refreshOrLoseSession())) {
        return undefined;
      }

      const fresh = session.getAccessToken();
      if (fresh === null) return undefined;
      retry.copy.headers.set('Authorization', `Bearer ${fresh}`);
      // The retry bypasses the middleware: whatever it returns is final (one retry at most).
      return send(retry.copy);
    },

    onError({ id }) {
      pending.delete(id);
    },
  };

  const client = createClient<paths>({ baseUrl, credentials: 'omit', fetch: send });
  client.use(auth);
  return { client, refreshSession };
}
