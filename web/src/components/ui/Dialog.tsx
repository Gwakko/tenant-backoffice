import { useEffect, useId, useRef, type ReactNode } from 'react';

interface DialogProps {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
}

// Native <dialog> opened modally: the browser traps focus, closes on Escape and marks the rest of
// the page inert. The children exist only while the dialog is open, so a form inside it starts
// clean (and takes a fresh idempotency key) every time it opens.
export function Dialog({ open, title, onClose, children }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (dialog === null) return;
    if (open && !dialog.open) {
      if (typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
    }
    if (!open && dialog.open) {
      if (typeof dialog.close === 'function') dialog.close();
      else dialog.removeAttribute('open');
    }
  }, [open]);

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      className="m-auto w-full max-w-md rounded-lg border border-slate-300 p-0 backdrop:bg-slate-900/40"
    >
      {open && (
        <div className="space-y-4 p-5">
          <h2 id={titleId} className="text-lg font-semibold">
            {title}
          </h2>
          {children}
        </div>
      )}
    </dialog>
  );
}
