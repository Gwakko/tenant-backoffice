import { createFileRoute } from '@tanstack/react-router';

import { OperationsPanel, parseOperationsSearch } from '@/features/operations';

export const Route = createFileRoute('/nodes/$nodeId/operations')({
  validateSearch: parseOperationsSearch,
  component: OperationsPage,
});

function OperationsPage() {
  const { nodeId } = Route.useParams();
  const filters = Route.useSearch();
  const navigate = Route.useNavigate();
  // A new history entry per change, so that "back" restores the previous filters (EC-123).
  return (
    <OperationsPanel
      nodeId={nodeId}
      filters={filters}
      onFiltersChange={(patch) => {
        void navigate({ search: (previous) => ({ ...previous, ...patch }) });
      }}
    />
  );
}
