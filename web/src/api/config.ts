// The API base URL comes from the build environment; the default is the path the
// reverse proxy serves, so the browser always talks to its own origin.
export const apiBaseUrl: string = import.meta.env.VITE_API_BASE_URL ?? '/api/v1';
