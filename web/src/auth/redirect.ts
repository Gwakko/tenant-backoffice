// The `redirect` search parameter is attacker-controlled. Only an application-relative path is
// accepted; anything else becomes "/" (no open redirect).
const MAX_REDIRECT_LENGTH = 512;
const MAX_DECODE_ROUNDS = 3;
const PLACEHOLDER_ORIGIN = 'http://app.invalid';

function hasForbiddenCharacter(value: string): boolean {
  for (const ch of value) {
    const code = ch.codePointAt(0) ?? 0;
    // Control characters and spaces (browsers strip tabs and newlines inside URLs), backslash.
    if (code <= 0x20 || code === 0x7f || ch === '\\') return true;
  }
  return false;
}

function isRelativePath(value: string): boolean {
  if (!value.startsWith('/') || value.startsWith('//')) return false;
  return !hasForbiddenCharacter(value);
}

export function safeRedirect(value: unknown): string {
  if (typeof value !== 'string' || value === '' || value.length > MAX_REDIRECT_LENGTH) return '/';

  // Check the raw value and every decoded form: "%2F%2Fevil.test" must not survive a decode.
  let candidate = value;
  for (let round = 0; round <= MAX_DECODE_ROUNDS; round++) {
    if (!isRelativePath(candidate)) return '/';
    let decoded: string;
    try {
      decoded = decodeURIComponent(candidate);
    } catch {
      return '/';
    }
    if (decoded === candidate) break;
    if (round === MAX_DECODE_ROUNDS) return '/';
    candidate = decoded;
  }

  let url: URL;
  try {
    url = new URL(value, PLACEHOLDER_ORIGIN);
  } catch {
    return '/';
  }
  if (url.origin !== PLACEHOLDER_ORIGIN) return '/';
  const target = `${url.pathname}${url.search}${url.hash}`;
  // Never bounce back to the login page itself.
  if (url.pathname === '/login' || url.pathname.startsWith('/login/')) return '/';
  return target;
}

// Search params for a redirect to /login that remembers where the user was heading.
export function loginSearch(href: string): { redirect?: string } {
  const target = safeRedirect(href);
  return target === '/' ? {} : { redirect: target };
}
