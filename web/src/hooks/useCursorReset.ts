import { useQueryClient, type QueryKey } from '@tanstack/react-query';
import { useEffect } from 'react';

import { problemOf } from '@/api/result';

interface CursorQuery {
  isFetchNextPageError: boolean;
  error: unknown;
}

// A cursor the server no longer accepts (`invalid_cursor`) must not leave a dead list: the query
// is reset, so the list is loaded again from the first page. Only a failed "next page" counts:
// the first page carries no cursor, so a loop is not possible. `queryKey` must be stable (memoised).
export function useCursorReset(query: CursorQuery, queryKey: QueryKey): void {
  const queryClient = useQueryClient();
  const stale = query.isFetchNextPageError && problemOf(query.error).code === 'invalid_cursor';
  useEffect(() => {
    if (stale) void queryClient.resetQueries({ queryKey, exact: true });
  }, [stale, queryKey, queryClient]);
}
