// A random v4 UUID that also works in an insecure context.
//
// `crypto.randomUUID()` exists only in a secure context (HTTPS or localhost): on a plain HTTP origin
// such as a compose service name it is undefined and the client would fail on its first request.
// `crypto.getRandomValues()` is available everywhere, so it is the fallback. Both give 122 random bits.

export function randomUuid(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();

  const bytes = crypto.getRandomValues(new Uint8Array(16));
  // RFC 4122: version 4 in the high nibble of byte 6, variant 10 in the top bits of byte 8.
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0'));
  return [hex.slice(0, 4), hex.slice(4, 6), hex.slice(6, 8), hex.slice(8, 10), hex.slice(10, 16)]
    .map((part) => part.join(''))
    .join('-');
}
