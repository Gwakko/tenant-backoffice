import { useRouter } from '@tanstack/react-router';
import { createContext, useContext, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react';

import { NodeName } from './NodeName';
import { visibleAncestorIds } from './parent';
import { messageOf, useChildren, useMe, useNodeDetail, type StatusFilter } from './queries';

interface TreeState {
  expanded: ReadonlySet<string>;
  status: StatusFilter;
  currentId: string | undefined;
  tabbableId: string | undefined;
  toggle: (id: string) => void;
  open: (id: string) => void;
}

const TreeContext = createContext<TreeState | null>(null);

function useTree(): TreeState {
  const state = useContext(TreeContext);
  if (state === null) throw new Error('Tree items must be rendered inside NodeTree');
  return state;
}

const STATUS_LABELS: Record<StatusFilter, string> = { all: 'All', active: 'Active', inactive: 'Inactive' };
const STATUS_ORDER: readonly StatusFilter[] = ['all', 'active', 'inactive'];

function nodeHref(id: string): string {
  return `/nodes/${encodeURIComponent(id)}`;
}

function indent(level: number): { paddingLeft: string } {
  return { paddingLeft: `${String(4 + (level - 1) * 14)}px` };
}

interface ItemProps {
  id: string;
  name: string;
  type: string;
  level: number;
  inactive: boolean;
  // Unknown for an access root (the list of roots carries no counter): such a node is expandable.
  expandable: boolean;
}

function TreeItem({ id, name, type, level, inactive, expandable }: ItemProps) {
  const tree = useTree();
  const isExpanded = expandable && tree.expanded.has(id);
  const isCurrent = tree.currentId === id;

  return (
    <li
      role="treeitem"
      aria-level={level}
      aria-label={name}
      aria-expanded={expandable ? isExpanded : undefined}
      aria-selected={isCurrent}
      aria-current={isCurrent ? 'page' : undefined}
      data-node-id={id}
      tabIndex={tree.tabbableId === id ? 0 : -1}
      className="outline-none"
    >
      <div
        style={indent(level)}
        className={`flex items-center gap-1 rounded py-1 pr-2 [li:focus-visible>&]:ring-2 [li:focus-visible>&]:ring-slate-500 ${
          isCurrent ? 'bg-slate-200 font-medium' : 'hover:bg-slate-100'
        }`}
      >
        {expandable ? (
          <button
            type="button"
            tabIndex={-1}
            aria-label={`${isExpanded ? 'Collapse' : 'Expand'} ${name}`}
            onClick={() => {
              tree.toggle(id);
            }}
            className="w-5 shrink-0 text-slate-600"
          >
            {isExpanded ? '▾' : '▸'}
          </button>
        ) : (
          <span className="w-5 shrink-0" aria-hidden="true" />
        )}
        <NodeLink id={id} name={name} />
        <span className="shrink-0 text-xs text-slate-500">{type}</span>
        {inactive && <span className="shrink-0 rounded bg-slate-200 px-1 text-xs text-slate-700">inactive</span>}
      </div>
      {isExpanded && <ChildGroup parentId={id} level={level + 1} />}
    </li>
  );
}

function NodeLink({ id, name }: { id: string; name: string }) {
  const router = useRouter();
  const tree = useTree();
  const href = nodeHref(id);
  const onClick = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    event.preventDefault();
    tree.open(id);
    router.history.push(href);
  };
  return (
    <a href={href} tabIndex={-1} onClick={onClick} className="flex min-w-0 flex-1">
      <NodeName name={name} />
    </a>
  );
}

function ChildGroup({ parentId, level }: { parentId: string; level: number }) {
  const { status } = useTree();
  const query = useChildren(parentId, status);

  let body;
  if (query.isPending) {
    body = <li role="none" style={indent(level)} className="py-1 text-sm text-slate-500">Loading...</li>;
  } else if (query.isError) {
    body = (
      <li role="none" style={indent(level)} className="py-1 text-sm">
        <span role="alert" className="text-red-700">{messageOf(query.error)}</span>{' '}
        <button type="button" onClick={() => void query.refetch()} className="underline">
          Retry
        </button>
      </li>
    );
  } else {
    const items = query.data.pages.flatMap((page) => page.items);
    body = (
      <>
        {items.map((child) => (
          <TreeItem
            key={child.id}
            id={child.id}
            name={child.name}
            type={child.type}
            level={level}
            inactive={child.status === 'inactive'}
            expandable={child.childrenCount > 0}
          />
        ))}
        {items.length === 0 && (
          <li role="none" style={indent(level)} className="py-1 text-sm text-slate-500">No children</li>
        )}
        {query.hasNextPage && (
          <li role="none" style={indent(level)} className="py-1">
            <button
              type="button"
              disabled={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}
              className="rounded border border-slate-300 px-2 py-0.5 text-sm hover:bg-slate-100 disabled:opacity-50"
            >
              Load more
            </button>
          </li>
        )}
      </>
    );
  }
  return <ul role="group">{body}</ul>;
}

