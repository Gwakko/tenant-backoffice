// Money input and output (spec 06 section 4, EC-121). Amounts are strings of minor units and are
// computed with BigInt only: no Number, no parseFloat, no floating point anywhere on the way.

export type Result<T, E> = { ok: true; value: T } | { ok: false; error: E };

export type MoneyError = 'empty' | 'invalid_format' | 'too_many_decimals' | 'too_large';

/** The largest amount the server accepts (int64). */
export const MAX_MINOR = 9223372036854775807n;

const MAX_INTEGER_DIGITS = 17;
const INPUT = /^(\d+)(?:\.(\d+))?$/;

/**
 * "10.50" -> "1050", "0.1" -> "10". Returns minor units as a string without leading zeros.
 * Accepts at most `scale` decimals and at most MAX_MINOR; everything else is an error.
 */
export function parseMoneyInput(input: string, scale = 2): Result<string, MoneyError> {
  const text = input.trim();
  if (text === '') return { ok: false, error: 'empty' };
  const match = INPUT.exec(text);
  if (match === null) return { ok: false, error: 'invalid_format' };
  const whole = match[1] ?? '';
  const fraction = match[2] ?? '';
  if (fraction.length > scale) return { ok: false, error: 'too_many_decimals' };
  if (whole.replace(/^0+(?=\d)/, '').length > MAX_INTEGER_DIGITS) return { ok: false, error: 'too_large' };
  const minor = BigInt(whole + fraction.padEnd(scale, '0'));
  if (minor > MAX_MINOR) return { ok: false, error: 'too_large' };
  return { ok: true, value: minor.toString() };
}

export const moneyErrorMessages: Record<MoneyError, string> = {
  empty: 'Enter an amount.',
  invalid_format: 'Enter an amount like 10 or 10.50.',
  too_many_decimals: 'Use at most two digits after the decimal point.',
  too_large: 'The amount is too large.',
};

function decimalSeparator(locale: string): string {
  const part = new Intl.NumberFormat(locale).formatToParts(1.1).find((p) => p.type === 'decimal');
  return part?.value ?? '.';
}

/**
 * "1050" -> "10.50". The grouping of the whole part is done on a BigInt; the amount never passes
 * through a floating point number. A value that is not a plain integer string is returned as is.
 */
export function formatMinor(minor: string, scale = 2, locale = 'en'): string {
  const match = /^(-?)(\d+)$/.exec(minor);
  if (match === null) return minor;
  const sign = match[1] ?? '';
  const digits = (match[2] ?? '').padStart(scale + 1, '0');
  const whole = BigInt(digits.slice(0, digits.length - scale)).toLocaleString(locale);
  if (scale === 0) return `${sign}${whole}`;
  return `${sign}${whole}${decimalSeparator(locale)}${digits.slice(digits.length - scale)}`;
}

/** The same amount for an input field: "1050" -> "10.50", no grouping, so parseMoneyInput accepts it back. */
export function minorToInput(minor: string, scale = 2): string {
  const match = /^(\d+)$/.exec(minor);
  if (match === null) return minor;
  const digits = (match[1] ?? '').padStart(scale + 1, '0');
  const whole = digits.slice(0, digits.length - scale).replace(/^0+(?=\d)/, '');
  return scale === 0 ? whole : `${whole}.${digits.slice(digits.length - scale)}`;
}
