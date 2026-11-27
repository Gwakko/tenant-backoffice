import type { QueryClient } from '@tanstack/react-query';
import { createRouter, type RouterHistory } from '@tanstack/react-router';

import type { Api } from '@/api/client';

import { routeTree } from './routeTree.gen';

export interface RouterContext {
  queryClient: QueryClient;
  api: Api;
}

export function createAppRouter(queryClient: QueryClient, api: Api, history?: RouterHistory) {
  return createRouter({
    routeTree,
    context: { queryClient, api },
    defaultPreload: 'intent',
    ...(history ? { history } : {}),
  });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
