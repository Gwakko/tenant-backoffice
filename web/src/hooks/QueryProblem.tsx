import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';

import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';

interface QueryProblemProps {
  error: unknown;
  /** The node whose permissions are read again when the server answers 403 (EC-117). */
  permsNodeId: string;
  /** Shown for a network failure and 5xx as a "Retry" button. */
  onRetry?: () => void;
}

// A failed read as an Alert (spec 06 section 6): 403 refreshes the permissions only, 429 names the
// Retry-After wait (describeProblem), network and 5xx offer "Retry". The text comes from the
// dictionary or is sanitised; nothing is rendered as HTML.
export function QueryProblem({ error, permsNodeId, onRetry }: QueryProblemProps) {
  const queryClient = useQueryClient();
  const problem = problemOf(error);
  const status = problem.status;

  useEffect(() => {
    if (status === 403) void queryClient.invalidateQueries({ queryKey: keys.perms(permsNodeId) });
  }, [status, permsNodeId, queryClient]);

  const retryable = status === 0 || status >= 500 || status === 429;
  return (
    <Alert>
      <p>{describeProblem(problem)}</p>
      {retryable && onRetry && (
        <Button variant="secondary" className="mt-2" onClick={onRetry}>
          Retry
        </Button>
      )}
    </Alert>
  );
}
