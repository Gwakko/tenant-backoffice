import type { QueryClient } from '@tanstack/react-query';

import type { Api } from '@/api/client';
import { keyPrefixes, keys } from '@/api/keys';
import type { Node, NodeType, SettingItem } from '@/api/resources';
import type { components } from '@/api/schema';
import { unwrap, unwrapEmpty } from '@/api/result';

export async function createChild(api: Api, parentId: string, body: { type: NodeType; name: string }): Promise<Node> {
  return (await unwrap(api.client.POST('/nodes/{id}/children', { params: { path: { id: parentId } }, body }))).data;
}

export async function renameNode(api: Api, id: string, name: string): Promise<Node> {
  return (await unwrap(api.client.PATCH('/nodes/{id}', { params: { path: { id } }, body: { name } }))).data;
}

export async function setNodeActive(api: Api, id: string, active: boolean): Promise<Node> {
  const path = active ? '/nodes/{id}/activate' : '/nodes/{id}/deactivate';
  return (await unwrap(api.client.POST(path, { params: { path: { id } } }))).data;
}

export async function deleteNode(api: Api, id: string): Promise<void> {
  await unwrapEmpty(api.client.DELETE('/nodes/{id}', { params: { path: { id } } }));
}

// The tree and the card read the same keys, so one invalidation refreshes both. The list of the
// parent is refreshed by its id (taken from the breadcrumbs by the caller, never from parentId).
export async function refreshAfterNodeChange(
  queryClient: QueryClient,
  nodeId: string,
  parentId: string | null,
): Promise<void> {
  const refreshes = [
    queryClient.invalidateQueries({ queryKey: keys.node(nodeId) }),
    queryClient.invalidateQueries({ queryKey: keys.children(nodeId) }),
    queryClient.invalidateQueries({ queryKey: keys.me() }),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.audit() }),
  ];
  if (parentId !== null) refreshes.push(queryClient.invalidateQueries({ queryKey: keys.children(parentId) }));
  else refreshes.push(queryClient.invalidateQueries({ queryKey: keyPrefixes.children() }));
  await Promise.all(refreshes);
}

export type SettingKey = components['schemas']['SettingKey'];
export type SettingValue = components['schemas']['SettingValue'];

export async function fetchSettings(api: Api, id: string): Promise<SettingItem[]> {
  return (await unwrap(api.client.GET('/nodes/{id}/settings', { params: { path: { id } } }))).data.items;
}

export async function putSetting(api: Api, id: string, key: SettingKey, value: SettingValue): Promise<SettingItem> {
  return (await unwrap(api.client.PUT('/nodes/{id}/settings/{key}', { params: { path: { id, key } }, body: { value } }))).data;
}

export async function resetSetting(api: Api, id: string, key: SettingKey): Promise<SettingItem> {
  return (await unwrap(api.client.DELETE('/nodes/{id}/settings/{key}', { params: { path: { id, key } } }))).data;
}
