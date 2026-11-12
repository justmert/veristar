import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, operatorTenantQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantDetail, TenantAction } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { StellarTomlSummary, VerificationDetail } from "@/components/evidence";
import { ReasonDialog } from "@/components/reason-dialog";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { formatCount, formatDate, formatDateTimeUtc } from "@/lib/format";
import { outcomeLabel } from "@/lib/outcomes";
import { queryClient } from "@/query-client";
import { NotFoundPage } from "../not-found";
import { AuditEntries } from "./audit";

type ActionSpec = {
  label: string;
  title: (name: string) => string;
  description: string;
  reasonRequired: boolean;
  tone: "primary" | "destructive";
  pending: string;
};

// contract 5.5: reject, suspend and remove need a reason; approve and resume
// take an optional note. the member sees whichever is given.
export const ACTIONS: Record<TenantAction, ActionSpec> = {
  approve: {
    label: "Approve",
    title: (n) => `Approve ${n}?`,
    description:
      "The project becomes active. Its dashboards are set up and its sites start receiving traffic.",
    reasonRequired: false,
    tone: "primary",
    pending: "Approving",
  },
  reject: {
    label: "Reject",
    title: (n) => `Reject ${n}?`,
    description:
      "Nothing is set up for the project. Its owners see the reason and can edit and resubmit the enrollment.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Rejecting",
  },
  suspend: {
    label: "Suspend",
    title: (n) => `Suspend ${n}?`,
    description:
      "New events from the project's sites are refused. Its dashboards stay readable. You can resume it later.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Suspending",
  },
  resume: {
    label: "Resume",
    title: (n) => `Resume ${n}?`,
    description: "The project becomes active again and its sites accept events.",
    reasonRequired: false,
    tone: "primary",
    pending: "Resuming",
  },
  remove: {
    label: "Remove",
    title: (n) => `Remove ${n}?`,
    description:
      "The project's sites stop accepting events, its dashboards and their data are deleted, and its members lose access. The project record and the audit log remain. This cannot be undone.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Removing",
  },
};

const ORDER: TenantAction[] = ["approve", "resume", "reject", "suspend", "remove"];

export function TenantDetailPage() {
  const { tenantId = "" } = useParams();
  const detail = useQuery(operatorTenantQuery(tenantId));
  if (detail.isPending) return <Loading label="Loading the project" />;
  if (isNotFound(detail.error)) return <NotFoundPage />;
  if (detail.error) return <ErrorPanel error={detail.error} onRetry={() => detail.refetch()} />;
  return <TenantDetail detail={detail.data} />;
}

async function refreshOperator() {
  await queryClient.invalidateQueries({ queryKey: keys.operator });
}

