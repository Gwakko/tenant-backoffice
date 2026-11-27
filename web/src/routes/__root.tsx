import { createRootRouteWithContext, Outlet, useRouter } from '@tanstack/react-router';
import { useState } from 'react';

import { logout } from '@/auth/service';
import { useSession } from '@/auth/useSession';
import type { RouterContext } from '@/router';

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
});

function RootLayout() {
  const { api, queryClient } = Route.useRouteContext();
  const router = useRouter();
  const { authenticated, user } = useSession();
  const [signingOut, setSigningOut] = useState(false);

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
    <div className="min-h-screen bg-slate-50 text-slate-900">
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
      <main className="px-6 py-6">
        <Outlet />
      </main>
    </div>
  );
}
