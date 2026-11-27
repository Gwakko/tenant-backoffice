const NODE_PATH = /^\/nodes\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\/|$)/i;

// The node a path points at (`/nodes/<id>` and its tabs), or undefined. Read from the path, so the
// sidebar does not depend on how the route files of the node card are named.
export function nodeIdFromPath(pathname: string): string | undefined {
  return NODE_PATH.exec(pathname)?.[1];
}
