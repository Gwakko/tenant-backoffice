import { Link, useNavigate } from '@tanstack/react-router';
import { useState, type ReactNode } from 'react';

import { describeProblem } from '@/api/problem';
import { can, parentIdOf } from '@/api/resources';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';

import { ActiveDialog, allowedChildType, CreateChildDialog, DeleteDialog, RenameDialog } from './NodeDialogs';
import { useNode, usePerms } from './queries';

type DialogName = 'create' | 'rename' | 'active' | 'delete' | 'move';

interface MoveSlotProps {
  open: boolean;
  onClose: () => void;
}

interface Props {
  nodeId: string;
  /**
   * The route supplies the move dialog (built by the tree feature): features do not import each
   * other, so the card only offers the button and the slot.
   */
  renderMoveDialog?: (props: MoveSlotProps) => ReactNode;
  /** The balance panel of the balance feature, placed by the route. */
  children?: ReactNode;
}

export function NodeOverview({ nodeId, renderMoveDialog, children }: Props) {
  const navigate = useNavigate();
  const node = useNode(nodeId);
  const perms = usePerms(nodeId);
  const parentId = node.data ? parentIdOf(node.data) : null;
  // node.delete is checked on the parent, so the button follows the permissions of the parent.
  const parentPerms = usePerms(parentId);
  const [dialog, setDialog] = useState<DialogName | null>(null);
  const close = () => {
    setDialog(null);
  };

  if (node.isPending) return <p className="text-sm text-slate-600">Loading...</p>;
  if (node.isError) return <Alert>{describeProblem(problemOf(node.error))}</Alert>;

  const data = node.data;
  const childType = allowedChildType(data.type);
  const isRoot = data.type === 'platform';
  const canMove = can(perms.data, 'node.move') && !isRoot;
  const status =
    data.status === 'inactive' ? 'Inactive' : data.effectiveActive ? 'Active' : 'Inactive through a parent';

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumbs">
        <ol className="flex flex-wrap items-center gap-1 text-sm text-slate-600">
          {data.breadcrumbs.map((crumb, index) => (
            <li key={crumb.id} className="flex min-w-0 items-center gap-1">
              {index > 0 && <span aria-hidden="true">/</span>}
              {crumb.id === data.id ? (
                <span aria-current="page" title={crumb.name} className="max-w-48 truncate font-medium text-slate-900">
                  {crumb.name}
                </span>
              ) : (
                <Link to="/nodes/$nodeId" params={{ nodeId: crumb.id }} title={crumb.name} className="max-w-48 truncate underline">
                  {crumb.name}
                </Link>
              )}
            </li>
          ))}
        </ol>
      </nav>

      <section aria-labelledby="node-title" className="space-y-1">
        <h1 id="node-title" title={data.name} className="truncate text-xl font-semibold">
          {data.name}
        </h1>
        <dl className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-slate-700">
          <div className="flex gap-1">
            <dt className="text-slate-500">Type</dt>
            <dd>{data.type}</dd>
          </div>
          <div className="flex gap-1">
            <dt className="text-slate-500">Status</dt>
            <dd>{status}</dd>
          </div>
          <div className="flex gap-1">
            <dt className="text-slate-500">Children</dt>
            <dd>{data.childrenCount}</dd>
          </div>
        </dl>
      </section>

      {children}

      <section aria-label="Node actions" className="flex flex-wrap gap-2">
        {can(perms.data, 'node.create') && childType !== null && (
          <Button
            onClick={() => {
              setDialog('create');
            }}
          >
            Create {childType}
          </Button>
        )}
        {can(perms.data, 'node.rename') && (
          <Button
            variant="secondary"
            onClick={() => {
              setDialog('rename');
            }}
          >
            Rename
          </Button>
        )}
        {canMove && renderMoveDialog && (
          <Button
            variant="secondary"
            onClick={() => {
              setDialog('move');
            }}
          >
            Move
          </Button>
        )}
        {can(perms.data, 'node.deactivate') && !isRoot && (
          <Button
            variant="secondary"
            onClick={() => {
              setDialog('active');
            }}
          >
            {data.status === 'active' ? 'Deactivate' : 'Activate'}
          </Button>
        )}
        {parentId !== null && can(parentPerms.data, 'node.delete') && !isRoot && (
          <Button
            variant="danger"
            onClick={() => {
              setDialog('delete');
            }}
          >
            Delete
          </Button>
        )}
      </section>

      <CreateChildDialog node={data} parentId={parentId} open={dialog === 'create'} onClose={close} />
      <RenameDialog node={data} parentId={parentId} open={dialog === 'rename'} onClose={close} />
      <ActiveDialog node={data} parentId={parentId} open={dialog === 'active'} onClose={close} />
      <DeleteDialog
        node={data}
        parentId={parentId}
        open={dialog === 'delete'}
        onClose={close}
        onDeleted={() => {
          void navigate(parentId === null ? { to: '/' } : { to: '/nodes/$nodeId', params: { nodeId: parentId } });
        }}
      />
      {renderMoveDialog?.({ open: dialog === 'move', onClose: close })}
    </div>
  );
}
