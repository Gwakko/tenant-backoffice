import { describe, expect, it } from 'vitest';

import { addDays, checkPeriod, daysBetween, formatUtc, isRealDate, utcStartOfDay } from './dates';
import { uniqueById } from './list';
import { previewJson, truncate } from './text';

describe('dates', () => {
  it('knows a real calendar date from a rolled one', () => {
    expect(isRealDate('2026-02-28')).toBe(true);
    expect(isRealDate('2028-02-29')).toBe(true);
    for (const bad of ['2026-02-29', '2026-02-31', '2026-13-01', '2026-00-10', '26-01-01', '2026-1-1', '', 'banana']) {
      expect(isRealDate(bad), bad).toBe(false);
    }
  });

  it('counts days and moves dates', () => {
    expect(daysBetween('2026-01-01', '2026-01-02')).toBe(1);
    expect(daysBetween('2026-01-01', '2027-01-02')).toBe(366);
    expect(daysBetween('2026-01-01', 'x')).toBeNull();
    expect(addDays('2026-02-28', 1)).toBe('2026-03-01');
    expect(addDays('2026-01-01', -1)).toBe('2025-12-31');
  });

  it('checks a period like the server: real dates, from < to, at most 366 days', () => {
    expect(checkPeriod('2026-01-01', '2027-01-02')).toBeNull();
    expect(checkPeriod('2026-01-01', '2027-01-03')).toBe('too_long');
    expect(checkPeriod('2026-01-01', '2026-01-01')).toBe('not_ordered');
    expect(checkPeriod('2026-01-02', '2026-01-01')).toBe('not_ordered');
    expect(checkPeriod('2026-02-30', '2026-03-01')).toBe('invalid_date');
    expect(checkPeriod('', '2026-03-01')).toBe('invalid_date');
  });

  it('formats a timestamp in UTC with the zone named', () => {
    expect(formatUtc('2026-03-01T10:20:30Z')).toBe('2026-03-01 10:20:30 UTC');
    expect(formatUtc('2026-03-01T12:20:30+02:00')).toBe('2026-03-01 10:20:30 UTC');
    expect(formatUtc('not a date')).toBe('not a date');
    expect(utcStartOfDay('2026-03-01')).toBe('2026-03-01T00:00:00Z');
  });
});

describe('lists and text', () => {
  it('uniqueById keeps the first of each id and the order', () => {
    expect(uniqueById([{ id: 'a', n: 1 }, { id: 'b', n: 2 }, { id: 'a', n: 3 }])).toEqual([
      { id: 'a', n: 1 },
      { id: 'b', n: 2 },
    ]);
  });

  it('truncate and previewJson cut long values and keep markup as plain text', () => {
    expect(truncate('abcdef', 3)).toBe('abc...');
    expect(truncate('abc', 3)).toBe('abc');
    expect(previewJson(null)).toBe('');
    const text = previewJson({ name: '<b>x</b>', long: 'y'.repeat(500) });
    expect(text).toContain('<b>x</b>');
    expect(text.length).toBeLessThan(500);
    const many = Object.fromEntries(Array.from({ length: 200 }, (_, i) => [`k${String(i)}`, 'v']));
    expect(previewJson(many).length).toBeLessThanOrEqual(1003);
  });
});
