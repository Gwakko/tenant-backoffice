// A node name is shown as text only (React escapes it), cut with CSS and available in full in
// `title`. `dir="auto"` and an isolated bidi context keep a right-to-left name from reordering
// its neighbours; combining marks stay inside the text node (EC-125).
export function NodeName({ name, className = '' }: { name: string; className?: string }) {
  return (
    <span dir="auto" title={name} className={`block min-w-0 truncate [unicode-bidi:isolate] ${className}`}>
      {name}
    </span>
  );
}
