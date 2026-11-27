import { redirect } from '@tanstack/react-router';

import type { Api } from '@/api/client';

import { loginSearch } from './redirect';
import { ensureSession } from './service';

// beforeLoad guard: without a session (and no cookie to restore it from) go to /login and come
// back afterwards. `href` is the current path with search and hash.
export async function requireSession(api: Api, href: string): Promise<void> {
  if (await ensureSession(api)) return;
  throw redirect({ to: '/login', search: loginSearch(href) });
}
