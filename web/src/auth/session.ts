// In-memory session holder. The access token must never be written to localStorage,
// sessionStorage or IndexedDB; a reload loses it and the refresh cookie restores the session (M5.2).
let accessToken: string | null = null;

export function getAccessToken(): string | null {
  return accessToken;
}

export function setAccessToken(token: string | null): void {
  accessToken = token;
}
