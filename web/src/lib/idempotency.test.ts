import { describe, expect, it } from 'vitest';

import { createAttemptKey } from './idempotency';

function counter() {
  let n = 0;
  return () => `key-${String(++n)}`;
}

describe('createAttemptKey', () => {
  it('[T-W-04] the same attempt keeps its key (double click, resend after a failure)', () => {
    const attempt = createAttemptKey(counter());
    const first = attempt.keyFor('debit:100');
    expect(attempt.keyFor('debit:100')).toBe(first);
    expect(attempt.keyFor('debit:100')).toBe(first);
  });

  it('[T-W-04] a changed request is another attempt with another key', () => {
    const attempt = createAttemptKey(counter());
    expect(attempt.keyFor('debit:100')).not.toBe(attempt.keyFor('debit:200'));
  });

  it('[T-W-04] after a success the next request gets a new key, even for the same request', () => {
    const attempt = createAttemptKey(counter());
    const first = attempt.keyFor('debit:100');
    attempt.succeeded();
    expect(attempt.keyFor('debit:100')).not.toBe(first);
  });

  it('[T-W-04] the default key is a UUID that the API accepts', () => {
    const key = createAttemptKey().keyFor('x');
    expect(key).toMatch(/^[A-Za-z0-9_\-:.]{1,128}$/);
  });
});
