import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useEffect } from 'react';

import { keyPrefixes, keys } from '@/api/keys';
import { fetchMe } from '@/api/resources';
import { EmptyState } from '@/components/ui/EmptyState';
import { useApi } from '@/hooks/useApi';

// 404 on a node route (EC-118): the node does not exist or the access was withdrawn. The screen
// gives no hint which of the two it is, offers the first access root, and reads `me` and the
// tree again so that the sidebar stops showing what the user no longer has.
export function NodeNotFound() {
  const api = useApi();
  const queryClient = useQueryClient();
  const me = useQuery({
    queryKey: keys.me(),
    queryFn: () => fetchMe(api),
    staleTime: 0,
    refetchOnMount: 'always',
  });

  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: keyPrefixes.children() });
  }, [queryClient]);

  const root = me.data?.accessRoots[0];
  return (
    <section className="space-y-4" aria-labelledby="not-found-title">
      <h1 id="not-found-title" className="text-xl font-semibold">
        Not found or no access
      </h1>
      <EmptyState>This node does not exist, or you no longer have access to it.</EmptyState>
      {root ? (
        <Link to="/nodes/$nodeId" params={{ nodeId: root.nodeId }} className="text-sm underline">
          Go to {root.name}
        </Link>
      ) : (
        <Link to="/" className="text-sm underline">
          Go to the start page
        </Link>
      )}
    </section>
  );
}
