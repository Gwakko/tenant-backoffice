import type { QueryClient } from '@tanstack/react-query';

import type { Api } from '@/api/client';
import { keyPrefixes, keys } from '@/api/keys';
import { unwrap } from '@/api/result';

export type OperationKind = 'credit' | 'debit' | 'transfer';

export interface OperationInput {
  kind: OperationKind;
  nodeId: string;
  /** Recipient of a transfer. */
  toNodeId: string | null;
  /** Minor units as a string. */
  amount: string;
  comment: string;
}

export interface OperationOutcome {
  /** The server answered from a stored result (a repeat of an earlier request): still a success. */
  replayed: boolean;
}

// One request of a balance operation. The key belongs to the attempt (lib/idempotency) and travels
// in the Idempotency-Key header; the same key makes the server return the first result.
export async function sendOperation(api: Api, input: OperationInput, key: string): Promise<OperationOutcome> {
  const comment = input.comment === '' ? {} : { comment: input.comment };
  const header = { 'Idempotency-Key': key };
  if (input.kind === 'transfer') {
    if (input.toNodeId === null) throw new TypeError('A transfer needs a recipient.');
    const result = await unwrap(
      api.client.POST('/transfers', {
        params: { header },
        body: { fromNodeId: input.nodeId, toNodeId: input.toNodeId, amount: input.amount, ...comment },
      }),
    );
    return { replayed: result.response.headers.get('Idempotent-Replayed') === 'true' };
  }
  const path = input.kind === 'credit' ? '/nodes/{id}/credits' : '/nodes/{id}/debits';
  const result = await unwrap(
    api.client.POST(path, { params: { path: { id: input.nodeId }, header }, body: { amount: input.amount, ...comment } }),
  );
  return { replayed: result.response.headers.get('Idempotent-Replayed') === 'true' };
}

// After a balance change: the node, its balance (and the recipient's), the list of its parent, and
// every history, report and audit list (the change may be in the scope of any of them).
export async function refreshAfterOperation(
  queryClient: QueryClient,
  input: OperationInput,
  parentId: string | null,
): Promise<void> {
  const ids = input.toNodeId === null ? [input.nodeId] : [input.nodeId, input.toNodeId];
  const refreshes = [
    ...ids.flatMap((id) => [
      queryClient.invalidateQueries({ queryKey: keys.node(id) }),
      queryClient.invalidateQueries({ queryKey: keys.balance(id) }),
    ]),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.operations() }),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.audit() }),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.reportSummary() }),
    queryClient.invalidateQueries({ queryKey: keyPrefixes.reportChildren() }),
  ];
  if (parentId !== null) refreshes.push(queryClient.invalidateQueries({ queryKey: keys.children(parentId) }));
  await Promise.all(refreshes);
}
