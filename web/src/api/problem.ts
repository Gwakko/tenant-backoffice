import { z } from 'zod';

import type { components } from './schema';

export type ErrorCode = components['schemas']['ErrorCode'];

// Client-side view of an application/problem+json response (or of a transport failure).
// status 0 means that no HTTP response arrived (network failure).
export interface ApiProblem {
  status: number;
  code: string | undefined;
  title: string | undefined;
  detail: string | undefined;
  errors: { field: string; message: string }[];
  requestId: string | undefined;
  retryAfterSeconds: number | undefined;
}

// One user-facing text per server error code. The record is exhaustive on purpose: adding a code
// to the contract breaks the build until a message is written for it.
export const problemMessages: Record<ErrorCode, string> = {
  validation_failed: 'Some fields are invalid. Check them and try again.',
  invalid_id: 'The identifier is not valid.',
  invalid_amount: 'The amount is not valid.',
  amount_must_be_positive: 'The amount must be greater than zero.',
  invalid_cursor: 'The list position is no longer valid. Reload the list.',
  invalid_period: 'The selected period is not valid.',
  same_node: 'Choose a different node.',
  idempotency_key_required: 'The request could not be sent. Reload the page and try again.',
  idempotency_key_invalid: 'The request could not be sent. Reload the page and try again.',
  invalid_credentials: 'Invalid email or password.',
  token_missing: 'Your session has ended. Sign in again.',
  token_expired: 'Your session has expired. Sign in again.',
  token_invalid: 'Your session is no longer valid. Sign in again.',
  refresh_invalid: 'Your session has ended. Sign in again.',
  forbidden: 'You do not have permission for this action.',
  not_found: 'Not found or no access.',
  name_taken: 'This name is already used here.',
  node_inactive: 'The node is inactive.',
  operations_disabled: 'Balance operations are disabled for this node.',
  insufficient_funds: 'The balance is not enough for this operation.',
  balance_overflow: 'The balance would exceed the allowed maximum.',
  node_has_children: 'The node still has children.',
  node_has_balance: 'The node still has a balance.',
  node_has_history: 'The node has operation history.',
  children_limit: 'The parent already has the maximum number of children.',
  last_root_owner: 'The last owner of an access root cannot be removed or demoted.',
  user_inactive: 'The user is inactive.',
  invalid_parent_type: 'This node type is not allowed under that parent.',
  move_cycle: 'A node cannot be moved into itself or its descendants.',
  root_immutable: 'A root node cannot be moved or deleted.',
  role_not_grantable: 'You do not have permission for this action.',
  unknown_setting: 'Unknown setting.',
  invalid_setting_value: 'The setting value is not valid.',
  amount_exceeds_limit: 'The amount exceeds the allowed limit.',
  idempotency_key_reused: 'This request was already used with different data. Reload and try again.',
  payload_too_large: 'The request is too large.',
  unsupported_media_type: 'The request format is not supported.',
  rate_limited: 'Too many requests. Try again later.',
  db_unavailable: 'The service is temporarily unavailable. Try again in a moment.',
  internal: 'Something went wrong on the server. Try again.',
  invalid_password: 'The password does not meet the requirements.',
  invalid_node_type: 'This node type is not valid here.',
  invalid_name: 'The name is not valid.',
  invalid_email: 'The email is not valid.',
  invalid_role: 'The role is not valid.',
};

const MAX_DETAIL_LENGTH = 200;
const MAX_RETRY_AFTER_SECONDS = 24 * 60 * 60;

const bodySchema = z.object({
  code: z.string().optional(),
  title: z.string().optional(),
  detail: z.string().optional(),
  errors: z.array(z.object({ field: z.string(), message: z.string() })).optional(),
  requestId: z.string().optional(),
});

// Server text is shown as plain text only (never as HTML); control characters are dropped and the
// length is capped so that a hostile or broken body cannot flood the screen.
export function safeText(value: string, max = MAX_DETAIL_LENGTH): string {
  const cleaned = Array.from(value)
    .filter((ch) => {
      const code = ch.codePointAt(0) ?? 0;
      return code >= 0x20 && code !== 0x7f && !(code >= 0x80 && code <= 0x9f);
    })
    .join('')
    .trim();
  return cleaned.length > max ? `${cleaned.slice(0, max)}...` : cleaned;
}

// Retry-After is either a number of seconds or an HTTP date.
export function parseRetryAfter(header: string | null, now: number = Date.now()): number | undefined {
  if (header === null) return undefined;
  const value = header.trim();
  if (/^\d{1,9}$/.test(value)) return Math.min(Number(value), MAX_RETRY_AFTER_SECONDS);
  const at = Date.parse(value);
  if (Number.isNaN(at)) return undefined;
  return Math.min(Math.max(Math.ceil((at - now) / 1000), 0), MAX_RETRY_AFTER_SECONDS);
}

function isJsonContent(response: Response): boolean {
  const type = response.headers.get('Content-Type') ?? '';
  return type.startsWith('application/problem+json') || type.startsWith('application/json');
}

// `body` is the parsed error body that openapi-fetch hands back next to the response.
export function parseProblem(response: Response, body: unknown): ApiProblem {
  const parsed = isJsonContent(response) ? bodySchema.safeParse(body) : undefined;
  const data = parsed?.success ? parsed.data : undefined;
  return {
    status: response.status,
    code: data?.code,
    title: data?.title,
    detail: data?.detail,
    errors: data?.errors ?? [],
    requestId: data?.requestId,
    retryAfterSeconds: parseRetryAfter(response.headers.get('Retry-After')),
  };
}

export function networkProblem(): ApiProblem {
  return {
    status: 0,
    code: undefined,
    title: undefined,
    detail: undefined,
    errors: [],
    requestId: undefined,
    retryAfterSeconds: undefined,
  };
}

function isKnownCode(code: string | undefined): code is ErrorCode {
  return code !== undefined && Object.hasOwn(problemMessages, code);
}

// The text for a problem: dictionary by code; unknown code falls back to the (sanitised) detail,
// then to a generic text by status.
export function describeProblem(problem: ApiProblem): string {
  if (problem.status === 0) return 'Cannot reach the server. Check your connection and try again.';
  if (problem.status === 429 || problem.code === 'rate_limited') {
    const wait = problem.retryAfterSeconds;
    return wait === undefined
      ? 'Too many requests. Try again later.'
      : `Too many requests. Try again in ${String(wait)} ${wait === 1 ? 'second' : 'seconds'}.`;
  }
  if (isKnownCode(problem.code)) return problemMessages[problem.code];
  if (problem.status === 503) return problemMessages.db_unavailable;
  if (problem.detail !== undefined) {
    const detail = safeText(problem.detail);
    if (detail !== '') return detail;
  }
  if (problem.status >= 500) return problemMessages.internal;
  return 'The request could not be completed.';
}
