import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';

import { keyPrefixes, keys } from '@/api/keys';

import { NodeName } from './NodeName';
import { visibleParentId } from './parent';
import { unwrap } from '@/api/result';

import { messageOf, useApi, useMoveTargets, useNodeDetail } from './queries';

const SEARCH_DELAY_MS = 250;

export interface MoveNodeDialogProps {
  /** The node being moved. */
  nodeId: string;
  open: boolean;
  /** Called after a successful move, on Cancel and on Escape. */
  onClose: () => void;
}

// Open/close state for a button that shows the dialog.
export function useMoveNodeDialog(): { isOpen: boolean; show: () => void; close: () => void } {
  const [isOpen, setIsOpen] = useState(false);
  return {
    isOpen,
    show: () => {
      setIsOpen(true);
    },
    close: () => {
      setIsOpen(false);
    },
  };
}

export function MoveNodeDialog({ nodeId, open, onClose }: MoveNodeDialogProps) {
  // The body is mounted only while the dialog is open, so every opening starts clean.
  return open ? <MoveDialogBody key={nodeId} nodeId={nodeId} onClose={onClose} /> : null;
}

function MoveDialogBody({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const inFlight = useRef(false);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog === null || dialog.open) return;
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      setQ(search.trim());
    }, SEARCH_DELAY_MS);
    return () => {
      clearTimeout(timer);
    };
  }, [search]);

  const detail = useNodeDetail(nodeId);
  const targets = useMoveTargets(nodeId, q, true);
  const candidates = targets.data?.pages.flatMap((page) => page.items) ?? [];
  const selected = candidates.find((candidate) => candidate.id === selectedId);

  const refresh = (newParentId: string | undefined) => {
    // The old parent comes from the breadcrumbs (`parentId` is null for an access root); when it
    // is unknown every loaded list of children is refreshed.
    const oldParentId = detail.data ? visibleParentId(detail.data) : null;
    const tasks = [
      queryClient.invalidateQueries({ queryKey: keys.node(nodeId) }),
      queryClient.invalidateQueries({ queryKey: keys.moveTargets(nodeId) }),
    ];
    if (oldParentId === null) tasks.push(queryClient.invalidateQueries({ queryKey: keyPrefixes.children() }));
    else tasks.push(queryClient.invalidateQueries({ queryKey: keys.children(oldParentId) }));
    if (newParentId !== undefined) tasks.push(queryClient.invalidateQueries({ queryKey: keys.children(newParentId) }));
    return Promise.all(tasks);
  };

  const move = useMutation({
    mutationFn: async (newParentId: string) =>
      (await unwrap(api.client.POST('/nodes/{id}/move', { params: { path: { id: nodeId } }, body: { newParentId } }))).data,
    // After success and after an error alike the tree is read again: a refused move means that
    // the server state is not what the screen showed.
    onSettled: (_data, _error, newParentId) => refresh(newParentId),
    onSuccess: () => {
      onClose();
    },
  });

  const confirm = () => {
    if (selected === undefined || inFlight.current) return;
    inFlight.current = true;
    move.mutate(selected.id, {
      onSettled: () => {
        inFlight.current = false;
      },
    });
  };

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby="move-dialog-title"
      onClose={onClose}
      className="m-auto w-full max-w-lg rounded border border-slate-300 p-0 shadow-lg backdrop:bg-slate-900/40"
    >
      <form
        method="dialog"
        className="flex flex-col gap-3 p-4"
        onSubmit={(event) => {
          event.preventDefault();
          confirm();
        }}
      >
        <h2 id="move-dialog-title" className="text-lg font-semibold">
          Move node
        </h2>

        <label className="flex flex-col gap-1 text-sm">
          <span>Search new parent</span>
          <input
            type="search"
            value={search}
            maxLength={100}
            autoComplete="off"
            onChange={(event) => {
              setSearch(event.target.value);
            }}
            className="rounded border border-slate-300 px-2 py-1"
          />
        </label>

        <div className="max-h-64 overflow-y-auto rounded border border-slate-200 p-1">
          {targets.isPending && <p className="p-1 text-sm text-slate-500">Loading...</p>}
          {targets.isError && (
            <p className="p-1 text-sm">
              <span role="alert" className="text-red-700">{messageOf(targets.error)}</span>{' '}
              <button type="button" onClick={() => void targets.refetch()} className="underline">
                Retry
              </button>
            </p>
          )}
          {targets.isSuccess && candidates.length === 0 && (
            <p className="p-1 text-sm text-slate-500">No valid parents found</p>
          )}
          {candidates.length > 0 && (
            <ul role="radiogroup" aria-label="New parent">
              {candidates.map((candidate) => (
                <li key={candidate.id}>
                  <label className="flex min-w-0 items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-100">
                    <input
                      type="radio"
                      name="new-parent"
                      value={candidate.id}
                      checked={selectedId === candidate.id}
                      onChange={() => {
                        setSelectedId(candidate.id);
                      }}
                    />
                    <NodeName name={candidate.name} className="flex-1" />
                    <span className="shrink-0 text-xs text-slate-500">{candidate.type}</span>
                  </label>
                </li>
              ))}
            </ul>
          )}
          {targets.hasNextPage && (
            <button
              type="button"
              disabled={targets.isFetchingNextPage}
              onClick={() => void targets.fetchNextPage()}
              className="mt-1 rounded border border-slate-300 px-2 py-0.5 text-sm hover:bg-slate-100 disabled:opacity-50"
            >
              Load more
            </button>
          )}
        </div>

        {selected !== undefined && (
          <p className="flex min-w-0 items-center gap-1 text-sm">
            <span className="shrink-0">Move under</span>
            <NodeName name={selected.name} className="font-medium" />
          </p>
        )}

        {move.isError && (
          <p role="alert" className="rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">
            {messageOf(move.error)}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={() => {
              onClose();
            }}
            className="rounded border border-slate-300 px-3 py-1 hover:bg-slate-100"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={selected === undefined || move.isPending}
            className="rounded bg-slate-900 px-3 py-1 text-white disabled:opacity-50"
          >
            Move
          </button>
        </div>
      </form>
    </dialog>
  );
}
