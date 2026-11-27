// Idempotency-Key of one attempt to change a balance (spec 06 section 5, EC-120).
//
// One key belongs to one attempt: it is created when the form or dialog opens, is reused when the
// same attempt is sent again (double click, network failure) and is dropped after a success.
// If the user changes the request between sends, it is a different attempt and gets a new key,
// because the server refuses a key that is reused with another body (422 idempotency_key_reused).

export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}

export interface AttemptKey {
  /** The key for a request with this fingerprint: the same key while the fingerprint is unchanged. */
  keyFor(fingerprint: string): string;
  /** The attempt succeeded: the next request starts with a new key. */
  succeeded(): void;
}

export function createAttemptKey(generate: () => string = newIdempotencyKey): AttemptKey {
  let key: string | null = null;
  let lastFingerprint: string | null = null;
  return {
    keyFor(fingerprint) {
      if (key === null || lastFingerprint !== fingerprint) {
        key = generate();
        lastFingerprint = fingerprint;
      }
      return key;
    },
    succeeded() {
      key = null;
      lastFingerprint = null;
    },
  };
}
