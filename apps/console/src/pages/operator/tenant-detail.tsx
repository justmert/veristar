import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Check, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { operatorTenantQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantDetail, TenantAction } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { StellarTomlSummary, VerificationDetail } from "@/components/evidence";
import { Stagger, StaggerItem } from "@/components/motion";
import { Fact } from "@/components/panel";
import { ReasonDialog } from "@/components/reason-dialog";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { formatCount, formatDate, formatDateTimeUtc } from "@/lib/format";
import { outcomeLabel } from "@/lib/outcomes";
import { NotFoundPage } from "../not-found";
import { AuditEntries } from "./audit";
import { EvidenceList, evidenceOf, refreshOperator } from "./decision";

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

// the buttons: one accent action that moves the project forward, an
// outline for the reversible stop, solid red for what cannot be undone
const VARIANT: Record<TenantAction, "default" | "outline" | "destructive"> = {
  approve: "default",
  resume: "default",
  reject: "outline",
  suspend: "outline",
  remove: "destructive",
};

export function TenantDetailPage() {
  const { tenantId = "" } = useParams();
  const detail = useQuery(operatorTenantQuery(tenantId));
  if (detail.isPending) return <Loading label="Loading the project" />;
  if (isNotFound(detail.error)) return <NotFoundPage />;
  if (detail.error) return <ErrorPanel error={detail.error} onRetry={() => detail.refetch()} />;
  return <TenantDetail detail={detail.data} />;
}

