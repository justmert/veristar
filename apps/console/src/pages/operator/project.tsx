import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  ArrowRight,
  Check,
  CirclePause,
  CirclePlay,
  LoaderCircle,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { type ReactNode, useId, useLayoutEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { operatorTenantQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantDetail, TenantAction } from "@/api/schemas";
import { OperatorCohortPanel } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DecisionPanel } from "@/components/decision-panel";
import { Disclosure } from "@/components/disclosure";
import { StellarTomlSummary, VerificationDetail } from "@/components/evidence";
import { EvidenceList, evidenceOf } from "@/components/evidence-list";
import { IntegrityReportView } from "@/components/integrity";
import { PageHeader } from "@/components/page-header";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { isNotFound } from "@/lib/errors";
import { formatCount, formatDate, formatDateTimeUtc } from "@/lib/format";
import { outcomeLabel } from "@/lib/outcomes";
import { NotFoundPage } from "@/pages/public/not-found";
import { AuditEntries } from "./audit";
import { refreshOperator } from "./decision";
import { ProjectNumbers } from "./numbers";
import { TenantOnchainSection } from "./onchain-parts";

type ActionSpec = {
  label: string;
  title: (name: string) => string;
  description: string;
  reasonRequired: boolean;
  tone: "primary" | "destructive";
  pending: string;
  done: (name: string) => string;
};

// contract 5.5: suspend and remove need a reason; resume takes an optional
// note. approve and reject are the decision panel's.
export const ACTIONS: Record<"suspend" | "resume" | "remove", ActionSpec> = {
  suspend: {
    label: "Suspend",
    title: (n) => `Suspend ${n}?`,
    description:
      "The sites of the project stop accepting events. Its dashboards stay readable. You can resume it later.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Suspending",
    done: (n) => `${n} is suspended. Its members see the reason.`,
  },
  resume: {
    label: "Resume",
    title: (n) => `Resume ${n}?`,
    description: "The project becomes active again and its sites accept events.",
    reasonRequired: false,
    tone: "primary",
    pending: "Resuming",
    done: (n) => `${n} is active again.`,
  },
  remove: {
    label: "Remove",
    title: (n) => `Remove ${n}?`,
    description:
      "The sites stop accepting events. The dashboards and their data are deleted, and the members lose access. The project record and the audit log stay. You cannot undo this.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Removing",
    done: (n) => `${n} is removed. Its data is being deleted.`,
  },
};

const MENU_ORDER = ["resume", "suspend", "remove"] as const;

// the removal steps of contract 5.13 in words
const STEP_TEXT: Record<string, string> = {
  registry: "Project record closed",
  otlp_tokens: "API tokens revoked",
  umami: "Web analytics deleted",
  web_ingest: "Web traffic stopped",
  api: "API data deleted",
  agg: "Daily totals handled",
};

// a removal deletes data in steps; the page checks again until it is done
const REMOVAL_POLL_MS = 10_000;

export function OperatorProjectPage() {
  const { tenantId = "" } = useParams();
  const detail = useQuery({
    ...operatorTenantQuery(tenantId),
    refetchInterval: (q) =>
      q.state.data?.removal && q.state.data.removal.completed_at === null ? REMOVAL_POLL_MS : false,
  });
  if (isNotFound(detail.error)) return <NotFoundPage />;
  if (detail.error)
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Project" />
        <ErrorState
          title="The project did not load"
          error={detail.error}
          onRetry={() => detail.refetch()}
        />
      </div>
    );
  if (detail.isPending)
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Project" />
        <LoadingState label="Loading the project">
          <Skeleton.StatRow count={3} />
          <Skeleton.Table rows={3} columns={2} />
        </LoadingState>
      </div>
    );
  return <TenantDetail detail={detail.data} />;
}

