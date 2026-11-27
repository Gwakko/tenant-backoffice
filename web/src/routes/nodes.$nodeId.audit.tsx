import { createFileRoute } from '@tanstack/react-router';

import { AuditPanel, parseAuditSearch } from '@/features/audit';

export const Route = createFileRoute('/nodes/$nodeId/audit')({
  validateSearch: parseAuditSearch,
  component: AuditPage,
});

function AuditPage() {
  const { nodeId } = Route.useParams();
  const filters = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <AuditPanel
      nodeId={nodeId}
      filters={filters}
      onFiltersChange={(patch) => {
        void navigate({ search: (previous) => ({ ...previous, ...patch }) });
      }}
    />
  );
}
