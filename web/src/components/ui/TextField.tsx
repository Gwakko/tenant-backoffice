import { useId, type InputHTMLAttributes } from 'react';

interface TextFieldProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  error?: string | undefined;
  hint?: string;
}

// Label, hint and error are tied to the input (htmlFor, aria-describedby, aria-invalid).
// `ref` is a normal prop in React 19, so react-hook-form `register()` can be spread into it.
export function TextField({ label, error, hint, className = '', ...rest }: TextFieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : null, error ? errorId : null].filter(Boolean).join(' ');
  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium">
        {label}
      </label>
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy === '' ? undefined : describedBy}
        className={`mt-1 w-full rounded border border-slate-300 px-3 py-2 ${className}`}
        {...rest}
      />
      {hint && (
        <p id={hintId} className="mt-1 text-xs text-slate-600">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} role="alert" className="mt-1 text-sm text-red-700">
          {error}
        </p>
      )}
    </div>
  );
}