// the operator's page of one project: the status, the one decision, the
// numbers, the sites, the evidence once and the record around it
export function TenantDetail({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const decides = detail.allowed_actions.some((a) => a === "approve" || a === "reject");
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title={t.name}
        meta={<StatusBadge kind="project" status={t.status} operator />}
        description={
          <>
            <span className="font-mono text-fg-2">{t.slug}</span>. Enrolled{" "}
            {formatDate(t.created_at)}
            {t.created_by ? ` by ${t.created_by.display_name}` : ""}
            {t.approved_by ? `, approved by ${t.approved_by.display_name}` : ""}. Status since{" "}
            {formatDateTimeUtc(t.status_changed_at)}.
          </>
        }
        secondary={<MoreActions detail={detail} />}
      />

      {t.status_reason ? (
        <p className="-mt-4 max-w-prose text-small break-words text-fg">
          <span className="text-fg-2">Reason: </span>
          {t.status_reason}
        </p>
      ) : null}

      {decides ? (
        <section
          aria-labelledby="decision-h"
          className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
        >
          <h2 id="decision-h" className="text-title text-fg">
            Decision
          </h2>
          <DecisionPanel
            subject={{ kind: "project", id: t.id, name: t.name, allowed: detail.allowed_actions }}
          />
        </section>
      ) : null}

      {detail.removal ? <Removal removal={detail.removal} /> : null}

      <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="flex min-w-0 flex-col gap-8">
          <ProjectNumbers tenantId={t.id} />

          <section aria-labelledby="sites-h" className="flex flex-col gap-3">
            <h2 id="sites-h" className="text-title text-fg">
              Sites
            </h2>
            {detail.sites.length === 0 ? (
              <p className="text-small text-fg-2">The project has no site yet.</p>
            ) : (
              <ul className="flex flex-col gap-3">
                {detail.sites.map((s) => (
                  <li key={s.id}>
                    <SiteCard site={s} />
                  </li>
                ))}
              </ul>
            )}
          </section>

          <Fold id="integrity" title="Integrity report">
            <IntegrityReportView
              scope="operator"
              tenantId={t.id}
              range={{ from: "", to: "" }}
              siteNames={Object.fromEntries(detail.sites.map((s) => [s.id, s.domain]))}
            />
          </Fold>

          <Fold id="onchain" title="On-chain ids">
            <TenantOnchainSection tenantId={t.id} />
          </Fold>

          <Fold id="events" title="Tracker requests by outcome">
            <p className="mb-3 text-small text-fg-2">
              What the gateway did with each tracker request. The counts roll up every hour.
            </p>
            <div className="grid gap-5 sm:grid-cols-2">
              <Counts title="Last 24 full hours, UTC" counts={detail.ingest.last_24h} />
              <Counts title="Last 30 full days, UTC" counts={detail.ingest.last_30d} />
            </div>
          </Fold>

          <section aria-labelledby="audit-h" className="flex flex-col gap-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h2 id="audit-h" className="text-title text-fg">
                Recent activity
              </h2>
              <Link
                to={`/operator/audit?tenant_id=${t.id}`}
                className="inline-flex items-center gap-1 rounded-sm text-small text-fg underline-offset-4 hover:underline focus-ring"
              >
                Open the full audit log
                <ArrowRight aria-hidden className="size-3.5" />
              </Link>
            </div>
            <AuditEntries entries={detail.recent_audit} limit={5} />
          </section>
        </div>

        <aside className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-4">
          <Box id="evidence-h" title="Evidence">
            <EvidenceList items={evidenceOf(detail)} stacked />
          </Box>
          <OperatorCohortPanel tenantId={t.id} />
          <Answers detail={detail} />
          <Box id="members-h" title="Members">
            <ul className="flex flex-col gap-2 text-small">
              {detail.members.map((m) => (
                <li
                  key={m.github_id ?? m.stellar_address ?? m.passkey_address ?? m.display_name}
                  className="flex min-w-0 flex-wrap items-baseline gap-x-2"
                >
                  <span className="min-w-0 truncate font-medium text-fg">{m.display_name}</span>
                  <span className="text-fg-2">
                    {m.role === "owner" ? "Owner" : "Member"} since {formatDate(m.created_at)}
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

function Box({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section
      aria-labelledby={id}
      className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <h2 id={id} className="text-title text-fg">
        {title}
      </h2>
      {children}
    </section>
  );
}

function Fold({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <Disclosure
      id={id}
      summary={title}
      className="min-w-0"
      summaryClassName="sm:px-5"
      bodyClassName="min-w-0 sm:p-5"
    >
      {children}
    </Disclosure>
  );
}

function Answers({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const a = t.enrollment_answers;
  const rows: [string, ReactNode][] = [
    ["Website", <span className="break-all">{a.website}</span>],
    ["How it uses Stellar", <Clamped text={a.stellar_usage} />],
    [
      "SCF round",
      a.scf_round_url ? <span className="break-all">{a.scf_round_url}</span> : "Not given",
    ],
    ["Description", t.description ? <Clamped text={t.description} /> : "Not given"],
    ["Terms version", t.accepted_terms_version ?? "Not accepted"],
    ["Dashboards", t.provisioning === "ready" ? "Set up" : "Not set up yet"],
  ];
  return (
    <Box id="answers-h" title="Enrollment answers">
      <dl className="flex flex-col gap-3 text-small">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-w-0 flex-col gap-1">
            <dt className="text-caption text-fg-2">{k}</dt>
            <dd className="text-fg">{v}</dd>
          </div>
        ))}
        <div className="flex min-w-0 flex-col gap-1">
          <dt className="text-caption text-fg-2">Account and contract ids</dt>
          <dd>
            {detail.stellar_ids.length === 0 ? (
              <span className="text-fg">Not given</span>
            ) : (
              <ul className="flex flex-col gap-1" aria-label="Account and contract ids">
                {detail.stellar_ids.map((s) => (
                  <li key={s.id} className="flex min-w-0 flex-col">
                    <code className="font-mono text-caption break-all text-fg">{s.id}</code>
                    <span
                      className={s.valid ? "text-caption text-fg-2" : "text-caption text-warning"}
                    >
                      {s.valid ? (s.type ?? "").replace("_", " ") : "Not a valid id"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </dd>
        </div>
      </dl>
    </Box>
  );
}

// a long answer shows its first three lines and a toggle for the rest, so
// one long answer does not push the other answers out of view. the toggle
// shows only when the text has more lines than that.
export function Clamped({ text }: { text: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const id = useId();
  const [open, setOpen] = useState(false);
  const [long, setLong] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || open) return;
    const measure = () => setLong(el.scrollHeight > el.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [open, text]);
  return (
    <span className="flex flex-col items-start gap-1">
      <span
        ref={ref}
        id={id}
        data-clamped={open ? undefined : ""}
        className={cn("whitespace-pre-wrap break-words", !open && "line-clamp-3")}
      >
        {text}
      </span>
      {long ? (
        <Button
          variant="link"
          size="xs"
          aria-expanded={open}
          aria-controls={id}
          onClick={() => setOpen((o) => !o)}
          className="max-lg:min-h-(--vs-control-touch)"
        >
          {open ? "Show less" : "Show more"}
        </Button>
      ) : null}
    </span>
  );
}

// suspend, resume and remove: less frequent than a decision, so they sit in
// one menu, and each goes through the confirmation with its reason
function MoreActions({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const [action, setAction] = useState<(typeof MENU_ORDER)[number] | null>(null);
  const run = useMutation({
    mutationFn: ({ act, reason }: { act: TenantAction; reason: string }) =>
      api.operator.tenantAction(t.id, act, reason),
    onSuccess: async (_d, vars) => {
      setAction(null);
      const spec = ACTIONS[vars.act as (typeof MENU_ORDER)[number]];
      toast.success(spec.done(t.name));
      await refreshOperator();
    },
  });
  const allowed = MENU_ORDER.filter((x) => detail.allowed_actions.includes(x));
  if (allowed.length === 0) return null;
  const spec = action ? ACTIONS[action] : null;
  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button variant="outline" className="max-lg:h-(--vs-control-touch)">
            <MoreHorizontal aria-hidden />
            Actions
          </Button>
        </MenuTrigger>
        <MenuContent align="end">
          {allowed.map((x) => (
            <MenuItem
              key={x}
              tone={ACTIONS[x].tone === "destructive" ? "critical" : undefined}
              onSelect={() => {
                run.reset();
                setAction(x);
              }}
            >
              {x === "suspend" ? (
                <CirclePause aria-hidden />
              ) : x === "remove" ? (
                <Trash2 aria-hidden />
              ) : (
                <CirclePlay aria-hidden />
              )}
              {ACTIONS[x].label} project
            </MenuItem>
          ))}
        </MenuContent>
      </Menu>
      <ConfirmDialog
        open={action !== null}
        onOpenChange={(o) => {
          if (!o) setAction(null);
        }}
        title={spec ? spec.title(t.name) : ""}
        description={spec?.description ?? ""}
        reason={{ required: spec?.reasonRequired ?? true }}
        typed={
          action === "remove" ? { value: t.slug, label: `Type ${t.slug} to confirm` } : undefined
        }
        confirmLabel={spec ? `${spec.label} project` : ""}
        pendingLabel={spec?.pending ?? ""}
        tone={spec?.tone ?? "destructive"}
        pending={run.isPending}
        error={run.error}
        onConfirm={(reason) => action && run.mutate({ act: action, reason })}
      />
    </>
  );
}

// one status per site: "In review" while an operator must decide, else the
// site's own status. the domain check and stellar.toml detail open on request.
function SiteCard({ site }: { site: OperatorSite }) {
  return (
    <article
      aria-labelledby={`op-site-${site.id}`}
      data-site={site.id}
      className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <h3 id={`op-site-${site.id}`} className="min-w-0 truncate text-body font-medium text-fg">
          {site.name}
        </h3>
        {site.in_review ? (
          <span className="inline-flex items-center gap-1 text-caption font-medium text-warning">
            In review
          </span>
        ) : (
          <StatusBadge kind="site" status={site.status} operator />
        )}
        <span className="min-w-0 font-mono text-small break-all text-fg-2 sm:ml-auto">
          {site.domain}
        </span>
      </div>
      {site.domain_previously_removed ? (
        <p className="text-small text-warning">
          This domain, or a parent or subdomain of it, belonged to a removed project.
        </p>
      ) : null}
      {site.status_reason ? (
        <p className="text-small break-words text-fg">
          <span className="text-fg-2">Reason: </span>
          {site.status_reason}
        </p>
      ) : null}
      <Disclosure
        variant="inline"
        summary="Domain check, stellar.toml and allowed origins"
        className="text-small"
        bodyClassName="flex flex-col gap-4 pt-3"
      >
        <div className="grid gap-4 md:grid-cols-2">
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
        <ul className="flex flex-wrap gap-1" aria-label="Allowed origins">
          {site.origins.map((o) => (
            <li
              key={o}
              className="rounded-sm border border-line px-1 font-mono text-caption break-all text-fg-2"
            >
              {o}
            </li>
          ))}
        </ul>
      </Disclosure>
      {site.in_review ? (
        <div className="border-t border-line-subtle pt-3">
          <DecisionPanel subject={{ kind: "site", id: site.id, name: site.domain }} />
        </div>
      ) : null}
    </article>
  );
}

function Counts({ title, counts }: { title: string; counts: Record<string, number> }) {
  const rows = Object.entries(counts).sort((x, y) => y[1] - x[1]);
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <h3 className="text-caption text-fg-2">{title}</h3>
      {rows.length === 0 ? (
        <p className="text-small text-fg-2">No tracker requests.</p>
      ) : (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 text-small">
          {rows.map(([k, n]) => (
            <div key={k} className="contents">
              <dt className="border-b border-line-subtle py-1 text-fg-2">{outcomeLabel(k)}</dt>
              <dd className="border-b border-line-subtle py-1 text-right text-fg tabular">
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
        : removal.requested_by.display_name;
  const steps = Object.entries(removal.steps).sort((x, y) =>
    x[1].done_at.localeCompare(y[1].done_at),
  );
  const done = removal.completed_at !== null;
  return (
    <section
      aria-labelledby="removal-h"
      className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <h2 id="removal-h" className="text-title text-fg">
        Removal
      </h2>
      <p className="flex items-center gap-2 text-small font-medium text-fg" role="status">
        {done ? (
          <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
        ) : (
          <LoaderCircle aria-hidden className="size-4 animate-spin text-fg-2" />
        )}
        {done
          ? `Completed ${formatDateTimeUtc(removal.completed_at ?? "")}.`
          : "The service is deleting the data. This page checks again every 10 seconds."}
      </p>
      <p className="text-small text-fg-2">
        Requested {formatDateTimeUtc(removal.requested_at)}
        {by ? ` by ${by}` : ""}. The daily totals are {removal.keep_aggregates ? "kept" : "deleted"}
        .
      </p>
      {steps.length > 0 ? (
        <ul className="flex flex-col gap-1" aria-label="Removal steps">
          {steps.map(([name, s]) => (
            <li
              key={name}
              className="flex flex-wrap items-center gap-x-2 text-small"
              data-step={name}
            >
              <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />
              <span className="text-fg">{STEP_TEXT[name] ?? name}</span>
              <span className="text-fg-2">{formatDateTimeUtc(s.done_at)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-small text-fg-2">No step is done yet.</p>
      )}
    </section>
  );
}
