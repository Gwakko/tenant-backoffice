import type { ReactNode } from 'react';

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="rounded border border-dashed border-slate-300 px-4 py-6 text-center text-sm text-slate-600">{children}</p>;
}
