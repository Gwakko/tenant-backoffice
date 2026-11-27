import { zodResolver } from '@hookform/resolvers/zod';
import { useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { keys } from '@/api/keys';
import type { Node, NodeType } from '@/api/resources';
import { describeProblem } from '@/api/problem';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { SelectField } from '@/components/ui/SelectField';
import { TextField } from '@/components/ui/TextField';
import { applyFieldErrors } from '@/hooks/fieldErrors';
import { useApi } from '@/hooks/useApi';
import { useAction } from '@/hooks/useAction';

import { createChild, deleteNode, refreshAfterNodeChange, renameNode, setNodeActive } from './actions';

// The type of a child follows from the type of the parent (spec 01 section 2).
const CHILD_TYPE: Record<NodeType, NodeType | null> = {
  platform: 'partner',
  partner: 'company',
  company: 'unit',
  unit: null,
};

export function allowedChildType(type: NodeType): NodeType | null {
  return CHILD_TYPE[type];
}

const nameSchema = z.string().trim().min(1, 'Enter a name.').max(100, 'The name is at most 100 characters.');

interface BaseProps {
  node: Node;
  parentId: string | null;
  open: boolean;
  onClose: () => void;
}

function FormActions({ pending, submitLabel, onClose }: { pending: boolean; submitLabel: string; onClose: () => void }) {
  return (
    <div className="flex justify-end gap-2">
      <Button variant="secondary" onClick={onClose}>
        Cancel
      </Button>
      <Button type="submit" busy={pending}>
        {submitLabel}
      </Button>
    </div>
  );
}

const createSchema = z.object({ type: z.enum(['platform', 'partner', 'company', 'unit']), name: nameSchema });
type CreateForm = z.infer<typeof createSchema>;

export function CreateChildDialog({ node, parentId, open, onClose }: BaseProps) {
  const childType = allowedChildType(node.type);
  return (
    <Dialog open={open} title="Create child node" onClose={onClose}>
      {childType !== null && <CreateChildForm node={node} parentId={parentId} childType={childType} onClose={onClose} />}
    </Dialog>
  );
}

function CreateChildForm({
  node,
  parentId,
  childType,
  onClose,
}: Pick<BaseProps, 'node' | 'parentId' | 'onClose'> & { childType: NodeType }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<CreateForm>({ resolver: zodResolver(createSchema), defaultValues: { type: childType, name: '' } });
  const action = useAction({
    permsNodeId: node.id,
    run: (values: CreateForm) => createChild(api, node.id, values),
    onSuccess: () => refreshAfterNodeChange(queryClient, node.id, parentId),
  });

  const onSubmit = async (values: CreateForm) => {
    const result = await action.execute(values);
    if (result?.ok === true) onClose();
    else if (result) applyFieldErrors(result.problem, ['name', 'type'], setError);
  };

  return (
    <form noValidate onSubmit={(event) => void handleSubmit(onSubmit)(event)} className="space-y-4">
      {action.problem && <Alert>{describeProblem(action.problem)}</Alert>}
      <SelectField
        label="Type"
        options={[{ value: childType, label: childType }]}
        error={errors.type?.message}
        {...register('type')}
      />
      <TextField label="Name" autoComplete="off" error={errors.name?.message} {...register('name')} />
      <FormActions pending={action.pending} submitLabel="Create" onClose={onClose} />
    </form>
  );
}

const renameSchema = z.object({ name: nameSchema });
type RenameForm = z.infer<typeof renameSchema>;

export function RenameDialog({ node, parentId, open, onClose }: BaseProps) {
  return (
    <Dialog open={open} title="Rename node" onClose={onClose}>
      <RenameForm node={node} parentId={parentId} onClose={onClose} />
    </Dialog>
  );
}

function RenameForm({ node, parentId, onClose }: Pick<BaseProps, 'node' | 'parentId' | 'onClose'>) {
  const api = useApi();
  const queryClient = useQueryClient();
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors },
  } = useForm<RenameForm>({ resolver: zodResolver(renameSchema), defaultValues: { name: node.name } });
  const action = useAction({
    permsNodeId: node.id,
    run: (values: RenameForm) => renameNode(api, node.id, values.name),
    onSuccess: () => refreshAfterNodeChange(queryClient, node.id, parentId),
  });

  const onSubmit = async (values: RenameForm) => {
    const result = await action.execute(values);
    if (result?.ok === true) onClose();
    else if (result) applyFieldErrors(result.problem, ['name'], setError);
  };

  return (
    <form noValidate onSubmit={(event) => void handleSubmit(onSubmit)(event)} className="space-y-4">
      {action.problem && <Alert>{describeProblem(action.problem)}</Alert>}
      <TextField label="Name" autoComplete="off" error={errors.name?.message} {...register('name')} />
      <FormActions pending={action.pending} submitLabel="Rename" onClose={onClose} />
    </form>
  );
}

export function ActiveDialog({ node, parentId, open, onClose }: BaseProps) {
  const activating = node.status === 'inactive';
  return (
    <Dialog open={open} title={activating ? 'Activate node' : 'Deactivate node'} onClose={onClose}>
      <ActiveForm node={node} parentId={parentId} activating={activating} onClose={onClose} />
    </Dialog>
  );
}

function ActiveForm({
  node,
  parentId,
  activating,
  onClose,
}: Pick<BaseProps, 'node' | 'parentId' | 'onClose'> & { activating: boolean }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const action = useAction({
    permsNodeId: node.id,
    run: () => setNodeActive(api, node.id, activating),
    onSuccess: () => refreshAfterNodeChange(queryClient, node.id, parentId),
  });
  const confirm = async () => {
    const result = await action.execute(undefined);
    if (result?.ok === true) onClose();
  };
  return (
    <div className="space-y-4">
      {action.problem && <Alert>{describeProblem(action.problem)}</Alert>}
      <p className="text-sm">
        {activating
          ? 'The node and its subtree can be used again.'
          : 'An inactive node and its subtree stop accepting balance operations.'}
      </p>
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button variant={activating ? 'primary' : 'danger'} busy={action.pending} onClick={() => void confirm()}>
          {activating ? 'Activate' : 'Deactivate'}
        </Button>
      </div>
    </div>
  );
}

interface DeleteProps extends BaseProps {
  /** Called after the node is gone: the screen leaves the deleted node. */
  onDeleted: () => void;
}

export function DeleteDialog({ node, parentId, open, onClose, onDeleted }: DeleteProps) {
  return (
    <Dialog open={open} title="Delete node" onClose={onClose}>
      <DeleteForm node={node} parentId={parentId} onClose={onClose} onDeleted={onDeleted} />
    </Dialog>
  );
}

function DeleteForm({ node, parentId, onClose, onDeleted }: Pick<DeleteProps, 'node' | 'parentId' | 'onClose' | 'onDeleted'>) {
  const api = useApi();
  const queryClient = useQueryClient();
  const action = useAction({
    permsNodeId: parentId ?? node.id,
    run: () => deleteNode(api, node.id),
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: keys.node(node.id) });
      await refreshAfterNodeChange(queryClient, node.id, parentId);
    },
  });
  const confirm = async () => {
    const result = await action.execute(undefined);
    if (result?.ok === true) {
      onClose();
      onDeleted();
    }
  };
  return (
    <div className="space-y-4">
      {action.problem && <Alert>{describeProblem(action.problem)}</Alert>}
      <p className="text-sm">
        Delete this node permanently? Only a node without children, balance and history can be deleted.
      </p>
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button variant="danger" busy={action.pending} onClick={() => void confirm()}>
          Delete
        </Button>
      </div>
    </div>
  );
}
