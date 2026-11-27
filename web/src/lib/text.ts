/** Cuts a long text and marks the cut. Server text is only ever shown as text, never as HTML. */
export function truncate(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max)}...` : text;
}

const VALUE_LIMIT = 200;
const TOTAL_LIMIT = 1000;

/**
 * A readable JSON text of an audit snapshot: every value and the whole text are cut, so a hostile
 * or huge record cannot flood the screen. The result is plain text for a <pre>.
 */
export function previewJson(value: Readonly<Record<string, string>> | null): string {
  if (value === null) return '';
  const cut: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) cut[truncate(key, VALUE_LIMIT)] = truncate(item, VALUE_LIMIT);
  return truncate(JSON.stringify(cut, null, 2), TOTAL_LIMIT);
}