function items(tree: HTMLElement): HTMLElement[] {
  return Array.from(tree.querySelectorAll<HTMLElement>('[role="treeitem"]'));
}

function itemOf(target: EventTarget): HTMLElement | null {
  return target instanceof HTMLElement ? target.closest<HTMLElement>('[role="treeitem"]') : null;
}

export interface NodeTreeProps {
  /** The node shown in the main area (the highlighted one); its ancestors are expanded. */
  currentNodeId?: string | undefined;
}

export function NodeTree({ currentNodeId }: NodeTreeProps) {
  const router = useRouter();
  const me = useMe();
  const treeRef = useRef<HTMLUListElement>(null);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [status, setStatus] = useState<StatusFilter>('all');
  const [focusedId, setFocusedId] = useState<string | undefined>(undefined);
  const [revealedFor, setRevealedFor] = useState<string | undefined>(undefined);

  // Reveal the current node once: expand the ancestors the actor can see. They come from the
  // breadcrumbs, because `parentId` of an access root is null.
  const detail = useNodeDetail(currentNodeId);
  const node = detail.data;
  if (node !== undefined && node.id === currentNodeId && revealedFor !== node.id) {
    setRevealedFor(node.id);
    setExpanded((prev) => new Set([...prev, ...visibleAncestorIds(node)]));
  }

  const roots = me.data?.accessRoots ?? [];
  const tabbableId = focusedId ?? roots[0]?.nodeId;

  // Keep one tab stop: if the remembered item is not on screen (a group was closed or the filter
  // changed), the first item takes it.
  useEffect(() => {
    const tree = treeRef.current;
    if (tree === null) return;
    const all = items(tree);
    if (!all.some((el) => el.tabIndex === 0)) all[0]?.setAttribute('tabindex', '0');
  });

  const state: TreeState = {
    expanded,
    status,
    currentId: currentNodeId,
    tabbableId,
    toggle: (id) => {
      setExpanded((prev) => {
        const next = new Set(prev);
        if (!next.delete(id)) next.add(id);
        return next;
      });
    },
    open: (id) => {
      setFocusedId(id);
    },
  };

  const focusItem = (el: HTMLElement | undefined) => {
    if (el === undefined) return;
    el.focus();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLUListElement>) => {
    const tree = treeRef.current;
    const item = itemOf(event.target);
    if (tree === null || item === null || event.target !== item) return;
    const all = items(tree);
    const index = all.indexOf(item);
    const id = item.dataset.nodeId;
    if (id === undefined) return;
    const isOpen = item.getAttribute('aria-expanded') === 'true';
    const canOpen = item.getAttribute('aria-expanded') === 'false';
    let handled = true;
    switch (event.key) {
      case 'ArrowDown':
        focusItem(all[index + 1]);
        break;
      case 'ArrowUp':
        focusItem(all[index - 1]);
        break;
      case 'Home':
        focusItem(all[0]);
        break;
      case 'End':
        focusItem(all[all.length - 1]);
        break;
      case 'ArrowRight':
        if (canOpen) state.toggle(id);
        else if (isOpen) {
          const next = all[index + 1];
          if (next && item.contains(next)) focusItem(next);
        }
        break;
      case 'ArrowLeft':
        if (isOpen) state.toggle(id);
        else focusItem(item.parentElement?.closest<HTMLElement>('[role="treeitem"]') ?? undefined);
        break;
      case 'Enter':
        state.open(id);
        router.history.push(nodeHref(id));
        break;
      default:
        handled = false;
    }
    if (handled) event.preventDefault();
  };

  return (
    <TreeContext.Provider value={state}>
      <div className="flex flex-col gap-2 text-sm">
        <label className="flex items-center gap-2 text-slate-700">
          <span>Show</span>
          <select
            value={status}
            onChange={(event) => {
              const value = STATUS_ORDER.find((candidate) => candidate === event.target.value);
              if (value !== undefined) setStatus(value);
            }}
            className="rounded border border-slate-300 bg-white px-1 py-0.5"
          >
            {STATUS_ORDER.map((value) => (
              <option key={value} value={value}>
                {STATUS_LABELS[value]}
              </option>
            ))}
          </select>
        </label>

        {me.isPending && <p className="text-slate-500">Loading...</p>}
        {me.isError && (
          <p>
            <span role="alert" className="text-red-700">{messageOf(me.error)}</span>{' '}
            <button type="button" onClick={() => void me.refetch()} className="underline">
              Retry
            </button>
          </p>
        )}
        {me.isSuccess && roots.length === 0 && <p className="text-slate-500">No access roots</p>}
        {roots.length > 0 && (
          <ul
            ref={treeRef}
            role="tree"
            aria-label="Nodes"
            onKeyDown={onKeyDown}
            onFocus={(event) => {
              const id = itemOf(event.target)?.dataset.nodeId;
              if (id !== undefined) setFocusedId(id);
            }}
          >
            {roots.map((root) => (
              <TreeItem key={root.nodeId} id={root.nodeId} name={root.name} type={root.type} level={1} inactive={false} expandable />
            ))}
          </ul>
        )}
      </div>
    </TreeContext.Provider>
  );
}
