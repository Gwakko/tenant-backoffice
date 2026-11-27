import { zodResolver } from '@hookform/resolvers/zod';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { keys } from '@/api/keys';
import { describeProblem } from '@/api/problem';
import { can, fetchNode, fetchPerms, type Member, type Role } from '@/api/resources';
import { problemOf } from '@/api/result';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { EmptyState } from '@/components/ui/EmptyState';
import { SelectField } from '@/components/ui/SelectField';
import { TextField } from '@/components/ui/TextField';
import { applyFieldErrors } from '@/hooks/fieldErrors';
import { useApi } from '@/hooks/useApi';
import { useAction } from '@/hooks/useAction';

import { addMember, changeRole, fetchMembersPage, refreshMembers, removeMember } from './actions';
import { canManage, grantableRoles, ROLES } from './roles';

const roleSchema = z.enum(['viewer', 'analyst', 'admin', 'owner']);

// Password rules of the contract: 12..128 bytes of UTF-8. The password is optional (an existing
// user keeps their own) and is never kept after the request.
const addSchema = z.object({
  email: z.string().trim().min(1, 'Enter an email.').max(254, 'The email is too long.').pipe(z.email('Enter a valid email.')),
  role: roleSchema,
  initialPassword: z
    .string()
    .refine((value) => value === '' || new TextEncoder().encode(value).length >= 12, 'Use at least 12 characters.')
    .refine((value) => new TextEncoder().encode(value).length <= 128, 'The password is too long.'),
});
type AddForm = z.infer<typeof addSchema>;

