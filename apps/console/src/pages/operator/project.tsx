import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  ArrowRight,
  Check,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CirclePause,
  CirclePlay,
  CircleX,
  LoaderCircle,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { type ReactNode, useId, useLayoutEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { auditQuery, operatorTenantQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantDetail, TenantAction, TenantStatus } from "@/api/schemas";
import { OperatorCohortPanel } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable, useRowPages } from "@/components/data-table";
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
import { Dialog } from "@/components/ui/dialog";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Tooltip } from "@/components/ui/tooltip";
import { isNotFound } from "@/lib/errors";
import { formatCount, formatDate, formatDateTimeUtc, truncateMiddle } from "@/lib/format";
import { outcomeLabel } from "@/lib/outcomes";
import { termsVersionLabel } from "@/lib/terms";
import { NotFoundPage } from "@/pages/public/not-found";
import { AuditEntries } from "./audit";
import { DecisionSection, refreshOperator } from "./decision";
import { ProjectNumbers, useProjectNumbers, withNumbers } from "./numbers";
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

// the removal steps of contract 5.13 in words; the daily totals step says
// what the setting did with them
const STEP_TEXT: Record<string, string> = {
  registry: "Project record closed",
  otlp_tokens: "API tokens revoked",
  umami: "Web analytics deleted",
  web_ingest: "Web traffic stopped",
  api: "API data deleted",
  chain: "On-chain data deleted",
  reports: "Reports revoked",
};

function stepText(name: string, keepAggregates: boolean): string {
  if (name === "agg") return keepAggregates ? "Daily totals kept" : "Daily totals deleted";
  const known = STEP_TEXT[name];
  if (known) return known;
  // a step this console does not know yet, in sentence case
  const words = name.replace(/_/g, " ");
  return `${words.charAt(0).toUpperCase()}${words.slice(1)} done`;
}

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

