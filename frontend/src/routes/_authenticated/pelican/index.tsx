import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import PelicanTest from '@/features/pelican';

function ProtectedPelicanTest() {
  return (
    <RouteGuard requiredScopes={['read_channels']} scopeLevel="system">
      <PelicanTest />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/pelican/')({
  component: ProtectedPelicanTest,
});
