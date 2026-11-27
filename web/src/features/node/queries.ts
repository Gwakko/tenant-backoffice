import { useQuery } from '@tanstack/react-query';

import { keys } from '@/api/keys';
import { fetchNode, fetchPerms } from '@/api/resources';
import { useApi } from '@/hooks/useApi';

export function useNode(nodeId: string) {
  const api = useApi();
  return useQuery({ queryKey: keys.node(nodeId), queryFn: () => fetchNode(api, nodeId) });
}

export function usePerms(nodeId: string | null) {
  const api = useApi();
  return useQuery({
    queryKey: keys.perms(nodeId ?? ''),
    queryFn: () => fetchPerms(api, nodeId ?? ''),
    enabled: nodeId !== null,
  });
}
