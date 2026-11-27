import { createFileRoute, Link, Outlet } from '@tanstack/react-router';

import { problemOf } from '@/api/result';
import { requireSession } from '@/auth/guard';
import { can } from '@/api/resources';
import { NodeNotFound, useNode, usePerms } from '@/features/node';

export const Route = createFileRoute('/nodes/$nodeId')({
  beforeLoad: ({ context, location }) => requireSession(context.api, location.href),
  component: NodeLayout,
});

const tabClass = 'rounded px-3 py-1.5 text-sm hover:bg-slate-100';
const activeTabClass = { className: 'bg-slate-900 text-white hover:bg-slate-900' } as const;

function NodeLayout() {
  const { nodeId } = Route.useParams();
  const node = useNode(nodeId);
  const perms = usePerms(nodeId);

  // 404 means "does not exist" and "no access" alike (spec 06 section 6, EC-118).
  if (node.isError && problemOf(node.error).status === 404) return <NodeNotFound />;

  return (
    <div className="space-y-4">
      <nav aria-label="Node sections" className="flex gap-1 border-b border-slate-200 pb-2">
        <Link to="/nodes/$nodeId" params={{ nodeId }} activeOptions={{ exact: true }} activeProps={activeTabClass} className={tabClass}>
          Overview
        </Link>
        <Link to="/nodes/$nodeId/settings" params={{ nodeId }} activeProps={activeTabClass} className={tabClass}>
          Settings
        </Link>
        <Link to="/nodes/$nodeId/members" params={{ nodeId }} activeProps={activeTabClass} className={tabClass}>
          Members
        </Link>
        <Link to="/nodes/$nodeId/operations" params={{ nodeId }} activeProps={activeTabClass} className={tabClass}>
          Operations
        </Link>
        {can(perms.data, 'report.read') && (
          <Link to="/nodes/$nodeId/reports" params={{ nodeId }} activeProps={activeTabClass} className={tabClass}>
            Reports
          </Link>
        )}
        {can(perms.data, 'audit.read') && (
          <Link to="/nodes/$nodeId/audit" params={{ nodeId }} activeProps={activeTabClass} className={tabClass}>
            Audit
          </Link>
        )}
      </nav>
      <Outlet />
    </div>
  );
}
