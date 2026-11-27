import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { can, fetchBalance, fetchNode, fetchPerms, parentIdOf } from '@/api/resources';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { useApi } from '@/hooks/useApi';
import { formatMinor } from '@/lib/money';

import type { OperationKind } from './actions';
import { OperationDialog } from './OperationDialog';

export function BalancePanel({ nodeId }: { nodeId: string }) {
  const api = useApi();
  const balance = useQuery({ queryKey: keys.balance(nodeId), queryFn: () => fetchBalance(api, nodeId) });
  const perms = useQuery({ queryKey: keys.perms(nodeId), queryFn: () => fetchPerms(api, nodeId) });
  const node = useQuery({ queryKey: keys.node(nodeId), queryFn: () => fetchNode(api, nodeId) });
  const [kind, setKind] = useState<OperationKind | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const open = (next: OperationKind) => () => {
    setMessage(null);
    setKind(next);
  };

  return (
    <section aria-labelledby="balance-title" className="space-y-3 rounded border border-slate-200 bg-white p-4">
      <h2 id="balance-title" className="text-sm font-medium text-slate-600">
        Balance
      </h2>
      {balance.isPending && <p className="text-sm text-slate-600">Loading...</p>}
      {balance.isError && <Alert>{describeProblem(problemOf(balance.error))}</Alert>}
      {balance.isSuccess && (
        <p className="text-2xl font-semibold" data-testid="balance-amount">
          {formatMinor(balance.data.amount)} <span className="text-base font-normal text-slate-600">{balance.data.currency}</span>
        </p>
      )}
      {message && <Alert tone="success">{message}</Alert>}
      <div className="flex flex-wrap gap-2">
        {can(perms.data, 'balance.credit') && (
          <Button variant="secondary" onClick={open('credit')}>
            Credit
          </Button>
        )}
        {can(perms.data, 'balance.debit') && (
          <Button variant="secondary" onClick={open('debit')}>
            Debit
          </Button>
        )}
        {can(perms.data, 'balance.transfer') && (
          <Button variant="secondary" onClick={open('transfer')}>
            Transfer
          </Button>
        )}
      </div>
      <OperationDialog
        nodeId={nodeId}
        parentId={node.data ? parentIdOf(node.data) : null}
        kind={kind}
        currency={balance.data?.currency ?? ''}
        onClose={() => {
          setKind(null);
        }}
        onDone={setMessage}
      />
    </section>
  );
}
