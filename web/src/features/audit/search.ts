import { z } from 'zod';

import type { components } from '@/api/schema';
import { dateParam, enumParam, parseSearch, uuidParam } from '@/lib/searchParams';

export type AuditAction = components['schemas']['AuditAction'];

// Readable names of the audit actions. The record is exhaustive on purpose: a new action in the
// contract breaks the build until it gets a name. The keys are also the allowed values of the URL.
export const auditActionLabels: Record<AuditAction, string> = {
  'node.created': 'Node created',
  'node.renamed': 'Node renamed',
  'node.moved': 'Node moved',
  'node.child_moved_out': 'Child moved out',
  'node.child_moved_in': 'Child moved in',
  'node.deactivated': 'Node deactivated',
  'node.activated': 'Node activated',
  'node.deleted': 'Node deleted',
  'member.granted': 'Member granted',
  'member.changed': 'Member role changed',
  'member.revoked': 'Member revoked',
  'user.created': 'User created',
  'settings.set': 'Setting set',
  'settings.unset': 'Setting reset',
  'balance.credited': 'Balance credited',
  'balance.debited': 'Balance debited',
  'balance.transferred': 'Balance transferred',
  'auth.password_changed': 'Password changed',
};

export const AUDIT_ACTIONS = Object.keys(auditActionLabels) as [AuditAction, ...AuditAction[]];

export const auditSearchSchema = z.object({
  node: uuidParam,
  action: enumParam(AUDIT_ACTIONS),
  from: dateParam,
  to: dateParam,
});

export type AuditSearch = z.infer<typeof auditSearchSchema>;

export function parseAuditSearch(raw: unknown): AuditSearch {
  return parseSearch(auditSearchSchema, raw);
}
