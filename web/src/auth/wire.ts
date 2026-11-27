import type { QueryClient } from '@tanstack/react-query';
import type { AnyRouter } from '@tanstack/react-router';

import { loginSearch } from './redirect';
import { onSessionLost } from './session';

// When the session is lost (refresh failed): clear the Query cache and go to /login?redirect=...
export function wireSessionLoss(queryClient: QueryClient, router: AnyRouter): () => void {
  return onSessionLost(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    const location = router.state.location;
    if (location.pathname === '/login') return;
    void router.navigate({ to: '/login', search: loginSearch(location.href), replace: true });
  });
}
