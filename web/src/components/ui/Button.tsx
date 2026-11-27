import type { ButtonHTMLAttributes } from 'react';

type Variant = 'primary' | 'secondary' | 'danger';

const styles: Record<Variant, string> = {
  primary: 'bg-slate-900 text-white hover:bg-slate-800',
  secondary: 'border border-slate-300 bg-white text-slate-900 hover:bg-slate-100',
  danger: 'bg-red-700 text-white hover:bg-red-800',
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  /** The action is running: the button is disabled and announces the busy state. */
  busy?: boolean;
}

export function Button({ variant = 'primary', busy = false, disabled, type = 'button', className = '', ...rest }: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled === true || busy}
      aria-busy={busy || undefined}
      className={`rounded px-3 py-1.5 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-slate-900 disabled:cursor-not-allowed disabled:opacity-50 ${styles[variant]} ${className}`}
      {...rest}
    />
  );
}
