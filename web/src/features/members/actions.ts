import type { QueryClient } from '@tanstack/react-query';

import type { Api } from '@/api/client';
import { keyPrefixes, keys } from '@/api/keys';
import type { Member, Role } from '@/api/resources';
import { unwrap, unwrapEmpty } from '@/api/result';
import type { components } from '@/api/schema';

export type MemberPage = components['schemas']['MemberPage'];

export async function fetchMembersPage(api: Api, id: string, inherited: boolean, cursor?: string): Promise<MemberPage> {
  const query: { inherited: boolean; cursor?: string } = { inherited };
  if (cursor !== undefined) query.cursor = cursor;
  return (await unwrap(api.client.GET('/nodes/{id}/members', { params: { path: { id }, query } }))).data;
}

export async function addMember(
  api: Api,
  id: string,
  body: { email: string; role: Role; initialPassword?: string },
): Promise<Member> {
  return (await unwrap(api.client.POST('/nodes/{id}/members', { params: { path: { id } }, body }))).data;
}

export async function changeRole(api: Api, id: string, userId: string, role: Role): Promise<Member> {
  return (await unwrap(api.client.PUT('/nodes/{id}/members/{userId}', { params: { path: { id, userId } }, body: { role } }))).data;
}

export async function removeMember(api: Api, id: string, userId: string): Promise<void> {
  await unwrapEmpty(api.client.DELETE('/nodes/{id}/members/{userId}', { params: { path: { id, userId } } }));
}

export async function refreshMembers(queryClient: QueryClient, nodeId: string): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: keys.members(nodeId) }),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.audit() }),
  ]);
}
