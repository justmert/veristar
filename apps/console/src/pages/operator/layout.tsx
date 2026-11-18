import { useQuery } from "@tanstack/react-query";
import { Outlet, useLocation } from "react-router";
import { stepUpQuery } from "@/api/operators";
import { meQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { pageTitle } from "@/components/nav/nav-items";
import { PageHeader } from "@/components/page-header";
import { StepUpGate } from "@/components/step-up";
import { OperatorsOnlyPage } from "@/pages/public/not-found";

// the gate in front of every operator page. the control plane enforces
// operator rights on every route (contract 5.1); this check only keeps the
// pages away from people who would get 403 on each request anyway. the
// sidebar names the pages and the shell draws the line for a paging alert.
// the gate draws the page title (the sidebar's name of the page) until each
// operator page draws its own header.
export function OperatorLayout() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (!me.data.user.is_operator) return <OperatorsOnlyPage />;
  return <SteppedUp userName={me.data.user.display_name} />;
}

// every operator route needs a recent passkey step-up (contract 5.15); the
// pages are drawn only once the session holds one
function SteppedUp({ userName }: { userName: string }) {
  const s = useQuery(stepUpQuery);
  const { pathname } = useLocation();
  if (s.isPending) return <Loading label="Loading" />;
  if (s.error) return <ErrorPanel error={s.error} onRetry={() => s.refetch()} />;
  if (!s.data.stepped_up) return <StepUpGate status={s.data} userName={userName} />;
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader title={pageTitle(pathname)} />
      <Outlet />
    </div>
  );
}
