import createClient from 'openapi-fetch';

import { apiBaseUrl } from './config';
import type { paths } from './schema';

export type ApiClient = ReturnType<typeof createApiClient>;

// credentials: 'same-origin' lets the browser send the HttpOnly refresh cookie to /api/v1/auth.
// The access token is never stored in web storage; M5.2 attaches it from memory (src/auth).
export function createApiClient(baseUrl: string = apiBaseUrl) {
  return createClient<paths>({ baseUrl, credentials: 'same-origin' });
}

export const apiClient = createApiClient();
