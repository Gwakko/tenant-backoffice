import type { Role } from '@/api/resources';

// Ranks from the domain (spec 01 section 3.3): a user may grant and revoke roles up to their own.
export const ROLES: readonly Role[] = ['viewer', 'analyst', 'admin', 'owner'];

export function roleRank(role: Role): number {
  return ROLES.indexOf(role);
}

/** The roles the actor may offer in a select: not above the actor's own role. */
export function grantableRoles(own: Role | undefined): Role[] {
  if (own === undefined) return [];
  return ROLES.filter((role) => roleRank(role) <= roleRank(own));
}

/** The actor may change or remove an assignment only when its role is not above their own. */
export function canManage(own: Role | undefined, target: Role): boolean {
  return own !== undefined && roleRank(target) <= roleRank(own);
}
