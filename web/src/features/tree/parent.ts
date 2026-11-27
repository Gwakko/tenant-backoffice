import type { Node } from './queries';

// The parent of a node as the actor may see it. `parentId` is NOT used: it is null for an access
// root and may name nothing the actor can read. Breadcrumbs start at the access root and end with
// the node itself, so the parent is the entry before the last one (none for an access root).
export function visibleParentId(node: Pick<Node, 'breadcrumbs'>): string | null {
  const crumbs = node.breadcrumbs;
  return crumbs[crumbs.length - 2]?.id ?? null;
}

// Ancestors the actor can see, from the access root down to the parent.
export function visibleAncestorIds(node: Pick<Node, 'breadcrumbs'>): string[] {
  return node.breadcrumbs.slice(0, -1).map((crumb) => crumb.id);
}
