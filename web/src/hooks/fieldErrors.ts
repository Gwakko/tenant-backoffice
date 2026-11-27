import type { FieldValues, Path, UseFormSetError } from 'react-hook-form';

import type { ApiProblem } from '@/api/problem';

// Field errors of a 400/422 answer go to the form (spec 06 section 6). Returns true when at least
// one error landed on a known field; the rest of the problem is shown above the form.
export function applyFieldErrors<T extends FieldValues>(
  problem: ApiProblem,
  fields: readonly Path<T>[],
  setError: UseFormSetError<T>,
): boolean {
  let applied = false;
  for (const item of problem.errors) {
    const field = fields.find((name) => name === item.field);
    if (field === undefined) continue;
    setError(field, { type: 'server', message: item.message });
    applied = true;
  }
  return applied;
}
