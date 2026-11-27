import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/')({
  component: HomePage,
});

function HomePage() {
  return (
    <section>
      <h1 className="text-xl font-semibold">Tenant Backoffice</h1>
      <p className="mt-2 text-slate-600">The web client is being built. Sign-in arrives in the next milestone.</p>
    </section>
  );
}
