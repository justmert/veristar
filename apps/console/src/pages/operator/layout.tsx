import { useQuery } from "@tanstack/react-query";
import { NavLink, Outlet } from "react-router";
import { meQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";

const NAV = [
  { to: "/operator", label: "Review queue", end: true },
  { to: "/operator/tenants", label: "Projects", end: false },
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

  return (
    <div>
      <h1 className="text-3xl font-semibold tracking-tight">Operator</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Review projects, change their status and the enrollment mode. Every action is recorded in
        the audit log with its reason.
      </p>
      <nav aria-label="Operator" className="mt-6 border-b">
        <ul className="-mb-px flex gap-1 overflow-x-auto">
          {NAV.map((n) => (
            <li key={n.to}>
              <NavLink
                to={n.to}
                end={n.end}
                className={({ isActive }) =>
                  `inline-block border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap ${
                    isActive
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted-foreground hover:text-foreground"
                  }`
                }
              >
                {n.label}
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