// the operator's page of one project: the status, then the same order as
// the review queue at every width (evidence and enrollment answers, the
// decision, the figures when there are any), then the sites and the record
// around it. a removed project shows its record
// only: its data is deleted and no action applies.
export function TenantDetail({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const removed = t.status === "removed";
  const decides = detail.allowed_actions.some((a) => a === "approve" || a === "reject");
  const numbers = useProjectNumbers(t.id, !removed);
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title={t.name}
        meta={<StatusBadge kind="project" status={t.status} operator />}
        details={[
          {
            label: "Slug",
            value: (
              <span className="font-mono" title={t.slug}>
                {truncateMiddle(t.slug, 40)}
              </span>
            ),
          },
          {
            label: "Enrolled",
            value: `${formatDate(t.created_at)}${t.created_by ? ` by ${t.created_by.display_name}` : ""}`,
          },
          ...(t.approved_by ? [{ label: "Approved by", value: t.approved_by.display_name }] : []),
          { label: "Status since", value: formatDateTimeUtc(t.status_changed_at) },
        ]}
        secondary={<MoreActions detail={detail} />}
      />

      {t.status_reason ? (
        <p className="-mt-4 max-w-prose text-small break-words text-fg">
          <span className="text-fg-2">Reason: </span>
          {t.status_reason}
        </p>
      ) : null}

      {detail.removal ? <Removal tenantId={t.id} removal={detail.removal} /> : null}

      <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="flex min-w-0 flex-col gap-8">
          <div className="grid min-w-0 items-start gap-8 lg:grid-cols-2">
            <section aria-labelledby="evidence-h" className="flex min-w-0 flex-col gap-2">
              <h2 id="evidence-h" className="text-title text-fg">
                Evidence
              </h2>
              <EvidenceList items={withNumbers(evidenceOf(detail), numbers)} stacked />
            </section>
            <Answers detail={detail} />
          </div>

          {decides ? (
            <DecisionSection>
              <DecisionPanel
                subject={{
                  kind: "project",
                  id: t.id,
                  name: t.name,
                  allowed: detail.allowed_actions,
                }}
              />
            </DecisionSection>
          ) : null}

          {removed ? null : <ProjectNumbers tenantId={t.id} numbers={numbers} />}

          <Sites sites={detail.sites} decides={!removed} project={t.status} />

          {removed ? null : (
            <div className="flex min-w-0 flex-col gap-3">
              <Fold id="integrity" title="Integrity report">
                <IntegrityReportView
                  scope="operator"
                  tenantId={t.id}
                  range={{ from: "", to: "" }}
                  siteNames={Object.fromEntries(detail.sites.map((s) => [s.id, s.domain]))}
                />
              </Fold>

              <Fold id="onchain" title="On-chain IDs">
                <TenantOnchainSection tenantId={t.id} />
              </Fold>

              <Fold id="events" title="Tracker requests by outcome">
                <div className="grid gap-5 sm:grid-cols-2">
                  <Counts title="Last 24 full hours, UTC" counts={detail.ingest.last_24h} />
                  <Counts title="Last 30 full days, UTC" counts={detail.ingest.last_30d} />
                </div>
              </Fold>
            </div>
          )}

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
            <AuditEntries
              entries={detail.recent_audit}
              limit={5}
              names={{
                [`tenant:${t.id}`]: t.name,
                ...Object.fromEntries(detail.sites.map((s) => [`site:${s.id}`, s.domain])),
              }}
            />
          </section>
        </div>

        <aside className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-4">
          <OperatorCohortPanel tenantId={t.id} readOnly={removed} />
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

// the enrollment answers beside the evidence, label over value like the
// evidence list. the scf round link is one of the evidence facts.
export function Answers({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const a = t.enrollment_answers;
  const rows: [string, ReactNode][] = [
    ["Website", <span className="break-all">{a.website}</span>],
    ["How it uses Stellar", <Clamped text={a.stellar_usage} />],
    ...(t.description
      ? [["Description", <Clamped text={t.description} />] as [string, ReactNode]]
      : []),
  ];
  return (
    <section aria-labelledby="answers-h" className="flex min-w-0 flex-col gap-2">
      <h2 id="answers-h" className="text-title text-fg">
        Enrollment answers
      </h2>
      <dl className="flex flex-col gap-2 text-small">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-w-0 flex-col">
            <dt className="text-fg-2">{k}</dt>
            <dd className="text-fg">{v}</dd>
          </div>
        ))}
        {detail.stellar_ids.length > 0 ? (
          <div className="flex min-w-0 flex-col">
            <dt className="text-fg-2">Account and contract IDs</dt>
            <dd>
              <ul className="flex flex-col gap-1" aria-label="Account and contract IDs">
                {detail.stellar_ids.map((s) => (
                  <li key={s.id} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <code className="min-w-0 font-mono text-caption break-all text-fg">{s.id}</code>
                    <span
                      className={s.valid ? "text-caption text-fg-2" : "text-caption text-warning"}
                    >
                      {s.valid ? (s.type ?? "").replace("_", " ") : "Not a valid id"}
                    </span>
                  </li>
                ))}
              </ul>
            </dd>
          </div>
        ) : null}
      </dl>
      <div className="flex min-w-0 flex-wrap gap-x-6 gap-y-1 text-caption text-fg-2">
        <span>
          Terms:{" "}
          <span className="text-fg">
            {t.accepted_terms_version
              ? termsVersionLabel(t.accepted_terms_version)
              : "Not accepted"}
          </span>
        </span>
        <span>
          Dashboards:{" "}
          <span className="text-fg">
            {t.provisioning === "ready" ? "Set up" : "Not set up yet"}
          </span>
        </span>
      </div>
    </section>
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

// the sites as dense rows, so many sites do not push the record down: the
// first rows, then the rest on request
const SITES_SHOWN = 10;

function Sites({
  sites,
  decides,
  project,
}: {
  sites: OperatorSite[];
  decides: boolean;
  project: TenantStatus;
}) {
  const [open, setOpen] = useState<OperatorSite | null>(null);
  // sites that wait for a decision come first, and each gets its decision
  // above the table
  const ordered = [...sites].sort((a, b) => Number(b.in_review) - Number(a.in_review));
  const waiting = decides ? ordered.filter((s) => s.in_review) : [];
  const paged = useRowPages(ordered, SITES_SHOWN);
  const columns: Column<OperatorSite>[] = [
    {
      key: "site",
      header: "Site",
      primary: true,
      minWidth: "min-w-48",
      className: "w-full max-w-0",
      truncate: (x) => `${x.name}, ${x.domain}`,
      cell: (x) => (
        <span className="flex min-w-0 flex-col py-1">
          <span className="flex min-w-0 items-center gap-1 font-medium text-fg">
            <span className="truncate">{x.name}</span>
            {x.domain_previously_removed ? (
              <Tooltip content="This domain, or a parent or subdomain of it, belonged to a removed project.">
                <CircleAlert
                  aria-label="Domain of a removed project"
                  className="size-3.5 shrink-0 text-warning"
                />
              </Tooltip>
            ) : null}
          </span>
          <span className="truncate font-mono text-caption font-normal text-fg-2">{x.domain}</span>
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (x) => (
        <StatusBadge
          kind="site"
          status={x.in_review ? "pending" : x.status}
          project={project}
          operator
        />
      ),
    },
    {
      key: "checks",
      header: "Checks",
      mobile: true,
      minWidth: "min-w-44",
      cell: (x) => (
        <span className="flex flex-col gap-1 py-1 text-small">
          <DomainFact site={x} />
          <TomlFact site={x} />
        </span>
      ),
    },
    {
      key: "origins",
      header: "Origins",
      align: "right",
      numeric: true,
      cell: (x) => formatCount(x.origins.length),
    },
    {
      key: "details",
      header: "Actions",
      align: "right",
      action: true,
      cell: (x) => (
        <Menu>
          <MenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Actions for ${x.domain}`}
              className="relative z-(--vs-layer-raised) max-lg:size-(--vs-control-touch)"
            >
              <MoreHorizontal aria-hidden />
            </Button>
          </MenuTrigger>
          <MenuContent align="end">
            <MenuItem onSelect={() => setOpen(x)}>Show checks</MenuItem>
          </MenuContent>
        </Menu>
      ),
    },
  ];
  return (
    <section aria-labelledby="sites-h" className="flex min-w-0 flex-col gap-3">
      <h2 id="sites-h" className="text-title text-fg">
        Sites{" "}
        {sites.length > 0 ? (
          <span className="font-normal text-fg-2">({formatCount(sites.length)})</span>
        ) : null}
      </h2>
      {waiting.length > 0 ? (
        <ul className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised">
          {waiting.map((x) => (
            <li key={x.id} className="border-b border-line-subtle last:border-0">
              <SiteRow site={x} project={project} />
            </li>
          ))}
        </ul>
      ) : null}
      {sites.length === 0 ? (
        <p className="text-small text-fg-2">The project has no site.</p>
      ) : (
        <DataTable<OperatorSite>
          label="Sites"
          columns={columns}
          rows={paged.rows}
          getRowKey={(x) => x.id}
          rowLabel={(x) => x.name}
          rowProps={(x) => ({ "data-site": x.id })}
          footer={paged.footer}
        />
      )}
      <Dialog
        open={open !== null}
        onOpenChange={(o) => {
          if (!o) setOpen(null);
        }}
        title={open ? `Checks of ${open.domain}` : ""}
      >
        {open ? <SiteChecks site={open} /> : null}
      </Dialog>
    </section>
  );
}

function DomainFact({ site }: { site: OperatorSite }) {
  return (
    <SiteFact
      state={
        site.verification === "verified"
          ? "ok"
          : site.verification === "unchecked"
            ? "absent"
            : "missing"
      }
    >
      {site.verification === "verified"
        ? "Domain verified"
        : site.verification === "unchecked"
          ? "Domain not checked yet"
          : "Domain not verified"}
    </SiteFact>
  );
}

function TomlFact({ site }: { site: OperatorSite }) {
  const toml = site.stellar_toml?.status;
  return (
    <SiteFact state={toml === "found" ? "ok" : toml === undefined ? "absent" : "missing"}>
      {toml === "found"
        ? "stellar.toml found"
        : toml === undefined
          ? "stellar.toml not checked yet"
          : "No stellar.toml"}
    </SiteFact>
  );
}

// the full checks of one site: the domain check, the stellar.toml, the
// allowed origins and the reason of its status
function SiteChecks({ site }: { site: OperatorSite }) {
  return (
    <div className="flex min-w-0 flex-col gap-4 text-small">
      {site.domain_previously_removed ? (
        <p className="text-warning">
          This domain, or a parent or subdomain of it, belonged to a removed project.
        </p>
      ) : null}
      {site.status_reason ? (
        <p className="break-words text-fg">
          <span className="text-fg-2">Reason: </span>
          {site.status_reason}
        </p>
      ) : null}
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
      <div className="flex flex-col gap-1">
        <h3 className="text-caption text-fg-2">Allowed origins</h3>
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
      </div>
    </div>
  );
}

// one fact of a site with its status in words: the icon and the colour
// only repeat what the text says
function SiteFact({ state, children }: { state: "ok" | "missing" | "absent"; children: string }) {
  const Icon = state === "ok" ? CircleCheck : state === "missing" ? CircleX : CircleDashed;
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap" data-fact={state}>
      <Icon
        aria-hidden
        className={cn(
          "size-3.5",
          state === "ok" ? "text-success" : state === "missing" ? "text-critical" : "text-fg-3",
        )}
        strokeWidth={2.4}
      />
      {children}
    </span>
  );
}

// a site that waits for a decision: its name, its checks in words, the
// full checks on request, and the decision
function SiteRow({ site, project }: { site: OperatorSite; project: TenantStatus }) {
  return (
    <article
      aria-labelledby={`op-site-${site.id}`}
      data-review-site={site.id}
      className="flex min-w-0 flex-col gap-2 px-4 py-3 sm:px-5"
    >
      <div className="grid min-w-0 gap-x-4 gap-y-1 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
        <div className="flex min-w-0 flex-col">
          <h3 id={`op-site-${site.id}`} className="truncate text-body font-medium text-fg">
            {site.name}
          </h3>
          <span className="truncate font-mono text-caption text-fg-2" title={site.domain}>
            {site.domain}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-small text-fg-2">
          <StatusBadge kind="site" status="pending" project={project} operator />
          <DomainFact site={site} />
          <TomlFact site={site} />
        </div>
      </div>
      <Disclosure
        variant="inline"
        summary="Checks and allowed origins"
        className="text-small"
        bodyClassName="pt-3"
      >
        <SiteChecks site={site} />
      </Disclosure>
      <div className="border-t border-line-subtle pt-3">
        <DecisionPanel subject={{ kind: "site", id: site.id, name: site.domain }} />
      </div>
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

// the login of the person who asked for the removal. the record holds
// their user id; their audit entries on the project carry the login.
function useRequester(tenantId: string, by: string | { display_name: string } | null) {
  const userId = typeof by === "string" ? by : null;
  const audit = useInfiniteQuery({
    ...auditQuery({
      tenant_id: tenantId,
      actor: `user:${userId ?? ""}`,
      action: "",
      target: "",
      from: "",
      to: "",
    }),
    enabled: userId !== null,
    staleTime: Number.POSITIVE_INFINITY,
  });
  if (by === null) return null;
  if (typeof by !== "string") return by.display_name;
  return audit.data?.pages[0]?.items.find((e) => e.actor_login)?.actor_login ?? null;
}

function Removal({
  tenantId,
  removal,
}: {
  tenantId: string;
  removal: NonNullable<OperatorTenantDetail["removal"]>;
}) {
  const by = useRequester(tenantId, removal.requested_by);
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
      <p className="text-small text-fg-2" data-requested="">
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
              <span className="text-fg">{stepText(name, removal.keep_aggregates)}</span>
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
