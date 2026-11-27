import { describe, expect, it } from 'vitest';

import { describeProblem, networkProblem, parseProblem, parseRetryAfter, problemMessages, safeText } from './problem';

function problemResponse(status: number, body: unknown, headers: Record<string, string> = {}): [Response, unknown] {
  return [
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/problem+json', ...headers } }),
    body,
  ];
}

describe('problem parsing', () => {
  it('reads code, detail, field errors and request id from application/problem+json', () => {
    const [response, body] = problemResponse(400, {
      type: 'about:blank',
      title: 'Validation failed',
      status: 400,
      code: 'validation_failed',
      requestId: 'req-1',
      errors: [{ field: 'name', code: 'invalid_name', message: 'too long' }],
    });

    const problem = parseProblem(response, body);

    expect(problem.status).toBe(400);
    expect(problem.code).toBe('validation_failed');
    expect(problem.requestId).toBe('req-1');
    expect(problem.errors).toEqual([{ field: 'name', message: 'too long' }]);
  });

  it('ignores a body that is not JSON content or does not match the shape', () => {
    const html = new Response('<html>oops</html>', { status: 502, headers: { 'Content-Type': 'text/html' } });
    expect(parseProblem(html, '<html>oops</html>').code).toBeUndefined();

    const [response] = problemResponse(500, { code: 42 });
    expect(parseProblem(response, { code: 42 }).code).toBeUndefined();
    expect(parseProblem(response, undefined).status).toBe(500);
  });

  it('has a message for every code in the contract', () => {
    for (const message of Object.values(problemMessages)) {
      expect(message.length).toBeGreaterThan(5);
    }
  });

  it('uses the dictionary for known codes and the sanitised detail for unknown ones', () => {
    const [known, knownBody] = problemResponse(409, { code: 'name_taken', detail: 'raw server text' });
    expect(describeProblem(parseProblem(known, knownBody))).toBe(problemMessages.name_taken);

    const [unknown, unknownBody] = problemResponse(409, { code: 'brand_new_code', detail: 'Something specific.' });
    expect(describeProblem(parseProblem(unknown, unknownBody))).toBe('Something specific.');

    const [bare, bareBody] = problemResponse(409, { code: 'brand_new_code' });
    expect(describeProblem(parseProblem(bare, bareBody))).toBe('The request could not be completed.');
  });

  it('shows server text as plain text: control characters dropped, length capped', () => {
    expect(safeText('a\u0000b\u001bc\u0085d')).toBe('abcd');
    expect(safeText('<img src=x onerror=alert(1)>')).toBe('<img src=x onerror=alert(1)>');
    expect(safeText('x'.repeat(500)).length).toBeLessThan(210);
  });

  it('shows Retry-After for 429, as seconds or as an HTTP date', () => {
    const [response, body] = problemResponse(429, { code: 'rate_limited' }, { 'Retry-After': '17' });
    expect(describeProblem(parseProblem(response, body))).toBe('Too many requests. Try again in 17 seconds.');

    const [one, oneBody] = problemResponse(429, { code: 'rate_limited' }, { 'Retry-After': '1' });
    expect(describeProblem(parseProblem(one, oneBody))).toBe('Too many requests. Try again in 1 second.');

    const [none, noneBody] = problemResponse(429, { code: 'rate_limited' });
    expect(describeProblem(parseProblem(none, noneBody))).toBe('Too many requests. Try again later.');

    const now = Date.parse('2026-01-01T00:00:00Z');
    expect(parseRetryAfter('Thu, 01 Jan 2026 00:00:30 GMT', now)).toBe(30);
    expect(parseRetryAfter('Wed, 31 Dec 2025 23:00:00 GMT', now)).toBe(0);
    expect(parseRetryAfter('soon', now)).toBeUndefined();
    expect(parseRetryAfter('99999999', now)).toBe(86400);
  });

  it('describes a network failure and a 503', () => {
    expect(describeProblem(networkProblem())).toContain('Cannot reach the server');
    const [response, body] = problemResponse(503, { code: 'db_unavailable' });
    expect(describeProblem(parseProblem(response, body))).toBe(problemMessages.db_unavailable);
  });
});
