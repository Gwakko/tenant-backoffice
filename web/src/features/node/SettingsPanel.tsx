import { zodResolver } from '@hookform/resolvers/zod';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { can, fetchBalance, type Node, type SettingItem } from '@/api/resources';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SelectField } from '@/components/ui/SelectField';
import { TextField } from '@/components/ui/TextField';
import { applyFieldErrors } from '@/hooks/fieldErrors';
import { useApi } from '@/hooks/useApi';
import { useAction } from '@/hooks/useAction';
import { formatMinor, minorToInput, moneyErrorMessages, parseMoneyInput } from '@/lib/money';

import { fetchSettings, putSetting, resetSetting, type SettingKey, type SettingValue } from './actions';
import { useNode, usePerms } from './queries';

const LABELS: Record<SettingKey, string> = {
  display_timezone: 'Display time zone',
  operations_enabled: 'Balance operations',
  max_operation_amount: 'Maximum amount of one operation',
};

// Where the effective value comes from (EC-143): the node itself, a named ancestor the user may
// see, or an ancestor above the access root / the built-in default (no name is revealed).
export function describeSource(item: SettingItem, node: Node): string {
  if (item.own !== null) return 'own';
  if (item.sourceNodeId === null) return 'inherited';
  const source = node.breadcrumbs.find((crumb) => crumb.id === item.sourceNodeId);
  return source ? `inherited from ${source.name}` : 'inherited';
}

export function SettingsPanel({ nodeId }: { nodeId: string }) {
  const api = useApi();
  const node = useNode(nodeId);
  const perms = usePerms(nodeId);
  const settings = useQuery({ queryKey: keys.settings(nodeId), queryFn: () => fetchSettings(api, nodeId) });
  const balance = useQuery({ queryKey: keys.balance(nodeId), queryFn: () => fetchBalance(api, nodeId) });

  if (settings.isPending || node.isPending) return <p className="text-sm text-slate-600">Loading...</p>;
  if (settings.isError) return <Alert>{describeProblem(problemOf(settings.error))}</Alert>;
  if (node.isError) return <Alert>{describeProblem(problemOf(node.error))}</Alert>;
  if (settings.data.length === 0) return <EmptyState>No settings.</EmptyState>;

  const writable = can(perms.data, 'settings.write');
  return (
    <section aria-labelledby="settings-title" className="space-y-4">
      <h1 id="settings-title" className="text-xl font-semibold">
        Settings
      </h1>
      <ul className="divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {settings.data.map((item) => (
          <li key={`${item.key}:${String(item.own)}`} className="p-4">
            <SettingRow
              nodeId={nodeId}
              node={node.data}
              item={item}
              writable={writable}
              currency={balance.data?.currency ?? ''}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}

interface RowProps {
  nodeId: string;
  node: Node;
  item: SettingItem;
  writable: boolean;
  currency: string;
}

function displayValue(item: SettingItem, currency: string): string {
  const value = item.effective;
  if (typeof value === 'boolean') return value ? 'Enabled' : 'Disabled';
  if (item.key === 'max_operation_amount') return `${formatMinor(value)} ${currency}`.trim();
  return value;
}

function initialInput(item: SettingItem): string {
  const value = item.own ?? item.effective;
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  return item.key === 'max_operation_amount' ? minorToInput(value) : value;
}

const rowSchema = z.object({ value: z.string().trim().min(1, 'Enter a value.').max(64, 'The value is too long.') });
type RowForm = z.infer<typeof rowSchema>;

function toSettingValue(key: SettingKey, input: string): { ok: true; value: SettingValue } | { ok: false; message: string } {
  if (key === 'operations_enabled') return { ok: true, value: input === 'true' };
  if (key === 'max_operation_amount') {
    const parsed = parseMoneyInput(input);
    if (!parsed.ok) return { ok: false, message: moneyErrorMessages[parsed.error] };
    if (parsed.value === '0') return { ok: false, message: 'The amount must be greater than zero.' };
    return { ok: true, value: parsed.value };
  }
  return { ok: true, value: input.trim() };
}

function SettingRow({ nodeId, node, item, writable, currency }: RowProps) {
  const api = useApi();
  const queryClient = useQueryClient();
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<RowForm>({ resolver: zodResolver(rowSchema), defaultValues: { value: initialInput(item) } });
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.settings(nodeId) });
  const save = useAction({
    permsNodeId: nodeId,
    run: (value: SettingValue) => putSetting(api, nodeId, item.key, value),
    onSuccess: refresh,
  });
  const reset = useAction({
    permsNodeId: nodeId,
    run: () => resetSetting(api, nodeId, item.key),
    onSuccess: refresh,
  });

  const onSubmit = async (values: RowForm) => {
    const parsed = toSettingValue(item.key, values.value);
    if (!parsed.ok) {
      setError('value', { type: 'validate', message: parsed.message });
      return;
    }
    const result = await save.execute(parsed.value);
    if (result?.ok === false) applyFieldErrors(result.problem, ['value'], setError);
  };

  const problem = save.problem ?? reset.problem;
  const source = describeSource(item, node);
  const label = LABELS[item.key];

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-medium">{label}</h2>
        <p className="text-sm text-slate-700">
          <span data-testid={`value-${item.key}`}>{displayValue(item, currency)}</span>{' '}
          <span data-testid={`source-${item.key}`} className="text-slate-500">
            ({source})
          </span>
        </p>
      </div>
      {problem && <Alert>{describeProblem(problem)}</Alert>}
      {writable && (
        <form
          noValidate
          onSubmit={(event) => void handleSubmit(onSubmit)(event)}
          className="flex flex-wrap items-end gap-2"
          aria-label={`Edit ${label}`}
        >
          <div className="min-w-48 flex-1">
            {item.key === 'operations_enabled' ? (
              <SelectField
                label={`${label} value`}
                options={[
                  { value: 'true', label: 'Enabled' },
                  { value: 'false', label: 'Disabled' },
                ]}
                error={errors.value?.message}
                {...register('value')}
              />
            ) : (
              <TextField
                label={`${label} value`}
                autoComplete="off"
                inputMode={item.key === 'max_operation_amount' ? 'decimal' : 'text'}
                error={errors.value?.message}
                {...register('value')}
              />
            )}
          </div>
          <Button type="submit" busy={save.pending}>
            Save
          </Button>
          {item.own !== null && (
            <Button
              variant="secondary"
              busy={reset.pending}
              onClick={() => {
                void reset.execute(undefined);
              }}
            >
              Reset to inherited
            </Button>
          )}
        </form>
      )}
    </div>
  );
}
