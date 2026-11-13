import { useMutation, useQuery } from "@tanstack/react-query";
import { NavLink, Outlet, useOutletContext, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, meQuery, tenantQuery } from "@/api/queries";
import type { Me, Tenant } from "@/api/schemas";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { ResubmitForm } from "@/pages/resubmit";
import { queryClient } from "@/query-client";
import { NotFoundPage } from "./not-found";

export type ProjectContext = { tenant: Tenant; me: Me; isOwner: boolean; termsCurrent: boolean };

export function useProject(): ProjectContext {
  return useOutletContext<ProjectContext>();
}

const NAV = [
  { to: "", label: "Overview", end: true },
  { to: "sites", label: "Sites", end: false },
  { to: "api/traffic", label: "API traffic", end: true },
  { to: "api", label: "API setup", end: true },
  { to: "members", label: "Members", end: true },
  { to: "privacy", label: "Privacy", end: true },
];

export function ProjectLayout() {
  const { tenantId = "" } = useParams();
  const me = useQuery(meQuery);
  const tenant = useQuery(tenantQuery(tenantId));

  if (tenant.isPending || me.isPending) return <Loading label="Loading the project" />;
  if (isNotFound(tenant.error)) {
    // a removed project answers 404 everywhere (contract 1.3); /api/me still
    // lists it with the operator's reason (5.10)
    const removed = me.data?.removed_tenants.find((r) => r.id === tenantId);
    return removed ? (
      <RemovedNotice name={removed.name} reason={removed.status_reason} />
    ) : (
      <NotFoundPage />
    );
  }
  if (tenant.error) return <ErrorPanel error={tenant.error} onRetry={() => tenant.refetch()} />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;

  const t = tenant.data;
  const current = me.data.terms.current_version;
  const ctx: ProjectContext = {
    tenant: t,
    me: me.data,
    isOwner: t.role === "owner",
    termsCurrent: t.accepted_terms_version === current,
  };

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="min-w-0 truncate text-3xl font-semibold tracking-tight">{t.name}</h1>
        <StatusPill status={t.status} />
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        {t.slug}, you are {ctx.isOwner ? "an owner" : "a member"}
      </p>

      <nav aria-label="Project" className="mt-6 border-b">
        <ul className="-mb-px flex gap-1 overflow-x-auto">
          {NAV.map((n) => (
            <li key={n.label}>
              <NavLink
                to={n.to ? `/projects/${t.id}/${n.to}` : `/projects/${t.id}`}
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

      <div className="mt-6 space-y-3">
        <StatusNotice tenant={t} isOwner={ctx.isOwner} />
        {ctx.termsCurrent ? null : (
          <TermsNotice tenant={t} version={current} isOwner={ctx.isOwner} />
        )}
      </div>

      <div className="mt-6">
        <Outlet context={ctx} />
      </div>
    </div>
  );
}

export function Notice({
  tone,
  children,
}: {
  tone: "warn" | "bad" | "info";
  children: React.ReactNode;
}) {
  const cls =
    tone === "bad"
      ? "border-destructive/40 bg-destructive/5"
      : tone === "warn"
        ? "border-warn/40 bg-warn/8"
        : "border-primary/30 bg-primary/5";
  return <div className={`rounded-lg border p-4 text-sm ${cls}`}>{children}</div>;
}

export function RemovedNotice({ name, reason }: { name: string; reason: string | null }) {
  return (
    <section className="max-w-2xl">
      <h1 className="text-3xl font-semibold tracking-tight">{name}</h1>
      <div className="mt-6">
        <Notice tone="bad">
          <p className="font-medium">This project was removed by an operator</p>
          <p className="mt-1 text-muted-foreground">
            Its sites no longer accept traffic and its dashboards were deleted.
          </p>
          {reason ? <p className="mt-1 text-muted-foreground">Reason given: {reason}</p> : null}
        </Notice>
      </div>
    </section>
  );
}

export function StatusNotice({ tenant, isOwner }: { tenant: Tenant; isOwner: boolean }) {
  const reason = tenant.status_reason ? (
    <p className="mt-1 text-muted-foreground">Reason given: {tenant.status_reason}</p>
  ) : null;
  switch (tenant.status) {
    case "pending":
      return (
        <Notice tone="warn">
          <p className="font-medium">Waiting for review</p>
          <p className="mt-1 text-muted-foreground">
            Operators review new projects before their sites receive traffic. You can add sites and
            members now.
          </p>
        </Notice>
      );
    case "rejected":
      return (
        <Notice tone="bad">
          <p className="font-medium">Enrollment was not approved</p>
          {reason}
          {isOwner ? (
            <ResubmitForm tenant={tenant} />
          ) : (
            <p className="mt-1 text-muted-foreground">
              An owner of this project can change the answers and submit them again.
            </p>
          )}
        </Notice>
      );
    case "suspended":
      return (
        <Notice tone="bad">
          <p className="font-medium">This project is suspended</p>
          <p className="mt-1 text-muted-foreground">
            New events from its sites are refused. Its dashboards stay readable.
          </p>
          {reason}
        </Notice>
      );
    default:
      if (tenant.provisioning === "pending")
        return (
          <Notice tone="info">
            <p className="font-medium">Setting up the dashboards</p>
            <p className="mt-1 text-muted-foreground">
              This takes about a minute. Web dashboards open once it is done.
            </p>
          </Notice>
        );
      return null;
  }
}

function TermsNotice({
  tenant,
  version,
  isOwner,
}: {
  tenant: Tenant;
  version: string;
  isOwner: boolean;
}) {
  const accept = useMutation({
    mutationFn: () => api.acceptTerms(tenant.id, version),
    onSuccess: async (t) => {
      queryClient.setQueryData(keys.tenant(t.id), t);
      await queryClient.invalidateQueries({ queryKey: keys.me });
    },
    onError: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.me });
    },
  });
  return (
    <Notice tone="warn">
      <p className="font-medium">The terms of the service changed</p>
      <p className="mt-1 text-muted-foreground">
        Until an owner accepts version {version}, this project can be viewed but not changed.{" "}
        <a
          href="/terms"
          target="_blank"
          rel="noopener"
          className="text-primary underline underline-offset-2"
        >
          Read the terms
        </a>
      </p>
      {isOwner ? (
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <Button size="sm" onClick={() => accept.mutate()} disabled={accept.isPending}>
            Accept version {version}
          </Button>
          <FormMessage error={accept.error} />
        </div>
      ) : (
        <p className="mt-1 text-muted-foreground">Ask an owner of this project to accept them.</p>
      )}
    </Notice>
  );
}
