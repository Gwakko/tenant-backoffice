import { createFileRoute } from '@tanstack/react-router';

import { parseReportsSearch, ReportsPanel } from '@/features/reports';

export const Route = createFileRoute('/nodes/$nodeId/reports')({
  validateSearch: parseReportsSearch,
  component: ReportsPage,
});

function ReportsPage() {
  const { nodeId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <ReportsPanel
      nodeId={nodeId}
      search={search}
      onApply={(next) => {
        void navigate({ search: next });
      }}
    />
  );
}
