import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useRouter } from '@tanstack/react-router';

import type { Api } from '@/api/client';
import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { fetchMe, fetchNode, type Me, type Node, type NodeSummary } from '@/api/resources';
import { problemOf, unwrap } from '@/api/result';
import type { components } from '@/api/schema';

export type { Me, Node, NodeSummary };
export type NodeSummaryPage = components['schemas']['NodeSummaryPage'];
export type StatusFilter = 'all' | 'active' | 'inactive';

export const PAGE_SIZE = 50;

// User-facing text of a failed request, always from the problem dictionary: server details and
// identifiers never reach the screen through it.
export function messageOf(error: unknown): string {
  return describeProblem(problemOf(error));
}

// The API client travels in the router context; features do not import routes or the app wiring.
export function useApi(): Api {
  return useRouter().options.context.api;
}

export function useMe() {
  const api = useApi();
  return useQuery({
    queryKey: keys.me(),
    queryFn: () => fetchMe(api),
  });
}

export function useNodeDetail(nodeId: string | undefined) {
  const api = useApi();
  return useQuery({
    queryKey: keys.node(nodeId ?? ''),
    queryFn: () => fetchNode(api, nodeId ?? ''),
    enabled: nodeId !== undefined,
  });
}

export function useChildren(nodeId: string, status: StatusFilter) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: keys.children(nodeId, { status }),
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) =>
      (await unwrap(
        api.client.GET('/nodes/{id}/children', {
          params: {
            path: { id: nodeId },
            query: { limit: PAGE_SIZE, status, ...(pageParam === undefined ? {} : { cursor: pageParam }) },
          },
        }),
      )).data,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}

// Candidate parents come only from this endpoint (EC-122): the client never filters a list of
// all nodes by itself.
export function useMoveTargets(nodeId: string, q: string, enabled: boolean) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: keys.moveTargets(nodeId, { q }),
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) =>
      (await unwrap(
        api.client.GET('/nodes/{id}/move-targets', {
          params: {
            path: { id: nodeId },
            query: {
              limit: PAGE_SIZE,
              ...(q === '' ? {} : { q }),
              ...(pageParam === undefined ? {} : { cursor: pageParam }),
            },
          },
        }),
      )).data,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled,
  });
}
