import { describe, expect, it } from 'vitest';

import source from './money.ts?raw';
import { formatMinor, MAX_MINOR, minorToInput, parseMoneyInput } from './money';

function value(input: string): string {
  const result = parseMoneyInput(input);
  if (!result.ok) throw new Error(`expected ${input} to parse, got ${result.error}`);
  return result.value;
}

describe('parseMoneyInput', () => {
  it('[T-W-05] "10.50" is "1050", "0.1" is "10", whole numbers get two zeros', () => {
    expect(value('10.50')).toBe('1050');
    expect(value('0.1')).toBe('10');
    expect(value('10')).toBe('1000');
    expect(value('0')).toBe('0');
    expect(value('007.5')).toBe('750');
  });

  it('[T-W-05] "10.505" is an error, not a rounded value', () => {
    expect(parseMoneyInput('10.505')).toEqual({ ok: false, error: 'too_many_decimals' });
  });

  it('[T-W-05] the largest int64 is accepted and one more is refused', () => {
    expect(value('92233720368547758.07')).toBe('9223372036854775807');
    expect(parseMoneyInput('92233720368547758.08')).toEqual({ ok: false, error: 'too_large' });
    expect(parseMoneyInput('100000000000000000')).toEqual({ ok: false, error: 'too_large' });
  });

  it('[T-W-05] exponent, signs, separators and empty input are errors', () => {
    for (const bad of ['1e3', '-1', '+1', '1,5', '1.', '.5', 'abc', '1 0', '0x10', 'NaN', 'Infinity']) {
      expect(parseMoneyInput(bad).ok, bad).toBe(false);
    }
    expect(parseMoneyInput('')).toEqual({ ok: false, error: 'empty' });
    expect(parseMoneyInput('   ')).toEqual({ ok: false, error: 'empty' });
  });

  it('[T-W-05] 1000 random strings agree with an independent BigInt computation', () => {
    let seed = 20260101;
    const next = (n: number): number => {
      seed = (seed * 1103515245 + 12345) % 2147483648;
      return seed % n;
    };
    const digits = (count: number): string => Array.from({ length: count }, () => String(next(10))).join('');
    for (let i = 0; i < 1000; i++) {
      const whole = digits(1 + next(19));
      const fractionLength = next(4);
      const input = fractionLength === 0 ? whole : `${whole}.${digits(fractionLength)}`;
      const result = parseMoneyInput(input);
      const [w = '', f = ''] = input.split('.');
      if (f.length > 2) {
        expect(result, input).toEqual({ ok: false, error: 'too_many_decimals' });
        continue;
      }
      const expected = BigInt(w) * 100n + BigInt(f.padEnd(2, '0') === '' ? '0' : f.padEnd(2, '0'));
      if (expected > MAX_MINOR) {
        expect(result.ok, input).toBe(false);
      } else {
        expect(result, input).toEqual({ ok: true, value: expected.toString() });
      }
    }
  });

  it('[T-W-05] the source has no floating point arithmetic on amounts', () => {
    const code = source.replace(/\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '');
    expect(code).not.toMatch(/\bparseFloat\b|\bNumber\s*\(|\bNumber\.parse|\bMath\./);
  });
});

describe('formatMinor', () => {
  it('[T-W-05] formats minor units with grouping, without float', () => {
    expect(formatMinor('1050')).toBe('10.50');
    expect(formatMinor('5')).toBe('0.05');
    expect(formatMinor('0')).toBe('0.00');
    expect(formatMinor('123456789')).toBe('1,234,567.89');
    expect(formatMinor('9223372036854775807')).toBe('92,233,720,368,547,758.07');
  });

  it('[T-W-05] leaves a value that is not an integer string as it is', () => {
    expect(formatMinor('10.5')).toBe('10.5');
    expect(formatMinor('')).toBe('');
  });

  it('[T-W-05] round trip: parse(minorToInput(x)) is x', () => {
    for (const minor of ['0', '1', '10', '1050', '9223372036854775807']) {
      expect(value(minorToInput(minor))).toBe(minor);
    }
  });
});
