// In-memory session holder. The access token lives only in this module's variable: never in
// localStorage, sessionStorage, IndexedDB, a cookie or a URL. A reload loses it and the HttpOnly
// refresh cookie restores the session through POST /auth/refresh.
export interface SessionUser {
  id: string;
  email: string;
}

export interface SessionSnapshot {
  authenticated: boolean;
  user: SessionUser | null;
}

interface TokenResponse {
  accessToken: string;
  user: SessionUser;
}

const signedOut: SessionSnapshot = { authenticated: false, user: null };

let accessToken: string | null = null;
let snapshot: SessionSnapshot = signedOut;
const listeners = new Set<() => void>();
const lossListeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

export function getAccessToken(): string | null {
  return accessToken;
}

export function hasSession(): boolean {
  return accessToken !== null;
}

export function setSession(tokens: TokenResponse): void {
  accessToken = tokens.accessToken;
  snapshot = { authenticated: true, user: { id: tokens.user.id, email: tokens.user.email } };
  emit();
}

export function clearSession(): void {
  accessToken = null;
  snapshot = signedOut;
  emit();
}

// The snapshot never carries the token: components read who is signed in, not the credential.
export function getSnapshot(): SessionSnapshot {
  return snapshot;
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// Called when the session ended without the user asking for it (refresh failed).
export function onSessionLost(listener: () => void): () => void {
  lossListeners.add(listener);
  return () => {
    lossListeners.delete(listener);
  };
}

export function notifySessionLost(): void {
  clearSession();
  for (const listener of lossListeners) listener();
}