// the operator's page of one project (system v2 F-ProjectDetail)
export function TenantDetail({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const a = t.enrollment_answers;
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div className="flex min-w-0 flex-col gap-2">
          <Link
            to="/operator/tenants"
            className="inline-flex w-fit items-center gap-1.5 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring"
          >
            <ArrowLeft aria-hidden className="size-3.5" />
            Projects
          </Link>
          <h2 className="min-w-0 break-words text-page text-fg">{t.name}</h2>
          <p className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-small text-fg-2">
            <StatusPill status={t.status} />
            <span className="font-mono">{t.slug}</span>
            <span>
              Enrolled {formatDate(t.created_at)}
              {t.created_by ? ` by ${t.created_by.github_login}` : ""}
              {t.approved_by ? `, approved by ${t.approved_by.github_login}` : ""}
            </span>
            <span>Status since {formatDateTimeUtc(t.status_changed_at)}</span>
          </p>
          {t.status_reason ? (
            <p className="text-small text-fg">
              <span className="text-fg-3">Reason: </span>
              {t.status_reason}
            </p>
          ) : null}
        </div>
        <Actions detail={detail} />
      </div>

      <IngestTiles detail={detail} />

      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="flex min-w-0 flex-col gap-6">
          {detail.removal ? <Removal removal={detail.removal} /> : null}

          <section aria-labelledby="sites-h" className="flex flex-col gap-3">
            <h3 id="sites-h" className="text-title text-fg">
              Sites
            </h3>
            {detail.sites.length === 0 ? (
              <p className="rounded-lg border border-dashed border-line-strong px-4 py-5 text-small text-fg-2">
                No sites.
              </p>
            ) : (
              <Stagger as="ul" className="flex flex-col gap-3">
                {detail.sites.map((s) => (
                  <StaggerItem as="li" key={s.id}>
                    <SiteEvidence site={s} />
                  </StaggerItem>
                ))}
              </Stagger>
            )}
          </section>

          <Box
            id="ingest-h"
            title="Events across all sites"
            description="Every outcome the gateway recorded, from the hourly ingest counts."
          >
            <div className="grid gap-5 sm:grid-cols-2">
              <Counts title="Last 24 full hours (UTC)" counts={detail.ingest.last_24h} />
              <Counts title="Last 30 full days (UTC)" counts={detail.ingest.last_30d} />
            </div>
          </Box>

          <section aria-labelledby="audit-h" className="flex flex-col gap-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h3 id="audit-h" className="text-title text-fg">
                Recent audit entries
              </h3>
              <Button asChild variant="ghost" size="sm">
                <Link to={`/operator/audit?tenant_id=${t.id}`}>
                  Full audit log of this project
                  <ArrowRight aria-hidden />
                </Link>
              </Button>
            </div>
            <AuditEntries entries={detail.recent_audit} />
          </section>
        </div>

        <aside className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-4">
          <Box id="evidence-h" title="Evidence">
            <EvidenceList items={evidenceOf(detail)} stacked />
          </Box>
          <Box id="answers-h" title="Enrollment answers">
            <dl className="flex flex-col gap-3">
              <Fact label="Website">
                <span className="break-all">{a.website}</span>
              </Fact>
              <Fact label="How it uses Stellar">
                <span className="whitespace-pre-wrap break-words">{a.stellar_usage}</span>
              </Fact>
              <Fact label="SCF round">
                <span className="break-all">{a.scf_round_url ?? "None given"}</span>
              </Fact>
              <Fact label="Description">
                <span className="whitespace-pre-wrap break-words">
                  {t.description || "None given"}
                </span>
              </Fact>
              <Fact label="Terms version">{t.accepted_terms_version ?? "Not accepted"}</Fact>
              <Fact label="Dashboards">{t.provisioning === "ready" ? "Set up" : "Not set up"}</Fact>
            </dl>
            <h4 className="mt-4 text-caption text-fg-3">Account and contract ids</h4>
            {detail.stellar_ids.length === 0 ? (
              <p className="mt-1 text-small text-fg-2">None given.</p>
            ) : (
              <ul className="mt-1.5 flex flex-col gap-1.5" aria-label="Account and contract ids">
                {detail.stellar_ids.map((s) => (
                  <li key={s.id} className="flex flex-col text-small">
                    <code className="break-all font-mono text-caption text-fg">{s.id}</code>
                    <span
                      className={s.valid ? "text-caption text-fg-3" : "text-caption text-warning"}
                    >
                      {s.valid ? (s.type ?? "").replace("_", " ") : "not a valid id"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Box>
          <Box id="members-h" title="Members">
            <ul className="flex flex-col gap-1.5 text-small">
              {detail.members.map((m) => (
                <li key={m.github_id} className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium text-fg">{m.github_login}</span>
                  <span className="text-fg-3">
                    {m.role}, since {formatDate(m.created_at)}
                  </span>
                </li>
              ))}
            </ul>
          </Box>
        </aside>
      </div>
    </div>
  );
}

// a raised section with an h3, under the page's h2
function Box({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      aria-labelledby={id}
      className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised px-4 py-4"
    >
      <div className="flex flex-col gap-0.5">
        <h3 id={id} className="text-title text-fg">
          {title}
        </h3>
        {description ? <p className="text-caption text-fg-3">{description}</p> : null}
      </div>
      {children}
    </section>
  );
}

// the ingest counts the detail carries, as the tiles of the page; the
// detail has no page view or api numbers, those live in compare
function IngestTiles({ detail }: { detail: OperatorTenantDetail }) {
  const sum = (c: Record<string, number>) => Object.values(c).reduce((x, y) => x + y, 0);
  const day = sum(detail.ingest.last_24h);
  const month = sum(detail.ingest.last_30d);
  const accepted = detail.ingest.last_30d.accepted ?? 0;
  const quarantined = detail.ingest.last_30d.quarantined ?? 0;
  const share = (n: number) => (month > 0 ? `${((n / month) * 100).toFixed(1)}%` : "none");
  const tiles = [
    { k: "Events, 24 hours", v: formatCount(day), d: "All outcomes" },
    { k: "Events, 30 days", v: formatCount(month), d: "All outcomes" },
    { k: "Accepted, 30 days", v: share(accepted), d: `${formatCount(accepted)} events` },
    { k: "Quarantined, 30 days", v: share(quarantined), d: "Held back before counting" },
  ];
  return (
    <dl className="grid grid-cols-2 overflow-hidden rounded-lg border border-line bg-raised lg:grid-cols-4">
      {tiles.map((x, i) => (
        <div
          key={x.k}
          className={`flex flex-col gap-1 border-line px-4 py-3.5 ${i % 2 === 1 ? "border-l" : ""} ${i >= 2 ? "max-lg:border-t lg:border-l" : ""}`}
        >
          <dt className="text-caption text-fg-2">{x.k}</dt>
          <dd className="font-mono text-section text-fg tabular">{x.v}</dd>
          <dd className="text-caption text-fg-3">{x.d}</dd>
        </div>
      ))}
    </dl>
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
    <section aria-label="Actions" className="flex flex-wrap items-center gap-2">
      {allowed.length === 0 ? (
        <p className="text-small text-fg-2">No action is possible from this status.</p>
      ) : (
        allowed.map((x) => (
          <Button
            key={x}
            variant={VARIANT[x]}
            className="max-lg:h-(--vs-control-touch)"
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
      className={`flex flex-col gap-3 rounded-lg border bg-raised px-4 py-4 ${site.in_review ? "border-warning-line" : "border-line"}`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <h4 id={`op-site-${site.id}`} className="text-body font-semibold text-fg">
          {site.name}
        </h4>
        <StatusPill status={site.status} />
        {site.in_review ? (
          <span className="text-caption font-medium text-warning">In review</span>
        ) : null}
        <span className="font-mono text-small text-fg-2 sm:ml-auto">{site.domain}</span>
      </div>
      {site.domain_previously_removed ? (
        <p className="text-small text-warning">
          This domain, a parent or a subdomain of it, belonged to a removed project.
        </p>
      ) : null}
      {site.status_reason ? (
        <p className="text-small text-fg">
          <span className="text-fg-3">Reason: </span>
          {site.status_reason}
        </p>
      ) : null}
      <ul className="flex flex-wrap gap-1.5" aria-label="Allowed origins">
        {site.origins.map((o) => (
          <li
            key={o}
            className="rounded-sm border border-line px-1.5 font-mono text-axis text-fg-2"
          >
            {o}
          </li>
        ))}
      </ul>
      <div className="grid gap-4 border-t border-line-subtle pt-3 md:grid-cols-2">
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
      </div>
      {site.in_review ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setAction("approve")}>
            Approve site
          </Button>
          <Button size="sm" variant="outline" onClick={() => setAction("reject")}>
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
    <div className="flex flex-col gap-1.5">
      <h4 className="text-caption text-fg-3">{title}</h4>
      {rows.length === 0 ? (
        <p className="text-small text-fg-2">No events.</p>
      ) : (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 text-small">
          {rows.map(([k, n]) => (
            <div key={k} className="contents">
              <dt className="border-b border-line-subtle py-1 text-fg-2">
                {outcomeLabel(k) === "Other rejections" ? k : outcomeLabel(k)}
              </dt>
              <dd className="border-b border-line-subtle py-1 text-right font-mono text-fg tabular">
                {formatCount(n)}
              </dd>
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
  const steps = Object.entries(removal.steps).sort((x, y) =>
    x[1].done_at.localeCompare(y[1].done_at),
  );
  const done = removal.completed_at !== null;
  return (
    <section
      aria-labelledby="removal-h"
      className={`flex flex-col gap-3 rounded-lg border bg-raised px-4 py-4 ${done ? "border-line" : "border-line-strong"}`}
    >
      <div className="flex flex-col gap-1">
        <h3 id="removal-h" className="text-title text-fg">
          Removal
        </h3>
        <p className="flex items-center gap-2 text-small font-medium text-fg" role="status">
          {done ? (
            <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
          ) : (
            <LoaderCircle aria-hidden className="size-4 animate-spin text-info" />
          )}
          {done
            ? `Completed ${formatDateTimeUtc(removal.completed_at ?? "")}.`
            : "Data is still being deleted."}
        </p>
        <p className="text-small text-fg-2">
          Requested {formatDateTimeUtc(removal.requested_at)}
          {by ? ` by ${by}` : ""}. Aggregates are {removal.keep_aggregates ? "kept" : "deleted"}.
        </p>
      </div>
      {steps.length > 0 ? (
        <ul className="flex flex-col gap-1.5" aria-label="Removal steps">
          {steps.map(([name, s]) => (
            <li
              key={name}
              className="grid grid-cols-[1rem_minmax(0,12rem)_minmax(0,1fr)] items-center gap-2.5 text-small"
            >
              <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />
              <span className="font-mono text-caption text-fg">{name}</span>
              <span className="text-fg-2">Done {formatDateTimeUtc(s.done_at)}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
