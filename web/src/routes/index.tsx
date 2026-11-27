import { createFileRoute } from '@tanstack/react-router';

import { requireSession } from '@/auth/guard';
import { useSession } from '@/auth/useSession';

export const Route = createFileRoute('/')({
  beforeLoad: ({ context, location }) => requireSession(context.api, location.href),
  component: HomePage,
});

function HomePage() {
  const { user } = useSession();
  return (
    <section>
      <h1 className="text-xl font-semibold">Tenant Backoffice</h1>
      <p className="mt-2 text-slate-600">
        {user ? `Signed in as ${user.email}.` : 'Signed in.'} Select a node in the tree.
      </p>
    </section>
  );
}
