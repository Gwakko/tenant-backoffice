import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useRef, useState } from 'react';

import { keys } from '@/api/keys';
import type { ApiProblem } from '@/api/problem';
import { problemOf } from '@/api/result';

export type ActionResult<T> = { ok: true; value: T } | { ok: false; problem: ApiProblem };

interface Options<A, T> {
  /** The node whose permissions are read again when the server answers 403 (EC-117). */
  permsNodeId: string;
  run: (args: A) => Promise<T>;
  /** After a success: invalidate what the change touches. No optimistic updates. */
  onSuccess?: (value: T, args: A) => Promise<void> | void;
}

// One user action that changes data: runs at most once at a time (a second click while the first
// is in flight is ignored), reports its problem, and on 403 only refreshes the permissions: the
// cache of the data stays as it was.
export function useAction<A, T>({ permsNodeId, run, onSuccess }: Options<A, T>) {
  const queryClient = useQueryClient();
  const inFlight = useRef(false);
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState<ApiProblem | null>(null);

  const execute = useCallback(
    async (args: A): Promise<ActionResult<T> | undefined> => {
      if (inFlight.current) return undefined;
      inFlight.current = true;
      setPending(true);
      setProblem(null);
      try {
        const value = await run(args);
        await onSuccess?.(value, args);
        return { ok: true, value };
      } catch (error) {
        const found = problemOf(error);
        if (found.status === 403) {
          void queryClient.invalidateQueries({ queryKey: keys.perms(permsNodeId) });
        }
        setProblem(found);
        return { ok: false, problem: found };
      } finally {
        inFlight.current = false;
        setPending(false);
      }
    },
    [permsNodeId, queryClient, run, onSuccess],
  );

  const clear = useCallback(() => {
    setProblem(null);
  }, []);

  return { execute, pending, problem, clear };
}
