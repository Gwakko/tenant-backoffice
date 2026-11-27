import { createFileRoute } from '@tanstack/react-router';

import { MembersPanel } from '@/features/members';

export const Route = createFileRoute('/nodes/$nodeId/members')({
  component: MembersPage,
});

function MembersPage() {
  const { nodeId } = Route.useParams();
  return <MembersPanel nodeId={nodeId} />;
}
