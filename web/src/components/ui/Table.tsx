import type { ReactNode, TdHTMLAttributes, ThHTMLAttributes } from 'react';

// A plain data table: the caption names it for assistive technology, the wrapper scrolls on a
// narrow screen instead of breaking the layout.
export function Table({ caption, children }: { caption: string; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded border border-slate-200 bg-white">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{caption}</caption>
        {children}
      </table>
    </div>
  );
}

export function Th({ className = '', ...rest }: ThHTMLAttributes<HTMLTableCellElement>) {
  return <th scope="col" className={`whitespace-nowrap border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium ${className}`} {...rest} />;
}

export function Td({ className = '', ...rest }: TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={`border-b border-slate-100 px-3 py-2 align-top ${className}`} {...rest} />;
}
