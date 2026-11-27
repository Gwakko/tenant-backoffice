// Query keys of the whole application, in one place (spec 06 section 3).
//
// Features may not import each other (layer rules, T-ARCH-04), but a mutation in one
// feature invalidates the data of another: a balance operation refreshes the node, a move
// refreshes the tree. They agree through these keys, which live in the shared api layer.

/** Values of a list filter: only flat scalars, so a key stays serializable and stable. */
export type FilterValue = string | number | boolean | null | undefined;
export type Filters = Readonly<Record<string, FilterValue>>;

export const keys = {
  me: () => ['me'] as const,
  children: (nodeId: string, filters?: Filters) =>
    filters === undefined ? (['children', nodeId] as const) : (['children', nodeId, filters] as const),
  node: (nodeId: string) => ['node', nodeId] as const,
  moveTargets: (nodeId: string, filters?: Filters) =>
    filters === undefined ? (['move-targets', nodeId] as const) : (['move-targets', nodeId, filters] as const),
  balance: (nodeId: string) => ['balance', nodeId] as const,
  perms: (nodeId: string) => ['perms', nodeId] as const,
  settings: (nodeId: string) => ['settings', nodeId] as const,
  members: (nodeId: string, inherited?: boolean) =>
    inherited === undefined ? (['members', nodeId] as const) : (['members', nodeId, inherited] as const),
  operations: (scopeNodeId: string, filters?: Filters) =>
    filters === undefined ? (['operations', scopeNodeId] as const) : (['operations', scopeNodeId, filters] as const),
  reportSummary: (nodeId: string, filters?: Filters) =>
    filters === undefined ? (['report-summary', nodeId] as const) : (['report-summary', nodeId, filters] as const),
  reportChildren: (nodeId: string, filters?: Filters) =>
    filters === undefined ? (['report-children', nodeId] as const) : (['report-children', nodeId, filters] as const),
  audit: (scopeNodeId: string, filters?: Filters) =>
    filters === undefined ? (['audit', scopeNodeId] as const) : (['audit', scopeNodeId, filters] as const),
} as const;

// Prefixes for invalidation across scopes: a balance operation does not know which scope node
// of the history or the audit shows it, so it refreshes every list under the name.
export const keyPrefixes = {
  children: () => ['children'] as const,
  operations: () => ['operations'] as const,
  audit: () => ['audit'] as const,
  reportSummary: () => ['report-summary'] as const,
  reportChildren: () => ['report-children'] as const,
} as const;
