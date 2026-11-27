import type { ReactNode } from 'react';

type Tone = 'error' | 'success' | 'info';

const styles: Record<Tone, string> = {
  error: 'border-red-300 bg-red-50 text-red-800',
  success: 'border-green-300 bg-green-50 text-green-800',
  info: 'border-slate-300 bg-slate-50 text-slate-800',
};

interface AlertProps {
  tone?: Tone;
  children: ReactNode;
  className?: string;
}

// Errors use role="alert" (announced at once), the rest role="status" (announced politely).
export function Alert({ tone = 'error', children, className = '' }: AlertProps) {
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      aria-live={tone === 'error' ? 'assertive' : 'polite'}
      className={`rounded border px-3 py-2 text-sm ${styles[tone]} ${className}`}
    >
      {children}
    </div>
  );
}
