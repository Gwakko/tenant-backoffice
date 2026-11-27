import { describe, expect, it } from 'vitest';

import { barPercents, formatSum } from './format';
import { resolvePeriod } from './search';

describe('report sums', () => {
  it('[T-W-05] formats signed sums exactly, also above 2^53 and above int64', () => {
    expect(formatSum('0')).toBe('0.00');
    expect(formatSum('-5')).toBe('-0.05');
    expect(formatSum('9007199254740993')).toBe('90,071,992,547,409.93');
    expect(formatSum('-9007199254740993')).toBe('-90,071,992,547,409.93');
    expect(formatSum('123456789012345678901234567890')).toBe('1,234,567,890,123,456,789,012,345,678.90');
  });

  it('bar heights are proportional and exact for huge values', () => {
    expect(barPercents(['0', '0'])).toEqual([0, 0]);
    expect(barPercents(['100', '50', '-100'])).toEqual([100, 50, 100]);
    // 2^53 + 1 against 2^53: equal as floats, 99 percent as BigInt.
    expect(barPercents(['9007199254740993', '9007199254740992'])).toEqual([100, 99]);
  });

  it('the default period is the last 30 days up to tomorrow (UTC), or 30 days after a given start', () => {
    const now = new Date('2026-03-31T12:00:00Z');
    expect(resolvePeriod({}, now)).toEqual({ from: '2026-03-02', to: '2026-04-01' });
    expect(resolvePeriod({ from: '2026-01-01' }, now)).toEqual({ from: '2026-01-01', to: '2026-01-31' });
    expect(resolvePeriod({ to: '2026-02-01' }, now)).toEqual({ from: '2026-01-02', to: '2026-02-01' });
  });
});