export function TenantDetail({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const a = t.enrollment_answers;
  return (
    <div className="space-y-6">
      <div>
        <Link
          to="/operator/tenants"
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          Projects
        </Link>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-2">
          <h2 className="min-w-0 truncate text-2xl font-semibold tracking-tight">{t.name}</h2>
          <StatusPill status={t.status} />
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          {t.slug}, enrolled {formatDate(t.created_at)}
          {t.created_by ? ` by ${t.created_by.github_login}` : ""}, status since{" "}
          {formatDateTimeUtc(t.status_changed_at)}
          {t.approved_by ? `, approved by ${t.approved_by.github_login}` : ""}
        </p>
        {t.status_reason ? (
          <p className="mt-1 text-sm">
            <span className="text-muted-foreground">Reason: </span>
            {t.status_reason}
          </p>
        ) : null}
      </div>

      <Actions detail={detail} />

      <section aria-labelledby="answers-h" className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 id="answers-h" className="font-semibold">
          Enrollment answers
        </h3>
        <dl className="mt-3 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[12rem_1fr]">
          <dt className="text-muted-foreground">Website</dt>
          <dd className="break-all">{a.website}</dd>
          <dt className="text-muted-foreground">How it uses Stellar</dt>
          <dd className="whitespace-pre-wrap break-words">{a.stellar_usage}</dd>
          <dt className="text-muted-foreground">SCF round</dt>
          <dd className="break-all">{a.scf_round_url ?? "None given"}</dd>
          <dt className="text-muted-foreground">Description</dt>
          <dd className="whitespace-pre-wrap break-words">{t.description || "None given"}</dd>
          <dt className="text-muted-foreground">Terms version</dt>
          <dd>{t.accepted_terms_version ?? "Not accepted"}</dd>
          <dt className="text-muted-foreground">Dashboards</dt>
          <dd>{t.provisioning === "ready" ? "Set up" : "Not set up"}</dd>
        </dl>
        <h4 className="mt-4 text-sm font-medium">Account and contract ids</h4>
        {detail.stellar_ids.length === 0 ? (
          <p className="mt-1 text-sm text-muted-foreground">None given.</p>
        ) : (
          <ul className="mt-1 space-y-1" aria-label="Account and contract ids">
            {detail.stellar_ids.map((s) => (
              <li key={s.id} className="flex flex-wrap items-baseline gap-x-2 text-sm">
                <code className="break-all font-mono text-xs">{s.id}</code>
                <span className={s.valid ? "text-muted-foreground" : "text-warn"}>
                  {s.valid ? (s.type ?? "").replace("_", " ") : "not a valid id"}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="sites-h" className="space-y-3">
        <h3 id="sites-h" className="font-semibold">
          Sites
        </h3>
        {detail.sites.length === 0 ? (
          <p className="text-sm text-muted-foreground">No sites.</p>
        ) : (
          <ul className="space-y-3">
            {detail.sites.map((s) => (
              <li key={s.id}>
                <SiteEvidence site={s} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="ingest-h" className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 id="ingest-h" className="font-semibold">
          Events across all sites
        </h3>
        <div className="mt-3 grid gap-4 sm:grid-cols-2">
          <Counts title="Last 24 full hours (UTC)" counts={detail.ingest.last_24h} />
          <Counts title="Last 30 full days (UTC)" counts={detail.ingest.last_30d} />
        </div>
      </section>

      <section aria-labelledby="members-h" className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 id="members-h" className="font-semibold">
          Members
        </h3>
        <ul className="mt-2 space-y-1 text-sm">
          {detail.members.map((m) => (
            <li key={m.github_id}>
              <span className="font-medium">{m.github_login}</span>{" "}
              <span className="text-muted-foreground">
                {m.role}, since {formatDate(m.created_at)}
              </span>
            </li>
          ))}
        </ul>
      </section>

      {detail.removal ? <Removal removal={detail.removal} /> : null}

      <section aria-labelledby="audit-h" className="space-y-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 id="audit-h" className="font-semibold">
            Recent audit entries
          </h3>
          <Link
            to={`/operator/audit?tenant_id=${t.id}`}
            className="text-sm text-primary underline underline-offset-2"
          >
            Full audit log of this project
          </Link>
        </div>
        <AuditEntries entries={detail.recent_audit} />
      </section>
    </div>
  );
}

function Actions({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const [action, setAction] = useState<TenantAction | null>(null);
  const run = useMutation({
    mutationFn: ({ act, reason }: { act: TenantAction; reason: string }) =>
      api.operator.tenantAction(t.id, act, reason),
    onSuccess: async () => {
      setAction(null);
      await refreshOperator();
    },
  });
  const allowed = ORDER.filter((x) => detail.allowed_actions.includes(x));
  const spec = action ? ACTIONS[action] : null;

  return (
    <section aria-label="Actions" className="flex flex-wrap gap-2">
      {allowed.length === 0 ? (
        <p className="text-sm text-muted-foreground">No action is possible from this status.</p>
      ) : (
        allowed.map((x) => (
          <Button
            key={x}
            variant={ACTIONS[x].tone === "destructive" ? "destructive" : "default"}
            onClick={() => {
              run.reset();
              setAction(x);
            }}
          >
            {ACTIONS[x].label}
          </Button>
        ))
      )}
      <ReasonDialog
        open={action !== null}
        onOpenChange={(o) => {
          if (!o) setAction(null);
        }}
        title={spec ? spec.title(t.name) : ""}
        description={spec?.description ?? ""}
        reasonRequired={spec?.reasonRequired ?? true}
        confirmLabel={spec?.label ?? ""}
        pendingLabel={spec?.pending ?? ""}
        tone={spec?.tone ?? "destructive"}
        pending={run.isPending}
        error={run.error}
        onConfirm={(reason) => action && run.mutate({ act: action, reason })}
      />
    </section>
  );
}

function SiteEvidence({ site }: { site: OperatorSite }) {
  const [action, setAction] = useState<"approve" | "reject" | null>(null);
  const run = useMutation({
    mutationFn: ({ act, reason }: { act: "approve" | "reject"; reason: string }) =>
      api.operator.siteAction(site.id, act, reason),
    onSuccess: async () => {
      setAction(null);
      await refreshOperator();
    },
  });
  return (
    <article
      aria-labelledby={`op-site-${site.id}`}
      className="space-y-3 rounded-xl border bg-card p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h4 id={`op-site-${site.id}`} className="font-medium">
          {site.name}
        </h4>
        <StatusPill status={site.status} />
        {site.in_review ? <span className="text-xs font-medium text-warn">In review</span> : null}
      </div>
      <p className="text-sm text-muted-foreground">{site.domain}</p>
      {site.domain_previously_removed ? (
        <p className="text-sm text-warn">
          This domain, a parent or a subdomain of it, belonged to a removed project.
        </p>
      ) : null}
      {site.status_reason ? <p className="text-sm">Reason: {site.status_reason}</p> : null}
      <ul className="flex flex-wrap gap-1.5" aria-label="Allowed origins">
        {site.origins.map((o) => (
          <li key={o} className="rounded-md bg-muted px-2 py-0.5 font-mono text-xs">
            {o}
          </li>
        ))}
      </ul>
      <VerificationDetail
        verification={site.verification}
        verifyMethod={site.verify_method}
        verifiedAt={site.verified_at}
        lastVerifyAt={site.last_verify_at}
        lastVerifyError={site.last_verify_error}
      />
      <StellarTomlSummary
        toml={site.stellar_toml}
        checkedAt={site.stellar_toml_checked_at}
        domain={site.domain}
      />
      {site.in_review ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setAction("approve")}>
            Approve site
          </Button>
          <Button size="sm" variant="destructive" onClick={() => setAction("reject")}>
            Reject site
          </Button>
        </div>
      ) : null}
      <ReasonDialog
        open={action !== null}
        onOpenChange={(o) => {
          if (!o) {
            setAction(null);
            run.reset();
          }
        }}
        title={action === "approve" ? `Approve ${site.domain}?` : `Reject ${site.domain}?`}
        description={
          action === "approve"
            ? "The site becomes active and starts receiving traffic."
            : "The site is removed. Its owners see the reason."
        }
        reasonRequired={action === "reject"}
        confirmLabel={action === "approve" ? "Approve site" : "Reject site"}
        pendingLabel={action === "approve" ? "Approving" : "Rejecting"}
        tone={action === "approve" ? "primary" : "destructive"}
        pending={run.isPending}
        error={run.error}
        onConfirm={(reason) => action && run.mutate({ act: action, reason })}
      />
    </article>
  );
}

function Counts({ title, counts }: { title: string; counts: Record<string, number> }) {
  const rows = Object.entries(counts).sort((x, y) => y[1] - x[1]);
  return (
    <div>
      <h4 className="text-sm font-medium">{title}</h4>
      {rows.length === 0 ? (
        <p className="mt-1 text-sm text-muted-foreground">No events.</p>
      ) : (
        <dl className="mt-1 grid grid-cols-[1fr_auto] gap-x-4 gap-y-0.5 text-sm">
          {rows.map(([k, n]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">
                {outcomeLabel(k) === "Other rejections" ? k : outcomeLabel(k)}
              </dt>
              <dd className="text-right tabular-nums">{formatCount(n)}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

function Removal({ removal }: { removal: NonNullable<OperatorTenantDetail["removal"]> }) {
  const by =
    removal.requested_by === null
      ? null
      : typeof removal.requested_by === "string"
        ? removal.requested_by
        : removal.requested_by.github_login;
  const steps = Object.entries(removal.steps);
  return (
    <section aria-labelledby="removal-h" className="rounded-xl border bg-card p-4 sm:p-5">
      <h3 id="removal-h" className="font-semibold">
        Removal
      </h3>
      <p className="mt-1 text-sm">
        Requested {formatDateTimeUtc(removal.requested_at)}
        {by ? ` by ${by}` : ""}. Aggregates are {removal.keep_aggregates ? "kept" : "deleted"}.{" "}
        {removal.completed_at
          ? `Completed ${formatDateTimeUtc(removal.completed_at)}.`
          : "Data is still being deleted."}
      </p>
      {steps.length > 0 ? (
        <ul className="mt-2 space-y-0.5 text-sm" aria-label="Removal steps">
          {steps.map(([name, s]) => (
            <li key={name}>
              <span className="font-mono text-xs">{name}</span>{" "}
              <span className="text-muted-foreground">done {formatDateTimeUtc(s.done_at)}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
