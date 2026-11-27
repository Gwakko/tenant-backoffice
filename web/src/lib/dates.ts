// Calendar dates as `YYYY-MM-DD` strings and UTC timestamps, with the standard Date only
// (no date library). Pure functions: the clock is passed in where it matters.

const DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DAY_MS = 24 * 60 * 60 * 1000;

/** The longest report period the server accepts, in days. */
export const MAX_PERIOD_DAYS = 366;

function epochDay(text: string): number | null {
  const match = DATE.exec(text);
  if (match === null) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const at = Date.UTC(year, month - 1, day);
  const back = new Date(at);
  // Date rolls 2026-02-31 over to March: the round trip tells a real date from a rolled one.
  if (back.getUTCFullYear() !== year || back.getUTCMonth() !== month - 1 || back.getUTCDate() !== day) return null;
  return at / DAY_MS;
}

/** A real calendar date in the `YYYY-MM-DD` form. */
export function isRealDate(text: string): boolean {
  return epochDay(text) !== null;
}

/** `to - from` in whole days; null when either side is not a real date. */
export function daysBetween(from: string, to: string): number | null {
  const a = epochDay(from);
  const b = epochDay(to);
  return a === null || b === null ? null : b - a;
}

export function addDays(date: string, days: number): string {
  const day = epochDay(date);
  if (day === null) return date;
  return new Date((day + days) * DAY_MS).toISOString().slice(0, 10);
}

export function utcDate(now: Date): string {
  return now.toISOString().slice(0, 10);
}

export type PeriodProblem = 'invalid_date' | 'not_ordered' | 'too_long';

// The same rules as the server (`invalid_period`, spec 05 section 2): real dates, from < to, at most 366 days.
export function checkPeriod(from: string, to: string): PeriodProblem | null {
  const days = daysBetween(from, to);
  if (days === null) return 'invalid_date';
  if (days <= 0) return 'not_ordered';
  if (days > MAX_PERIOD_DAYS) return 'too_long';
  return null;
}

/** "2026-03-01T10:20:30.123Z" -> "2026-03-01 10:20:30 UTC". An unreadable value is returned unchanged. */
export function formatUtc(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return `${at.toISOString().slice(0, 19).replace('T', ' ')} UTC`;
}

/** The start of a UTC day as a timestamp for the API: "2026-03-01" -> "2026-03-01T00:00:00Z". */
export function utcStartOfDay(date: string): string {
  return `${date}T00:00:00Z`;
}
