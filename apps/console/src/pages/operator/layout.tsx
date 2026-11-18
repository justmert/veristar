import { useQuery } from "@tanstack/react-query";
import { Outlet } from "react-router";
import { stepUpQuery } from "@/api/operators";
import { meQuery } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { StepUpGate } from "@/components/step-up";
import { OperatorsOnlyPage } from "@/pages/public/not-found";

// the gate in front of every operator page. the control plane enforces
// operator rights on every route (contract 5.1); this check only keeps the
// pages away from people who would get 403 on each request anyway. the
// sidebar names the pages and the shell draws the line for a paging alert.
// each operator page draws its own header.
export function OperatorLayout() {
  const me = useQuery(meQuery);
  if (me.isPending) return <GateLoading />;
  if (me.error) return <GateError error={me.error} onRetry={() => me.refetch()} />;
  if (!me.data.user.is_operator) return <OperatorsOnlyPage />;
  return <SteppedUp userName={me.data.user.display_name} />;
}

// every operator route needs a recent passkey step-up (contract 5.15); the
// pages are drawn only once the session holds one
function SteppedUp({ userName }: { userName: string }) {
  const s = useQuery(stepUpQuery);
  if (s.isPending) return <GateLoading />;
  if (s.error) return <GateError error={s.error} onRetry={() => s.refetch()} />;
  if (!s.data.stepped_up)
    return (
      <div className="flex min-w-0 flex-col gap-6">
        <PageHeader
          title="Operator area"
          description="The operator area asks for your passkey before it opens."
        />
        <StepUpGate status={s.data} userName={userName} />
      </div>
    );
  return <Outlet />;
}

function GateLoading() {
  return (
    <LoadingState label="Loading the operator area">
      <Skeleton.Table rows={4} columns={3} />
    </LoadingState>
  );
}

function GateError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader title="Operator area" />
      <ErrorState title="The operator area did not load" error={error} onRetry={onRetry} />
    </div>
  );
}
