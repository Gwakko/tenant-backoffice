import { createFileRoute } from '@tanstack/react-router';

import { SettingsPanel } from '@/features/node';

export const Route = createFileRoute('/nodes/$nodeId/settings')({
  component: SettingsPage,
});

function SettingsPage() {
  const { nodeId } = Route.useParams();
  return <SettingsPanel nodeId={nodeId} />;
}
