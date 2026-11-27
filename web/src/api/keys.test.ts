import { describe, expect, it } from 'vitest';
import { keys } from './keys';

describe('query keys', () => {
  it('a key without filters is a prefix of the key with filters, so one invalidation covers both', () => {
    const prefix = keys.children('n1');
    const full = keys.children('n1', { status: 'active' });
    expect(full.slice(0, prefix.length)).toEqual([...prefix]);
  });

  it('different nodes never share a key', () => {
    expect(keys.node('a')).not.toEqual(keys.node('b'));
    expect(keys.operations('a', { kind: 'credit' })).not.toEqual(keys.operations('b', { kind: 'credit' }));
  });

  it('every list key starts with its own name, so the names in the spec are the invalidation prefixes', () => {
    expect(keys.me()[0]).toBe('me');
    expect(keys.balance('x')[0]).toBe('balance');
    expect(keys.reportSummary('x')[0]).toBe('report-summary');
    expect(keys.audit('x')[0]).toBe('audit');
  });
});
