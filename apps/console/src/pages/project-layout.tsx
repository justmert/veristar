import { useMutation, useQuery } from "@tanstack/react-query";
import { CirclePause, CircleX, Clock, FileText, LoaderCircle } from "lucide-react";
import { Link, Outlet, useLocation, useOutletContext, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, meQuery, tenantQuery } from "@/api/queries";
import type { Me, Tenant } from "@/api/schemas";
import { FormMessage } from "@/components/error-panel";
import { Notice as ToneNotice } from "@/components/notice";
import { ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { termsVersionLabel } from "@/lib/terms";
import { ProjectPagePending } from "@/pages/project/page-frame";
import { NotFoundPage, ProjectsButton, StopPage } from "@/pages/public/not-found";
import { ResubmitForm } from "@/pages/resubmit";
import { queryClient } from "@/query-client";

export type ProjectContext = { tenant: Tenant; me: Me; isOwner: boolean; termsCurrent: boolean };

export function useProject(): ProjectContext {
  return useOutletContext<ProjectContext>();
}

export function ProjectLayout() {
  const { tenantId = "" } = useParams();
  const { pathname } = useLocation();
  const me = useQuery(meQuery);
  const tenant = useQuery(tenantQuery(tenantId));

  // the shell and the sidebar are drawn already; the page keeps its own
  // title line and the shape of its content until the project lands
  if (tenant.isPending || me.isPending)
    return <ProjectPagePending tenantId={tenantId} pathname={pathname} />;
  if (isNotFound(tenant.error)) {
    // a removed project answers 404 everywhere (contract 1.3); /api/me still
    // lists it with the operator's reason (5.10)
    const removed = me.data?.removed_tenants.find((r) => r.id === tenantId);
    return removed ? (
      <RemovedNotice name={removed.name} reason={removed.status_reason} />
    ) : (
      <NotFoundPage project />
    );
  }
  if (tenant.error)
    return (
      <ErrorState
        title="The project did not load"
        error={tenant.error}
        onRetry={() => tenant.refetch()}
      />
    );
  if (me.error)
    return (
      <ErrorState title="Your account did not load" error={me.error} onRetry={() => me.refetch()} />
    );

  const t = tenant.data;
  const current = me.data.terms.current_version;
  const ctx: ProjectContext = {
    tenant: t,
    me: me.data,
    isOwner: t.role === "owner",
    termsCurrent: t.accepted_terms_version === current,
  };

  // the shell's sidebar names the project and navigates it; this frame only
  // puts the notice that applies above the page
  const terms = ctx.termsCurrent ? null : (
    <TermsNotice tenant={t} version={current} isOwner={ctx.isOwner} />
  );
  const status = <StatusNotice tenant={t} isOwner={ctx.isOwner} />;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3 empty:hidden">
        {status}
        {terms}
      </div>
      <Outlet context={ctx} />
    </div>
  );
}

// the older notice api that some pages still import; it draws the shared
// notice so every project page looks the same
const LEGACY_TONE = { warn: "warning", bad: "critical", info: "info" } as const;
export function Notice({
  tone,
  children,
}: {
  tone: "warn" | "bad" | "info";
  children: React.ReactNode;
}) {
  return <ToneNotice tone={LEGACY_TONE[tone]}>{children}</ToneNotice>;
}

export function RemovedNotice({ name, reason }: { name: string; reason: string | null }) {
  return (
    <StopPage
      icon={<CircleX aria-hidden />}
      tone="critical"
      title="This project was removed by an operator"
      actions={<ProjectsButton />}
    >
      <p>
        <span className="font-medium text-fg">{name}</span>: its sites no longer accept traffic and
        its dashboards were deleted.
      </p>
      {reason ? <p>Reason given: {reason}</p> : null}
    </StopPage>
  );
}

export function StatusNotice({ tenant, isOwner }: { tenant: Tenant; isOwner: boolean }) {
  const reason = tenant.status_reason ? ` Reason given: ${tenant.status_reason}` : "";
  switch (tenant.status) {
    case "pending":
      return (
        <ToneNotice
          tone="warning"
          icon={<Clock aria-hidden />}
          title="Waiting for review"
          actions={
            <Button asChild variant="outline" size="sm" className="max-sm:h-(--vs-control-touch)">
              <Link to={`/projects/${tenant.id}/sites`}>Add a site</Link>
            </Button>
          }
        >
          Operators review new projects before their sites receive traffic. You can add sites and
          members now.
        </ToneNotice>
      );
    case "rejected":
      return isOwner ? (
        <ResubmitForm tenant={tenant} />
      ) : (
        <ToneNotice tone="critical" title="Enrollment was not approved">
          {reason ? `${reason.trim()} ` : ""}An owner of this project can change the answers and
          submit them again.
        </ToneNotice>
      );
    case "suspended":
      return (
        <ToneNotice
          tone="info"
          icon={<CirclePause aria-hidden />}
          title="This project is suspended"
        >
          New events from its sites are refused. Its dashboards stay readable.{reason}
        </ToneNotice>
      );
    default:
      if (tenant.provisioning === "pending")
        return (
          <ToneNotice
            tone="info"
            icon={<LoaderCircle aria-hidden className="animate-spin" />}
            title="Setting up the dashboards"
          >
            This takes about a minute. Web dashboards open once it is done.
          </ToneNotice>
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
  const label = termsVersionLabel(version);
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
  const read = (
    <a
      href="/terms"
      target="_blank"
      rel="noopener"
      className="rounded-sm text-small text-fg-2 underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring max-sm:py-2"
    >
      Read the terms
    </a>
  );
  return (
    <ToneNotice
      tone="warning"
      icon={<FileText aria-hidden />}
      title="The terms of the service changed"
      actions={
        isOwner ? (
          <>
            {read}
            <Button
              size="sm"
              onClick={() => accept.mutate()}
              disabled={accept.isPending}
              pending={accept.isPending}
              className="max-sm:h-(--vs-control-touch)"
            >
              Accept the terms
            </Button>
          </>
        ) : (
          read
        )
      }
    >
      {isOwner
        ? `${label}. Until an owner accepts the new terms, you can view this project but not change it.`
        : `${label}. Until an owner accepts the new terms, you can view this project but not change it. Ask an owner to accept them.`}
      <FormMessage error={accept.error} className="mt-2" />
    </ToneNotice>
  );
}
