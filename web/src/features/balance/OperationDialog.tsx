import { zodResolver } from '@hookform/resolvers/zod';
import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { describeProblem } from '@/api/problem';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { TextField } from '@/components/ui/TextField';
import { applyFieldErrors } from '@/hooks/fieldErrors';
import { useApi } from '@/hooks/useApi';
import { useAction } from '@/hooks/useAction';
import { createAttemptKey } from '@/lib/idempotency';
import { moneyErrorMessages, parseMoneyInput } from '@/lib/money';

import { refreshAfterOperation, sendOperation, type OperationInput, type OperationKind } from './actions';
import { RecipientPicker, type Recipient } from './RecipientPicker';

const TITLES: Record<OperationKind, string> = {
  credit: 'Credit the balance',
  debit: 'Debit the balance',
  transfer: 'Transfer to another node',
};

const SUBMIT: Record<OperationKind, string> = {
  credit: 'Confirm credit',
  debit: 'Confirm debit',
  transfer: 'Confirm transfer',
};

const schema = z.object({
  amount: z.string().superRefine((value, ctx) => {
    const parsed = parseMoneyInput(value);
    if (!parsed.ok) ctx.addIssue({ code: 'custom', message: moneyErrorMessages[parsed.error] });
    else if (parsed.value === '0') ctx.addIssue({ code: 'custom', message: 'The amount must be greater than zero.' });
  }),
  comment: z.string().max(200, 'The comment is at most 200 characters.'),
});
type Form = z.infer<typeof schema>;

interface Props {
  nodeId: string;
  parentId: string | null;
  kind: OperationKind | null;
  currency: string;
  onClose: () => void;
  onDone: (message: string) => void;
}

// The form (and its idempotency key) exists only while the dialog is open: every opening starts
// a new attempt.
export function OperationDialog({ kind, onClose, ...rest }: Props) {
  return (
    <Dialog open={kind !== null} title={kind ? TITLES[kind] : ''} onClose={onClose}>
      {kind && <OperationForm kind={kind} onClose={onClose} {...rest} />}
    </Dialog>
  );
}

// Same attempt = same request; a changed request is another attempt (and another key).
function fingerprint(input: OperationInput): string {
  return JSON.stringify([input.kind, input.nodeId, input.toNodeId, input.amount, input.comment]);
}

function OperationForm({ nodeId, parentId, kind, currency, onClose, onDone }: Props & { kind: OperationKind }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const [attempt] = useState(() => createAttemptKey());
  const [recipient, setRecipient] = useState<Recipient | null>(null);
  const [recipientError, setRecipientError] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<Form>({ resolver: zodResolver(schema), defaultValues: { amount: '', comment: '' } });

  const action = useAction({
    permsNodeId: nodeId,
    run: async (input: OperationInput) => {
      const outcome = await sendOperation(api, input, attempt.keyFor(fingerprint(input)));
      // A success ends the attempt: the next operation gets its own key.
      attempt.succeeded();
      return outcome;
    },
    onSuccess: (_outcome, input) => refreshAfterOperation(queryClient, input, parentId),
  });

  const onSubmit = async (values: Form) => {
    const parsed = parseMoneyInput(values.amount);
    if (!parsed.ok) return;
    if (kind === 'transfer' && recipient === null) {
      setRecipientError('Choose a recipient.');
      return;
    }
    setRecipientError(null);
    const input: OperationInput = {
      kind,
      nodeId,
      toNodeId: kind === 'transfer' ? (recipient?.id ?? null) : null,
      amount: parsed.value,
      comment: values.comment.trim(),
    };
    const result = await action.execute(input);
    if (result?.ok === true) {
      onDone(kind === 'transfer' ? 'Transfer completed.' : kind === 'credit' ? 'Credit completed.' : 'Debit completed.');
      onClose();
    } else if (result) {
      applyFieldErrors(result.problem, ['amount', 'comment'], setError);
    }
  };

  const retryable = action.problem !== null && (action.problem.status === 0 || action.problem.status >= 500);

  return (
    <form noValidate onSubmit={(event) => void handleSubmit(onSubmit)(event)} className="space-y-4" aria-busy={action.pending}>
      {action.problem && (
        <Alert>
          <span>{describeProblem(action.problem)}</span>
          {retryable && (
            <Button
              variant="secondary"
              className="ml-3"
              busy={action.pending}
              onClick={() => void handleSubmit(onSubmit)()}
            >
              Retry
            </Button>
          )}
        </Alert>
      )}
      {kind === 'transfer' && (
        <div>
          <RecipientPicker fromNodeId={nodeId} value={recipient} onChange={setRecipient} />
          {recipientError && (
            <p role="alert" className="mt-1 text-sm text-red-700">
              {recipientError}
            </p>
          )}
        </div>
      )}
      <TextField
        label={currency === '' ? 'Amount' : `Amount (${currency})`}
        inputMode="decimal"
        autoComplete="off"
        error={errors.amount?.message}
        {...register('amount')}
      />
      <TextField label="Comment" autoComplete="off" error={errors.comment?.message} {...register('comment')} />
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" busy={action.pending}>
          {SUBMIT[kind]}
        </Button>
      </div>
    </form>
  );
}
