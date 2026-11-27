import { describe, expect, it } from 'vitest';

import { loginSearch, safeRedirect } from './redirect';

describe('safeRedirect', () => {
  const rejected: [string, unknown][] = [
    ['protocol-relative', '//evil.test'],
    ['protocol-relative with path', '//evil.test/nodes'],
    ['triple slash', '///evil.test'],
    ['backslash after slash', '/\\evil.test'],
    ['backslash inside', '/nodes\\..\\evil'],
    ['absolute https URL', 'https://evil.test'],
    ['absolute http URL', 'http://evil.test/nodes'],
    ['javascript scheme', 'javascript:alert(1)'],
    ['data scheme', 'data:text/html,<script>alert(1)</script>'],
    ['encoded protocol-relative', '%2F%2Fevil.test'],
    ['slash then encoded slash', '/%2Fevil.test'],
    ['encoded backslash', '/%5Cevil.test'],
    ['double-encoded protocol-relative', '/%252F%252Fevil.test'],
    ['triple-encoded protocol-relative', '/%25252F%25252Fevil.test'],
    ['tab inside the prefix', '/\t/evil.test'],
    ['encoded tab', '/%09/evil.test'],
    ['newline', '/\n/evil.test'],
    ['space', '/ /evil.test'],
    ['malformed encoding', '/%E0%A4%A'],
    ['empty string', ''],
    ['relative without a slash', 'nodes/1'],
    ['dot segments without a slash', '../evil'],
    ['very long string', `/${'a'.repeat(10_000)}`],
    ['just over the limit', `/${'a'.repeat(512)}`],
    ['not a string (undefined)', undefined],
    ['not a string (array)', ['/nodes']],
    ['not a string (object)', { toString: () => '/nodes' }],
  ];

  it.each(rejected)('falls back to / for %s', (_name, value) => {
    expect(safeRedirect(value)).toBe('/');
  });

  const accepted: [string, string, string][] = [
    ['root', '/', '/'],
    ['a path', '/nodes/0190f2a0', '/nodes/0190f2a0'],
    ['path with search and hash', '/nodes/1/operations?kind=credit&page=2#top', '/nodes/1/operations?kind=credit&page=2#top'],
    ['encoded value inside a query', '/search?q=a%2Fb', '/search?q=a%2Fb'],
    ['a scheme-like text after the slash (still a path on this origin)', '/javascript:alert(1)', '/javascript:alert(1)'],
    ['a URL inside a query', '/nodes?next=https://other.test', '/nodes?next=https://other.test'],
  ];

  it.each(accepted)('keeps %s', (_name, value, expected) => {
    expect(safeRedirect(value)).toBe(expected);
  });

  it('never redirects back to the login page', () => {
    expect(safeRedirect('/login')).toBe('/');
    expect(safeRedirect('/login?redirect=/nodes')).toBe('/');
  });

  it('builds login search params only when there is somewhere to return to', () => {
    expect(loginSearch('/')).toEqual({});
    expect(loginSearch('/nodes/1?tab=ops')).toEqual({ redirect: '/nodes/1?tab=ops' });
    expect(loginSearch('//evil.test')).toEqual({});
  });
});
