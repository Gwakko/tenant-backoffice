import { createRootRouteWithContext, Outlet, useRouter, useRouterState } from '@tanstack/react-router';
import { useState } from 'react';

import { logout } from '@/auth/service';
import { useSession } from '@/auth/useSession';
import { NodeTree, nodeIdFromPath } from '@/features/tree';
import type { RouterContext } from '@/router';

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
});

function RootLayout() {
  const { api, queryClient } = Route.useRouteContext();
  const router = useRouter();
  const { authenticated, user } = useSession();
  const [signingOut, setSigningOut] = useState(false);
  // The route files of the node card are not known here: the current node is read from the path.
  const currentNodeId = useRouterState({ select: (state) => nodeIdFromPath(state.location.pathname) });

  const signOut = async () => {
    if (signingOut) return;
    setSigningOut(true);
    try {
      await logout(api, queryClient);
    } finally {
      setSigningOut(false);
    }
    router.history.replace('/login');
  };

  return (
    <div className="flex min-h-screen flex-col bg-slate-50 text-slate-900">
      <header className="flex items-center justify-between border-b border-slate-200 bg-white px-6 py-3">
        <span className="font-semibold">Tenant Backoffice</span>
        {authenticated && (
          <div className="flex items-center gap-3 text-sm">
            <span className="text-slate-600">{user?.email}</span>
            <button
              type="button"
              onClick={() => void signOut()}
              disabled={signingOut}
              className="rounded border border-slate-300 px-3 py-1 hover:bg-slate-100 disabled:opacity-50"
            >
              Sign out
            </button>
          </div>
        )}
      </header>
      <div className="flex min-h-0 flex-1">
        {authenticated && (
          <aside aria-label="Node tree" className="w-72 shrink-0 overflow-y-auto border-r border-slate-200 bg-white p-3">
            <NodeTree currentNodeId={currentNodeId} />
          </aside>
        )}
        <main className="min-w-0 flex-1 px-6 py-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
