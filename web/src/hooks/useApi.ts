import { useRouteContext } from '@tanstack/react-router';

import type { Api } from '@/api/client';

// The API client lives in the router context (main.tsx); screens and features read it here.
export function useApi(): Api {
  return useRouteContext({ from: '__root__' }).api;
}
