import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation } from "react-router";
import { stepUpQuery } from "@/api/operators";
import { meQuery, operatorAlertsQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { OPERATOR_NAV, OPERATOR_TABS } from "@/components/nav/nav-items";
import { NavTabs } from "@/components/nav/nav-tabs";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/panel";
import { StepUpGate } from "@/components/step-up";
import { Button } from "@/components/ui/button";
import { criticalCount } from "@/lib/alerts";
import { OperatorsOnlyPage } from "@/pages/not-found";

const ALERTS_PATH = "/operator/alerts";

// the place header of every operator route: the sidebar picks the place, the
// header names it and the tabs pick the view inside it. pages under it start
// with their own h2.
const PLACE_TEXT: Record<string, string> = {
  attention: "Projects waiting for a decision and the platform's firing alerts.",
  projects:
    "Every enrolled project with its status, and its web and API numbers over a range of UTC days.",
  compare: "Web and API numbers of selected projects side by side, per UTC day.",
  admin:
    "Enrollment, the operator roster and passkeys, API keys for machine access, AI assistant connections and the audit log of every operator action.",
};

// one label for the tabs of every place, so tests and assistive technology
// find them the same way on every operator page
export const OPERATOR_TABS_LABEL = "Operator sections";

// the operator area. the control plane enforces operator rights on every
// route (contract 5.1); this check only keeps the pages away from people who
// would get 403 on each request anyway.
export function OperatorLayout() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (!me.data.user.is_operator) return <OperatorsOnly />;
  return <SteppedUp />;
}

// every operator route needs a recent passkey step-up (contract 5.11); the
// area is drawn only once the session holds one
function SteppedUp() {
  const s = useQuery(stepUpQuery);
  if (s.isPending) return <Loading label="Loading" />;
  if (s.error) return <ErrorPanel error={s.error} onRetry={() => s.refetch()} />;
  if (!s.data.stepped_up) return <StepUpGate status={s.data} />;
  return <OperatorArea />;
}

function placeOf(pathname: string) {
  const rest = pathname.replace(/^\/operator\/?/, "");
  return OPERATOR_NAV.find((n) => n.match(rest)) ?? OPERATOR_NAV[0];
}

// mounted for operators only, so a tenant's browser never asks for alerts
function OperatorArea() {
  const alerts = useQuery(operatorAlertsQuery);
  const { pathname } = useLocation();
  // a failed refresh keeps the last answer in the cache; the badge and the
  // banner then say the source is unreachable instead of showing old numbers
  const current = alerts.error ? undefined : alerts.data;
  const critical = current ? criticalCount(current.alerts) : 0;
  const place = placeOf(pathname);
  const tabs = place ? OPERATOR_TABS[place.key] : undefined;

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title={place?.label ?? "Operator"}
        description={place ? PLACE_TEXT[place.key] : undefined}
      />
      {critical > 0 ? <CriticalBanner count={critical} linked={pathname !== ALERTS_PATH} /> : null}
      {tabs ? (
        <NavTabs
          label={OPERATOR_TABS_LABEL}
          items={tabs.map((t) =>
            t.to === ALERTS_PATH
              ? {
                  ...t,
                  badge: (
                    <AlertsBadge
                      count={current?.alerts.length ?? null}
                      critical={critical}
                      failed={alerts.error !== null}
                    />
                  ),
                }
              : t,
          )}
        />
      ) : null}
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  );
}

// the number of firing alerts; red while a critical one is among them. a
// failed check shows "!" rather than a number that may no longer be true
function AlertsBadge({
  count,
  critical,
  failed,
}: {
  count: number | null;
  critical: number;
  failed: boolean;
}) {
  if (failed) {
    return (
      <span
        className="inline-flex h-5 min-w-5 items-center justify-center rounded-full border border-critical-line bg-critical-soft px-1.5 font-mono text-axis font-medium text-critical"
        title="Alert source unreachable"
      >
        !<span className="sr-only">, alert source unreachable</span>
      </span>
    );
  }
  if (count === null || count === 0) return null;
  return (
    <span
      className={
        critical > 0
          ? "inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-critical-solid px-1.5 font-mono text-axis font-medium text-on-critical tabular"
          : "inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-selected px-1.5 font-mono text-axis font-medium text-fg tabular"
      }
    >
      {count}
      <span className="sr-only">{count === 1 ? " alert firing" : " alerts firing"}</span>
    </span>
  );
}

function CriticalBanner({ count, linked }: { count: number; linked: boolean }) {
  return (
    <Notice
      tone="critical"
      role="alert"
      title={count === 1 ? "A critical alert is firing." : `${count} critical alerts are firing.`}
      actions={
        linked ? (
          <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
            <Link to={ALERTS_PATH}>Open alerts</Link>
          </Button>
        ) : null
      }
    >
      A page severity alert means act now.
    </Notice>
  );
}

export function OperatorsOnly() {
  return <OperatorsOnlyPage />;
}
