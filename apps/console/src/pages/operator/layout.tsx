import { useQuery } from "@tanstack/react-query";
import { CircleAlert } from "lucide-react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { meQuery, operatorAlertsQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { criticalCount } from "@/lib/alerts";

const ALERTS_PATH = "/operator/alerts";

const NAV = [
  { to: "/operator", label: "Review queue", end: true },
  { to: ALERTS_PATH, label: "Alerts", end: true },
  { to: "/operator/tenants", label: "Projects", end: false },
  { to: "/operator/web", label: "Global web", end: true },
  { to: "/operator/api", label: "Global API", end: true },
  { to: "/operator/compare", label: "Compare", end: true },
  { to: "/operator/api-keys", label: "API keys", end: true },
  { to: "/operator/ai", label: "AI / MCP", end: true },
  { to: "/operator/audit", label: "Audit log", end: true },
  { to: "/operator/settings", label: "Settings", end: true },
];

// the operator area. the control plane enforces operator rights on every
// route (contract 5.1); this check only keeps the pages away from people who
// would get 403 on each request anyway.
export function OperatorLayout() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (!me.data.user.is_operator) return <OperatorsOnly />;
  return <OperatorArea />;
}

// mounted for operators only, so a tenant's browser never asks for alerts
function OperatorArea() {
  const alerts = useQuery(operatorAlertsQuery);
  const { pathname } = useLocation();
  // a failed refresh keeps the last answer in the cache; the badge and the
  // banner then say the source is unreachable instead of showing old numbers
  const current = alerts.error ? undefined : alerts.data;
  const critical = current ? criticalCount(current.alerts) : 0;

  return (
    <div>
      <h1 className="text-3xl font-semibold tracking-tight">Operator</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Review projects, change their status and the enrollment mode, watch the platform's alerts,
        compare web and API numbers across projects, and manage API keys for machine access. Every
        action is recorded in the audit log.
      </p>
      {critical > 0 ? <CriticalBanner count={critical} linked={pathname !== ALERTS_PATH} /> : null}
      <nav aria-label="Operator" className="mt-6 border-b">
        <ul className="-mb-px flex gap-1 overflow-x-auto">
          {NAV.map((n) => (
            <li key={n.to}>
              <NavLink
                to={n.to}
                end={n.end}
                className={({ isActive }) =>
                  `inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap ${
                    isActive
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted-foreground hover:text-foreground"
                  }`
                }
              >
                {n.label}
                {n.to === ALERTS_PATH ? (
                  <AlertsBadge
                    count={current?.alerts.length ?? null}
                    critical={critical}
                    failed={alerts.error !== null}
                  />
                ) : null}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
      <div className="mt-6">
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
        className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-destructive/10 px-1.5 text-xs font-semibold text-destructive"
        title="Alert source unreachable"
      >
        !<span className="sr-only">, alert source unreachable</span>
      </span>
    );
  }
  if (count === null || count === 0) return null;
  return (
    <span
      className={`inline-flex h-5 min-w-5 items-center justify-center rounded-full px-1.5 text-xs font-semibold tabular-nums ${
        critical > 0 ? "bg-destructive text-white" : "bg-muted text-foreground"
      }`}
    >
      {count}
      <span className="sr-only">{count === 1 ? " alert firing" : " alerts firing"}</span>
    </span>
  );
}

function CriticalBanner({ count, linked }: { count: number; linked: boolean }) {
  return (
    <div
      role="alert"
      className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-2.5 text-sm"
    >
      <CircleAlert aria-hidden className="size-4 shrink-0 text-destructive" />
      <span className="font-medium text-destructive">
        {count === 1 ? "A critical alert is firing." : `${count} critical alerts are firing.`}
      </span>
      {linked ? (
        <Link to={ALERTS_PATH} className="underline">
          Open alerts
        </Link>
      ) : null}
    </div>
  );
}

export function OperatorsOnly() {
  return (
    <section className="max-w-xl">
      <h1 className="text-3xl font-semibold tracking-tight">Operators only</h1>
      <p className="mt-2 text-muted-foreground">
        This area is for operators of the service. Your GitHub account is not on the operator list.
        If it was added recently, sign out and sign in again.
      </p>
    </section>
  );
}
