import type { QueryClient } from '@tanstack/react-query';

import { networkProblem, parseProblem, type ApiProblem } from '@/api/problem';
import type { Api, SessionPort } from '@/api/client';

import { clearSession, getAccessToken, notifySessionLost, setSession } from './session';

export interface Credentials {
  email: string;
  password: string;
}

export type LoginResult = { ok: true } | { ok: false; problem: ApiProblem };

// Wires the API client to the in-memory session.
export const sessionPort: SessionPort = {
  getAccessToken,
  onTokens: setSession,
  onSessionLost: notifySessionLost,
};

// One attempt to restore the session from the refresh cookie per page load; later guard checks
// only look at the in-memory state.
let restoreAttempted = false;

export function resetRestoreAttempt(): void {
  restoreAttempted = false;
}

export async function ensureSession(api: Api): Promise<boolean> {
  if (getAccessToken() !== null) return true;
  if (restoreAttempted) return false;
  restoreAttempted = true;
  return api.refreshSession();
}

export async function login(api: Api, credentials: Credentials): Promise<LoginResult> {
  try {
    const { data, error, response } = await api.client.POST('/auth/login', { body: credentials });
    if (data) {
      setSession(data);
      restoreAttempted = true;
      return { ok: true };
    }
    return { ok: false, problem: parseProblem(response, error) };
  } catch {
    return { ok: false, problem: networkProblem() };
  }
}

// Ends the session. Local state is dropped even if the server cannot be reached.
export async function logout(api: Api, queryClient: QueryClient): Promise<void> {
  try {
    await api.client.POST('/auth/logout');
  } catch {
    // Offline: the refresh cookie stays valid until it expires; the local session is still cleared.
  } finally {
    clearSession();
    restoreAttempted = true;
    await queryClient.cancelQueries();
    queryClient.clear();
  }
}
