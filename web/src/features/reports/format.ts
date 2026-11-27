import { formatMinor } from '@/lib/money';

/** A report sum (a signed integer string of minor units, may be below zero) as text, via BigInt. */
export function formatSum(sum: string): string {
  return formatMinor(sum);
}

/**
 * Bar heights in percent (0..100) for signed sums. Everything is computed with BigInt, so values
 * above 2^53 keep their proportions; only the small final percentage becomes a number.
 */
export function barPercents(sums: readonly string[]): number[] {
  const values = sums.map((sum) => {
    const value = BigInt(sum);
    return value < 0n ? -value : value;
  });
  const max = values.reduce((top, value) => (value > top ? value : top), 0n);
  if (max === 0n) return values.map(() => 0);
  return values.map((value) => Number((value * 100n) / max));
}
