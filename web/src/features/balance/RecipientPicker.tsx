import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useDeferredValue, useState } from 'react';

import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { fetchChildrenPage, fetchNode, parentIdOf } from '@/api/resources';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { TextField } from '@/components/ui/TextField';
import { useApi } from '@/hooks/useApi';

const PAGE_SIZE = 50;

export interface Recipient {
  id: string;
  name: string;
}

interface Props {
  fromNodeId: string;
  value: Recipient | null;
  onChange: (recipient: Recipient | null) => void;
}

// The recipient of a transfer is chosen, never typed as an identifier: browse the subtree from
// the current node (open a child, go up) and search the children by name.
export function RecipientPicker({ fromNodeId, value, onChange }: Props) {
  const api = useApi();
  const [browseId, setBrowseId] = useState(fromNodeId);
  const [search, setSearch] = useState('');
  const q = useDeferredValue(search.trim());
  const here = useQuery({ queryKey: keys.node(browseId), queryFn: () => fetchNode(api, browseId) });
  const list = useInfiniteQuery({
    queryKey: keys.children(browseId, { purpose: 'picker', q }),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      fetchChildrenPage(api, browseId, { q, limit: PAGE_SIZE, ...(pageParam === undefined ? {} : { cursor: pageParam }) }),
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  const upId = here.data ? parentIdOf(here.data) : null;

  return (
    <fieldset className="space-y-2 rounded border border-slate-200 p-3">
      <legend className="px-1 text-sm font-medium">Recipient</legend>
      <p className="text-sm" data-testid="recipient-selected">
        {value ? (
          <>
            Selected: <span className="font-medium">{value.name}</span>
          </>
        ) : (
          'No recipient selected.'
        )}
      </p>
      <div className="flex items-center justify-between gap-2 text-sm">
        <span className="truncate" title={here.data?.name}>
          In: {here.data?.name ?? '...'}
        </span>
        {upId !== null && (
          <Button
            variant="secondary"
            onClick={() => {
              setBrowseId(upId);
              setSearch('');
            }}
          >
            Up one level
          </Button>
        )}
      </div>
      <TextField
        label="Search by name"
        autoComplete="off"
        value={search}
        onChange={(event) => {
          setSearch(event.target.value);
        }}
      />
      {list.isError && <Alert>{describeProblem(problemOf(list.error))}</Alert>}
      {list.isPending && <p className="text-sm text-slate-600">Loading...</p>}
      {list.isSuccess && items.length === 0 && <p className="text-sm text-slate-600">No nodes here.</p>}
      <ul className="max-h-48 divide-y divide-slate-100 overflow-y-auto">
        {browseId !== fromNodeId && here.data && q === '' && (
          <li className="flex items-center justify-between gap-2 py-1">
            <span className="truncate" title={here.data.name}>
              {here.data.name} (this level)
            </span>
            <Button
              variant="secondary"
              aria-label={`Select ${here.data.name}`}
              onClick={() => {
                onChange({ id: here.data.id, name: here.data.name });
              }}
            >
              Select
            </Button>
          </li>
        )}
        {items.map((item) => (
          <li key={item.id} className="flex items-center justify-between gap-2 py-1">
            <span className="min-w-0 truncate" title={item.name}>
              {item.name}
              {item.status === 'inactive' ? ' (inactive)' : ''}
            </span>
            <span className="flex shrink-0 gap-1">
              {item.childrenCount > 0 && (
                <Button
                  variant="secondary"
                  aria-label={`Open ${item.name}`}
                  onClick={() => {
                    setBrowseId(item.id);
                    setSearch('');
                  }}
                >
                  Open
                </Button>
              )}
              <Button
                variant="secondary"
                aria-label={`Select ${item.name}`}
                onClick={() => {
                  onChange({ id: item.id, name: item.name });
                }}
              >
                Select
              </Button>
            </span>
          </li>
        ))}
      </ul>
      {list.hasNextPage && (
        <Button
          variant="secondary"
          busy={list.isFetchingNextPage}
          onClick={() => {
            void list.fetchNextPage();
          }}
        >
          Load more
        </Button>
      )}
    </fieldset>
  );
}
