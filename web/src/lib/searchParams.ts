import { z } from 'zod';

import { isRealDate } from './dates';

// Building blocks for the Zod schemas of route search parameters (EC-123). A value that does not
// pass becomes `undefined`, which every screen reads as "the default": garbage in the URL never
// reaches a request and never breaks a screen.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const uuidParam = z
  .string()
  .regex(UUID)
  .transform((value) => value.toLowerCase())
  .optional()
  .catch(undefined);

export const dateParam = z.string().refine(isRealDate).optional().catch(undefined);

export function enumParam<const T extends readonly [string, ...string[]]>(values: T) {
  return z.enum(values).optional().catch(undefined);
}

/** Runs a schema on raw search params; anything that does not parse gives the empty object (all defaults). */
export function parseSearch<S extends z.ZodType>(schema: S, raw: unknown): z.output<S> {
  const result = schema.safeParse(raw);
  return result.success ? result.data : schema.parse({});
}