export function MembersPanel({ nodeId }: { nodeId: string }) {
  const api = useApi();
  const [inherited, setInherited] = useState(false);
  const [adding, setAdding] = useState(false);
  const perms = useQuery({ queryKey: keys.perms(nodeId), queryFn: () => fetchPerms(api, nodeId) });
  const node = useQuery({ queryKey: keys.node(nodeId), queryFn: () => fetchNode(api, nodeId) });
  const list = useInfiniteQuery({
    queryKey: keys.members(nodeId, inherited),
    queryFn: ({ pageParam }) => fetchMembersPage(api, nodeId, inherited, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });

  const own = perms.data?.role;
  const manageable = can(perms.data, 'member.manage');
  const members = list.data?.pages.flatMap((page) => page.items) ?? [];
  const nameOf = (id: string | null): string => {
    if (id === null) return '';
    return node.data?.breadcrumbs.find((crumb) => crumb.id === id)?.name ?? 'a parent node';
  };

  return (
    <section aria-labelledby="members-title" className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 id="members-title" className="text-xl font-semibold">
          Members
        </h1>
        {manageable && (
          <Button
            onClick={() => {
              setAdding(true);
            }}
          >
            Add member
          </Button>
        )}
      </div>

      <div role="group" aria-label="Members list" className="flex gap-2">
        <Button variant={inherited ? 'secondary' : 'primary'} aria-pressed={!inherited} onClick={() => { setInherited(false); }}>
          Direct
        </Button>
        <Button variant={inherited ? 'primary' : 'secondary'} aria-pressed={inherited} onClick={() => { setInherited(true); }}>
          Including inherited
        </Button>
      </div>

      {list.isPending && <p className="text-sm text-slate-600">Loading...</p>}
      {list.isError && <Alert>{describeProblem(problemOf(list.error))}</Alert>}
      {list.isSuccess && members.length === 0 && <EmptyState>No members.</EmptyState>}
      {members.length > 0 && (
        <ul className="divide-y divide-slate-200 rounded border border-slate-200 bg-white">
          {members.map((member) => (
            <li key={`${member.userId}:${member.nodeId}`} className="p-3">
              <MemberRow
                nodeId={nodeId}
                member={member}
                ownRole={own}
                manageable={manageable}
                inheritedName={nameOf(member.inheritedFrom)}
              />
            </li>
          ))}
        </ul>
      )}
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

      <Dialog
        open={adding}
        title="Add member"
        onClose={() => {
          setAdding(false);
        }}
      >
        <AddMemberForm
          nodeId={nodeId}
          ownRole={own}
          onClose={() => {
            setAdding(false);
          }}
        />
      </Dialog>
    </section>
  );
}

function AddMemberForm({ nodeId, ownRole, onClose }: { nodeId: string; ownRole: Role | undefined; onClose: () => void }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const roles = grantableRoles(ownRole);
  const {
    register,
    handleSubmit,
    setError,
    setValue,
    formState: { errors },
  } = useForm<AddForm>({
    resolver: zodResolver(addSchema),
    defaultValues: { email: '', role: roles[0] ?? 'viewer', initialPassword: '' },
  });
  const action = useAction({
    permsNodeId: nodeId,
    run: (values: AddForm) =>
      addMember(api, nodeId, {
        email: values.email,
        role: values.role,
        ...(values.initialPassword === '' ? {} : { initialPassword: values.initialPassword }),
      }),
    onSuccess: () => refreshMembers(queryClient, nodeId),
  });

  const onSubmit = async (values: AddForm) => {
    const result = await action.execute(values);
    // The password does not outlive the request, whatever the outcome.
    setValue('initialPassword', '');
    if (result?.ok === true) onClose();
    else if (result) applyFieldErrors(result.problem, ['email', 'role', 'initialPassword'], setError);
  };

  return (
    <form noValidate onSubmit={(event) => void handleSubmit(onSubmit)(event)} className="space-y-4">
      {action.problem && <Alert>{describeProblem(action.problem)}</Alert>}
      <TextField label="Email" type="email" autoComplete="off" error={errors.email?.message} {...register('email')} />
      <SelectField
        label="Role"
        options={roles.map((role) => ({ value: role, label: role }))}
        error={errors.role?.message}
        {...register('role')}
      />
      <TextField
        label="Initial password"
        type="password"
        autoComplete="new-password"
        hint="Only for a new user; an existing user keeps their password."
        error={errors.initialPassword?.message}
        {...register('initialPassword')}
      />
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" busy={action.pending}>
          Add
        </Button>
      </div>
    </form>
  );
}

interface RowProps {
  nodeId: string;
  member: Member;
  ownRole: Role | undefined;
  manageable: boolean;
  inheritedName: string;
}

function MemberRow({ nodeId, member, ownRole, manageable, inheritedName }: RowProps) {
  const api = useApi();
  const queryClient = useQueryClient();
  const [role, setRole] = useState<Role>(member.role);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const direct = member.inheritedFrom === null;
  const editable = manageable && direct && canManage(ownRole, member.role);
  const roles = grantableRoles(ownRole);

  const change = useAction({
    permsNodeId: nodeId,
    run: (next: Role) => changeRole(api, nodeId, member.userId, next),
    onSuccess: () => refreshMembers(queryClient, nodeId),
  });
  const remove = useAction({
    permsNodeId: nodeId,
    run: () => removeMember(api, nodeId, member.userId),
    onSuccess: () => refreshMembers(queryClient, nodeId),
  });

  const problem = change.problem;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium" title={member.email}>
            {member.email}
          </p>
          <p className="text-sm text-slate-600">
            {member.role}
            {direct ? '' : ` (inherited from ${inheritedName})`}
          </p>
        </div>
        {editable && (
          <div className="flex flex-wrap items-end gap-2">
            <SelectField
              label={`Role of ${member.email}`}
              value={role}
              options={roles.map((value) => ({ value, label: value }))}
              onChange={(event) => {
                const next = ROLES.find((value) => value === event.target.value);
                if (next) setRole(next);
              }}
            />
            <Button
              busy={change.pending}
              disabled={role === member.role}
              onClick={() => {
                void change.execute(role);
              }}
            >
              Save role
            </Button>
            <Button
              variant="danger"
              onClick={() => {
                setConfirmRemove(true);
              }}
            >
              Remove
            </Button>
          </div>
        )}
      </div>
      {problem && <Alert>{describeProblem(problem)}</Alert>}
      <Dialog
        open={confirmRemove}
        title="Remove member"
        onClose={() => {
          setConfirmRemove(false);
        }}
      >
        <div className="space-y-4">
          {remove.problem && <Alert>{describeProblem(remove.problem)}</Alert>}
          <p className="text-sm">Remove {member.email} from this node?</p>
          <div className="flex justify-end gap-2">
            <Button
              variant="secondary"
              onClick={() => {
                setConfirmRemove(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              busy={remove.pending}
              onClick={() => {
                void remove.execute(undefined).then((result) => {
                  if (result?.ok === true) setConfirmRemove(false);
                });
              }}
            >
              Remove
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
