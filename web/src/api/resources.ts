import type { Api } from './client';
import type { components } from './schema';
import { unwrap } from './result';

// Reads shared by several screens. Query keys come from keys.ts; React stays out of this layer.

export type Node = components['schemas']['Node'];
export type NodeSummary = components['schemas']['NodeSummary'];
export type NodeType = components['schemas']['NodeType'];
export type Role = components['schemas']['Role'];
export type Permission = components['schemas']['Permission'];
export type NodePermissions = components['schemas']['NodePermissions'];
export type Balance = components['schemas']['Balance'];
export type Me = components['schemas']['Me'];
export type SettingItem = components['schemas']['SettingItem'];
export type Member = components['schemas']['Member'];
export type Operation = components['schemas']['Operation'];

export async function fetchMe(api: Api): Promise<Me> {
  return (await unwrap(api.client.GET('/me'))).data;
}

export async function fetchNode(api: Api, id: string): Promise<Node> {
  return (await unwrap(api.client.GET('/nodes/{id}', { params: { path: { id } } }))).data;
}

export async function fetchPerms(api: Api, nodeId: string): Promise<NodePermissions> {
  return (await unwrap(api.client.GET('/me/permissions', { params: { query: { nodeId } } }))).data;
}

export async function fetchBalance(api: Api, id: string): Promise<Balance> {
  return (await unwrap(api.client.GET('/nodes/{id}/balance', { params: { path: { id } } }))).data;
}

export async function fetchChildrenPage(
  api: Api,
  id: string,
  query: { q?: string; cursor?: string; limit?: number },
): Promise<components['schemas']['NodeSummaryPage']> {
  const clean: { q?: string; cursor?: string; limit?: number } = {};
  if (query.q !== undefined && query.q !== '') clean.q = query.q;
  if (query.cursor !== undefined) clean.cursor = query.cursor;
  if (query.limit !== undefined) clean.limit = query.limit;
  return (await unwrap(api.client.GET('/nodes/{id}/children', { params: { path: { id }, query: clean } }))).data;
}

/** The parent of a node as far as the actor may see it: from the breadcrumbs, never from parentId. */
export function parentIdOf(node: Node): string | null {
  const crumbs = node.breadcrumbs;
  const last = crumbs.at(-1);
  if (last === undefined) return null;
  if (last.id === node.id) return crumbs.at(-2)?.id ?? null;
  return last.id;
}

export function can(perms: NodePermissions | undefined, permission: Permission): boolean {
  return perms?.permissions.includes(permission) ?? false;
}
