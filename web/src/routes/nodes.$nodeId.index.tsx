import { createFileRoute } from '@tanstack/react-router';

import { BalancePanel } from '@/features/balance';
import { NodeOverview } from '@/features/node';
import { MoveNodeDialog } from '@/features/tree';

export const Route = createFileRoute('/nodes/$nodeId/')({
  component: OverviewPage,
});

// The route composes the features: the card offers a slot for the move dialog, the tree feature
// builds the dialog, and neither imports the other (T-ARCH-04).
function OverviewPage() {
  const { nodeId } = Route.useParams();
  return (
    <NodeOverview
      nodeId={nodeId}
      renderMoveDialog={({ open, onClose }) => <MoveNodeDialog nodeId={nodeId} open={open} onClose={onClose} />}
    >
      <BalancePanel nodeId={nodeId} />
    </NodeOverview>
  );
}
