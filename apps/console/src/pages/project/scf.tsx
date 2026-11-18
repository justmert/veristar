// scf review: what scf reviewers see about the project, read through the
// operator views' own queries for the same range (contract 16.3), and the
// signed reports that carry the same figures to anyone (contract 20). the
// standing comes first, then the full integrity report with its appeal,
// the rules met, the figures, and the reports.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Download, ExternalLink, FileSignature, MoreHorizontal } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import type { Kpi, KpiKey } from "@/api/cohorts";
import { exportPaths, type SCFView, scfViewQuery } from "@/api/onchain";
import { integrityQuery, sitesQuery } from "@/api/queries";
import { type ReportSummary, reportApi, reportKeysOf, reportsQuery } from "@/api/reports";
import type { TenantStatus } from "@/api/schemas";
import { OwnCohortPanel, percentileHiddenReason } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { dayCountsText, gradeMeaning, IntegrityPanel } from "@/components/integrity";
import { DayStrip } from "@/components/onchain";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PageHeader } from "@/components/page-header";
import { DateField } from "@/components/range-control";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Tooltip } from "@/components/ui/tooltip";
import { errorMessage } from "@/lib/errors";
import {
  countOf,
  formatChange,
  formatCohortPercentile,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatDuration,
  formatLatency,
  formatPercent,
  formatPoints,
  formatTick,
  OPEN_BUCKET,
} from "@/lib/format";
import {
  chainGap,
  DAYS_WITHOUT_DATA,
  NO_VERIFIED_IDS,
  shortKey,
  weeklyActiveText,
} from "@/lib/onchain";
import {
  closedDays,
  downloadFile,
  lastFinalDay,
  MAX_REPORT_DAYS,
  validatePeriod,
} from "@/lib/reports";
import { useProject } from "@/pages/project-layout";
import { PAGE_HEADERS, ScfSkeleton } from "./page-frame";

const RANGE = {};

export function ScfReviewPage() {
  const { tenant, isOwner } = useProject();
  const q = useQuery(scfViewQuery(tenant.id, RANGE));
  const v = q.data;

  const header = (
    <PageHeader
      title={PAGE_HEADERS.scf.title}
      description={
        v
          ? `What SCF reviewers see about your project for ${formatDateRange(v.from, v.to)}, UTC. They read the same figures.`
          : PAGE_HEADERS.scf.description
      }
      secondary={
        v ? (
          <Button asChild variant="outline">
            <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        ) : null
      }
    />
  );

  let body: ReactNode;
  if (q.isPending) body = <ScfSkeleton />;
  else if (q.error || !v)
    body = (
      <ErrorState
        title="What SCF reviewers see did not load"
        error={q.error}
        onRetry={() => q.refetch()}
      />
    );
  else
    body = (
      <>
        <Standing v={v} tenantId={tenant.id} isOwner={isOwner} />
        {v.in_operator_views ? (
          <>
            <IntegritySection v={v} />
            <RulesMet v={v} />
            <Figures v={v} />
          </>
        ) : null}
      </>
    );

  return (
    <div className="flex min-w-0 flex-col gap-8">
      {header}
      {body}
      <SignedReports />
    </div>
  );
}

// ---- standing: grade, cohort, change and place in the cohort

function Standing({ v, tenantId, isOwner }: { v: SCFView; tenantId: string; isOwner: boolean }) {
  return (
    <section aria-label="Standing" className="flex min-w-0 flex-col gap-4">
      <h2 className="text-section text-fg">Standing</h2>
      {v.in_operator_views ? (
        <div className="grid min-w-0 gap-3 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
          <GradeCard v={v} />
          <OwnCohortPanel tenantId={tenantId} record={v.cohort} isOwner={isOwner} />
        </div>
      ) : (
        <>
          <EmptyState title="Not in the SCF views">
            This project is internal to the platform. SCF reviewers do not see it.
          </EmptyState>
          <OwnCohortPanel tenantId={tenantId} record={v.cohort} isOwner={isOwner} />
        </>
      )}
      {v.in_operator_views ? <CohortPlace v={v} /> : null}
    </section>
  );
}

// the grade of the project's row for the range: the word and colour of the
// shared badge, what it means, and the days behind it
function GradeCard({ v }: { v: SCFView }) {
  const g = v.integrity;
  const sites = v.sites?.sites ?? null;
  return (
    <div
      data-testid="standing-grade"
      className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <h3 className="text-caption text-fg-2">Integrity grade</h3>
      {g ? (
        <>
          <StatusBadge kind="grade" grade={g.grade} className="w-fit" />
          {sites === 0 ? (
            <p className="text-small text-fg">No site to grade. Add a site to get a grade.</p>
          ) : (
            <>
              <p className="text-small text-fg">{gradeMeaning(g.grade)}</p>
              {/* without a graded day the meaning above says it already */}
              {g.graded_days > 0 ? (
                <p className="text-caption text-fg-2">{dayCountsText(g)}</p>
              ) : null}
            </>
          )}
        </>
      ) : (
        <p className="text-small text-fg-2">
          The grade shows when the reviewers' view has one for this range.
        </p>
      )}
    </div>
  );
}

const KPI_ROWS: { key: KpiKey; label: string; kind: "count" | "rate" }[] = [
  { key: "visitors", label: "Visitors", kind: "count" },
  { key: "pageviews", label: "Page views", kind: "count" },
  { key: "requests", label: "API requests", kind: "count" },
  { key: "error_rate", label: "API 5xx rate", kind: "rate" },
  { key: "onchain_operations", label: "On-chain operations", kind: "count" },
];

type KpiRow = (typeof KPI_ROWS)[number] & { k: Kpi };

function kpiText(v: number | null, kind: "count" | "rate"): ReactNode {
  if (v === null) return <Missing reason="No data in this range" />;
  return kind === "rate" ? formatPercent(v) : formatCount(v);
}

// the change against the previous range and the place in the cohort,
// exactly as the operator lists compute them (contract 19.2 and 19.3). a
// percentile needs a cohort of five projects.
function CohortPlace({ v }: { v: SCFView }) {
  const c = v.comparison;
  if (!c) return null;
  const pc = v.percentile_cohort;
  const population =
    pc && (pc.program || pc.round)
      ? `the projects confirmed in ${[pc.program, pc.round].filter(Boolean).join(", ")}`
      : "every listed project, because no cohort is confirmed for yours";
  const hidden = percentileHiddenReason(c.cohort_size);
  // on-chain operations follow the one missing-value rule of the figures:
  // without a verified id neither range is measured
  const chain = v.onchain;
  const chainNow = chain ? chainGap(chain.ids_verified, v.onchain_series) : null;
  const chainBefore = chain && chain.ids_verified === 0 ? NO_VERIFIED_IDS : null;
  const gapOf = (r: KpiRow, gap: string | null) => (r.key === "onchain_operations" ? gap : null);
  const columns: Column<KpiRow>[] = [
    { key: "metric", header: "Metric", primary: true, cell: (r) => r.label },
    {
      key: "value",
      header: "This range",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) => {
        const gap = gapOf(r, chainNow);
        return gap ? <Missing reason={gap} /> : kpiText(r.k.value, r.kind);
      },
    },
    {
      key: "previous",
      header: "Previous range",
      align: "right",
      numeric: true,
      cell: (r) => {
        const gap = gapOf(r, chainBefore);
        return gap ? <Missing reason={gap} /> : kpiText(r.k.previous, r.kind);
      },
    },
    {
      key: "change",
      header: "Change",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) =>
        r.k.change === null ? (
          <Missing reason="No value in the previous range" />
        ) : r.kind === "rate" ? (
          formatPoints(r.k.change)
        ) : (
          formatChange(r.k.change)
        ),
    },
    {
      key: "percentile",
      header: "Percentile",
      definition: "The share of the cohort with a value at or below yours.",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) =>
        hidden ? (
          <Missing reason={hidden} />
        ) : r.k.percentile === null ? (
          <Missing reason="No value in this range" />
        ) : (
          formatCohortPercentile(r.k.percentile)
        ),
    },
  ];
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h3 className="text-title text-fg">Change and place in the cohort</h3>
        <p className="max-w-prose text-small text-fg-2" data-testid="cohort-population">
          Each metric against the previous range of the same length. The cohort is {population}:{" "}
          {countOf(c.cohort_size, "project")}, active or suspended.
        </p>
      </div>
      <DataTable<KpiRow>
        label="Key metrics"
        columns={columns}
        rows={KPI_ROWS.map((r) => ({ ...r, k: c.kpis[r.key] }))}
        getRowKey={(r) => r.key}
        rowLabel={(r) => r.label}
        rowProps={(r) => ({ "data-kpi": r.key })}
      />
      {hidden ? (
        <p className="text-caption text-fg-2" data-testid="percentile-hidden">
          Percentiles show from a cohort of 5 projects. Yours has{" "}
          {countOf(c.cohort_size, "project")}, so one project more or less moves them too far.
        </p>
      ) : null}
    </div>
  );
}

// ---- the integrity report, with its appeal

function IntegritySection({ v }: { v: SCFView }) {
  const { tenant } = useProject();
  const report = useQuery(integrityQuery("tenant", tenant.id, { from: v.from, to: v.to }));
  const sites = useQuery(sitesQuery(tenant.id));
  const names = Object.fromEntries((sites.data ?? []).map((s) => [s.id, s.domain]));
  let body: ReactNode;
  if (report.isPending)
    body = (
      <LoadingState label="Loading the integrity report">
        <Skeleton.Table rows={3} columns={3} />
      </LoadingState>
    );
  else if (report.error)
    body = (
      <ErrorState
        title="The integrity report did not load"
        error={report.error}
        onRetry={() => report.refetch()}
      />
    );
  else
    body = (
      <IntegrityPanel
        title="Integrity report"
        report={report.data}
        siteNames={names}
        appeal={sites.data ? { slug: tenant.slug, siteDomains: names } : undefined}
      />
    );
  return (
    <section aria-label="Integrity" className="flex min-w-0 flex-col gap-4">
      <h2 className="text-section text-fg">Integrity</h2>
      {body}
    </section>
  );
}

// ---- the operators' saved rules the project meets today

const METRIC_TEXT: Record<string, string> = {
  visitors: "Verified visitors",
  requests: "API requests",
  onchain_operations: "On-chain operations",
};

function RulesMet({ v }: { v: SCFView }) {
  return (
    <section aria-label="Rules you meet" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-section text-fg">Rules you meet</h2>
        <p className="max-w-prose text-small text-fg-2">
          The traction rules that SCF operators saved and that your project meets today.
        </p>
      </div>
      {v.rules_met.length === 0 ? (
        <EmptyState compact title="No rule met today">
          A rule counts the days of complete calendar months. Your project meets none of the rules
          now.
        </EmptyState>
      ) : (
        <ul className="grid gap-3 md:grid-cols-2">
          {v.rules_met.map(({ rule, match }) => (
            <li
              key={rule.id}
              data-rule={rule.id}
              className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4 sm:p-5"
            >
              <span className="text-body font-medium text-fg">{rule.name}</span>
              <span className="text-small text-fg-2">
                {METRIC_TEXT[rule.metric] ?? rule.metric}: {formatCount(rule.min_per_day)} or more a
                day, on {countOf(rule.min_days, "day")} or more in each of the last{" "}
                {countOf(rule.months, "month")}
                {rule.min_grade ? `. Integrity grade ${rule.min_grade} or better` : ""}.
              </span>
              <ul className="flex flex-wrap gap-x-4 gap-y-1 text-caption text-fg-2">
                {match.months.map((m) => (
                  <li key={m.month}>
                    {formatTick(`${m.month}-01`, "month")}:{" "}
                    {countOf(m.qualifying_days, "qualifying day")}
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---- the figures of the project's rows in the operator views

const DEFINED =
  "rounded-sm underline decoration-line-strong decoration-dotted underline-offset-2 focus-ring";

function Figure({
  label,
  definition,
  children,
  name,
  text = false,
}: {
  label: string;
  definition?: string;
  children: ReactNode;
  name?: string;
  // a word or a badge, not a number: set in the sans face
  text?: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1" data-figure={name}>
      <dt className="text-caption text-fg-2">
        {definition ? (
          <Tooltip content={definition}>
            {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
            <span tabIndex={0} className={DEFINED}>
              {label}
              <span className="sr-only">. {definition}</span>
            </span>
          </Tooltip>
        ) : (
          label
        )}
      </dt>
      <dd className={cn("min-w-0 text-body text-fg", text ? "font-sans" : "font-mono tabular")}>
        {children}
      </dd>
    </div>
  );
}

function FigureCard({
  title,
  meta,
  children,
}: {
  title: string;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section
      aria-label={title}
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-title text-fg">{title}</h3>
        {meta}
      </div>
      {children}
    </section>
  );
}

function share(v: number | null, why: string): ReactNode {
  return v === null ? <Missing reason={why} /> : formatPercent(v);
}

// one definition per label: "Distinct routes" is not the route rows of the
// api page, which list each method of a route
const DISTINCT_ROUTES =
  "Distinct service and route pairs with application requests. A route with two methods counts once. A route that only health checks reached does not count.";

function Figures({ v }: { v: SCFView }) {
  const web = v.web;
  const api = v.api;
  const chain = v.onchain;
  const chainMissing = chain ? chainGap(chain.ids_verified, v.onchain_series) : null;
  const award = v.comparison?.cohort.award_date ?? null;
  const marker = award ? { day: award, label: `Award date ${formatDate(award)}` } : null;
  const awardInRange = marker !== null && v.days.includes(marker.day);
  const notListed = <Missing reason="Not listed in the operator views" />;
  return (
    <section aria-label="Figures" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-section text-fg">Figures</h2>
        <p className="max-w-prose text-small text-fg-2">
          Your project's rows in the operator views, over the same range.
        </p>
      </div>
      <div className="grid min-w-0 gap-3 lg:grid-cols-2">
        <FigureCard title="Project">
          <dl className="grid grid-cols-2 gap-4">
            <Figure label="Status" text>
              {v.tenant ? (
                <StatusBadge kind="project" status={v.tenant.status as TenantStatus} />
              ) : (
                notListed
              )}
            </Figure>
            <Figure label="Sites">{v.sites ? formatCount(v.sites.sites) : notListed}</Figure>
            <Figure label="Verified sites">
              {v.sites ? formatCount(v.sites.sites_verified) : notListed}
            </Figure>
            <Figure label="Sites with stellar.toml">
              {v.sites ? formatCount(v.sites.sites_with_stellar_toml) : notListed}
            </Figure>
          </dl>
        </FigureCard>
        <FigureCard title="API">
          {api ? (
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
              <Figure label="Requests">{formatCount(api.requests)}</Figure>
              <Figure label="4xx rate">{share(api.rate_4xx, "No API requests")}</Figure>
              <Figure label="5xx rate">{share(api.rate_5xx, "No API requests")}</Figure>
              <Figure label="p95 latency">
                {api.p95_over_10s ? (
                  OPEN_BUCKET
                ) : api.p95_seconds === null ? (
                  <Missing reason="No API requests" />
                ) : (
                  formatLatency(api.p95_seconds)
                )}
              </Figure>
              <Figure label="Services">{formatCount(api.services)}</Figure>
              <Figure label="Distinct routes" definition={DISTINCT_ROUTES} name="routes">
                {formatCount(api.routes)}
              </Figure>
            </dl>
          ) : (
            <p className="text-small text-fg-2">No API data in this range.</p>
          )}
        </FigureCard>
      </div>
      <FigureCard title="Web">
        {web ? (
          <>
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Figure label="Page views">{formatCount(web.pageviews)}</Figure>
              <Figure label="Visitors">{formatCount(web.visitors)}</Figure>
              <Figure label="Visits">{formatCount(web.visits)}</Figure>
              <Figure label="Bounce rate">{share(web.bounce_rate, "No visits")}</Figure>
              <Figure label="Average visit">
                {web.avg_visit_duration === null ? (
                  <Missing reason="No visits" />
                ) : (
                  formatDuration(web.avg_visit_duration)
                )}
              </Figure>
              <Figure
                label="Quarantined share"
                definition="The share of page views held apart because they look automated."
              >
                {share(web.quarantine_share, "No page views")}
              </Figure>
              <Figure
                label="Gateway requests"
                definition="Events that reached the Veristar edge, counted or not."
              >
                {formatCount(web.ingest_requests)}
              </Figure>
              <Figure
                label="Refused share"
                definition="The share of gateway requests the edge refused."
              >
                {share(web.rejected_share, "No gateway requests")}
              </Figure>
            </dl>
            <div className="flex flex-col gap-1">
              <span className="text-caption text-fg-2">
                Daily page views, UTC days
                {awardInRange && award ? (
                  <span data-award>. The dashed line is your award date, {formatDate(award)}.</span>
                ) : null}
              </span>
              <DayStrip
                marker={marker}
                label="Daily page views"
                points={v.web_series.map((p) => ({
                  day: p.day,
                  value: p.aggregated ? p.pageviews : null,
                  missing: p.aggregated ? null : "not_read_yet",
                }))}
              />
            </div>
          </>
        ) : (
          <p className="text-small text-fg-2">No web data in this range.</p>
        )}
      </FigureCard>
      <FigureCard
        title="On-chain"
        meta={v.link_two_way ? <Pill tone="success">stellar.toml linked both ways</Pill> : null}
      >
        {chain ? (
          <>
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Figure label="Operations">
                {chainMissing ? <Missing reason={chainMissing} /> : formatCount(chain.operations)}
              </Figure>
              <Figure label="Transactions">
                {chainMissing ? <Missing reason={chainMissing} /> : formatCount(chain.transactions)}
              </Figure>
              <Figure label="Verified ids">
                {formatCount(chain.ids_verified)} of {formatCount(chain.ids)}
              </Figure>
              <Figure label="Days without data" definition={DAYS_WITHOUT_DATA} name="days-missing">
                {chain.ids_verified === 0 ? (
                  <Missing reason={NO_VERIFIED_IDS} />
                ) : (
                  formatCount(chain.days_missing)
                )}
              </Figure>
            </dl>
            <div className="flex flex-col gap-1">
              <span className="text-caption text-fg-2">Weekly active accounts</span>
              <span className="text-body text-fg">
                {weeklyActiveText(chain.weekly_active_accounts)}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-caption text-fg-2">Daily operations, UTC days</span>
              <DayStrip
                marker={marker}
                label="Daily operations"
                points={v.onchain_series.map((p) => ({
                  day: p.day,
                  value: p.operations,
                  missing: p.operations === null ? "not_read_yet" : null,
                }))}
              />
            </div>
            {v.contract_functions.length ? (
              <div className="flex min-w-0 flex-col gap-4">
                <h4 className="text-body font-medium text-fg">Contract calls</h4>
                {v.contract_functions.map((c) => (
                  <div
                    key={c.strkey}
                    className="flex min-w-0 flex-col gap-2"
                    data-contract={c.strkey}
                  >
                    <div className="flex flex-wrap items-baseline justify-between gap-2">
                      <code className="font-mono text-small text-fg" title={c.strkey}>
                        {shortKey(c.strkey)}
                      </code>
                      <span className="text-caption text-fg-2">
                        {functionsDaysText(c.functions)}
                      </span>
                    </div>
                    <FunctionsTable
                      summary={c.functions}
                      label={`Calls per function of ${c.strkey}`}
                    />
                  </div>
                ))}
              </div>
            ) : null}
          </>
        ) : (
          <p className="text-small text-fg-2">No accounts or contracts declared.</p>
        )}
      </FigureCard>
    </section>
  );
}

// ---- signed reports of the same figures

const CONTAINS = [
  "Web: visitors, page views, visits and bounce rate",
  "API: requests, error rates and p95 latency",
  "On-chain: operations, weekly active accounts and calls per function",
  "The integrity grade, the cohort, the percentiles and the rules met",
  "The Veristar signature, which anyone can check",
];

export function SignedReports() {
  const { tenant } = useProject();
  const list = useQuery(reportsQuery(tenant.id));
  const queryClient = useQueryClient();
  const initial = closedDays(30, new Date());
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  const [revoking, setRevoking] = useState<ReportSummary | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: reportKeysOf.list(tenant.id) });
  const create = useMutation({
    mutationFn: () => reportApi.create(tenant.id, { from, to }),
    onSuccess: async (r) => {
      toast.success(`Report for ${formatDateRange(r.from, r.to)} signed.`);
      await refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => reportApi.revoke(tenant.id, id),
    onSuccess: async (r) => {
      setRevoking(null);
      toast.success(`The link of the report for ${formatDateRange(r.from, r.to)} is revoked.`);
      await refresh();
    },
  });
  const download = useMutation({
    mutationFn: async (r: ReportSummary) => {
      const d = await reportApi.get(tenant.id, r.id);
      downloadFile(d.file, `veristar-report-${tenant.slug}-${r.from}-${r.to}.json`);
    },
    onError: (e) => toast.error(`The JSON file did not download. ${errorMessage(e)}`),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validatePeriod(from, to, new Date());
    setErrors(found);
    if (!found.from && !found.to) create.mutate();
  }

  return (
    <section aria-labelledby="signed-reports-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="signed-reports-h" className="text-section text-fg">
          Signed reports
        </h2>
        <p className="max-w-prose text-small text-fg-2">
          A signed report holds these figures for closed UTC days. Share its link in your SCF
          submission. Anyone can check the signature without an account.
        </p>
      </div>
      <div className="grid min-w-0 gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,20rem)]">
        <section
          aria-label="Generate a report"
          className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5"
        >
          <h3 className="text-title text-fg">Generate a report</h3>
          <form onSubmit={submit} noValidate className="flex flex-col gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <DateField
                label="From"
                value={from}
                onChange={setFrom}
                error={errors.from}
                hint="UTC day"
              />
              <DateField
                label="To"
                value={to}
                onChange={setTo}
                error={errors.to}
                hint={`UTC day, ${lastFinalDay(new Date())} or earlier`}
              />
            </div>
            <p className="text-caption text-fg-2">
              A report covers at most {MAX_REPORT_DAYS} days. The last 4 days can still change, so a
              report ends before them.
            </p>
            <FormMessage error={create.error} />
            {create.data ? (
              <div
                role="status"
                className="flex flex-col gap-2 rounded-md border border-line bg-sunken p-3 text-small text-fg"
              >
                <span>Report generated. Share this link:</span>
                <span className="flex min-w-0 flex-wrap items-center gap-2">
                  <a
                    href={create.data.url}
                    className="min-w-0 break-all rounded-sm font-mono text-accent-text underline-offset-4 hover:underline focus-ring"
                  >
                    {create.data.url}
                  </a>
                  <CopyButton text={create.data.url} label="Copy link" size="xs" />
                </span>
              </div>
            ) : null}
            <div>
              <Button
                type="submit"
                pending={create.isPending}
                className="max-sm:w-full max-lg:h-(--vs-control-touch)"
              >
                <FileSignature aria-hidden />
                {create.isPending ? "Signing" : "Generate and sign"}
              </Button>
            </div>
          </form>
        </section>
        <section
          aria-label="What a report contains"
          className="flex min-w-0 flex-col gap-3 rounded-lg border border-line p-4 sm:p-5"
        >
          <h3 className="text-caption text-fg-2">What a report contains</h3>
          <ul className="flex flex-col gap-2 text-small text-fg-2">
            {CONTAINS.map((t) => (
              <li key={t} className="flex gap-2">
                <span aria-hidden className="mt-2 size-1 shrink-0 rounded-full bg-fg-3" />
                {t}
              </li>
            ))}
          </ul>
          <p className="text-caption text-fg-2">
            Daily and period totals only. No visitor, address or user agent.
          </p>
        </section>
      </div>

      <section aria-label="Your reports" className="flex min-w-0 flex-col gap-3">
        <h3 className="text-title text-fg">Your reports</h3>
        {list.isPending ? (
          <LoadingState label="Loading the reports">
            <Skeleton.Table rows={2} columns={3} />
          </LoadingState>
        ) : list.error ? (
          <ErrorState
            compact
            title="The reports did not load"
            error={list.error}
            onRetry={() => list.refetch()}
          />
        ) : list.data.items.length === 0 ? (
          <EmptyState compact icon={<FileSignature aria-hidden />} title="No report yet">
            Generate a report, then share its link in your SCF submission.
          </EmptyState>
        ) : (
          <ul
            className="flex flex-col divide-y divide-line rounded-lg border border-line bg-raised"
            aria-label="Reports"
          >
            {list.data.items.map((r) => (
              <ReportRow
                key={r.id}
                r={r}
                downloading={download.isPending && download.variables?.id === r.id}
                onDownload={() => download.mutate(r)}
                onRevoke={() => {
                  revoke.reset();
                  setRevoking(r);
                }}
              />
            ))}
          </ul>
        )}
      </section>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            // a refusal shown for one report must not greet the next
            revoke.reset();
          }
        }}
        title="Revoke this link?"
        description="People who open the link see that the report is not available. A downloaded JSON file keeps its valid signature. You cannot restore the link."
        confirmLabel="Revoke link"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </section>
  );
}

// one report: its days, when it was made and how often its link was read,
// one main action and the rest in a menu
function ReportRow({
  r,
  downloading,
  onDownload,
  onRevoke,
}: {
  r: ReportSummary;
  downloading: boolean;
  onDownload: () => void;
  onRevoke: () => void;
}) {
  const of = `the report for ${formatDateRange(r.from, r.to)}`;
  const revoked = r.revoked_at !== null;
  return (
    <li
      data-report={r.id}
      className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 sm:px-5"
    >
      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-body font-medium text-fg">{formatDateRange(r.from, r.to)}</span>
          {revoked ? <Pill tone="info">Link revoked</Pill> : null}
        </span>
        <span className="text-caption text-fg-2">
          Generated {formatDateTimeUtc(r.created_at)}, {countOf(r.view_count, "view")}
        </span>
      </span>
      <span className="flex items-center gap-1">
        {revoked ? (
          <Button
            size="sm"
            variant="outline"
            onClick={onDownload}
            pending={downloading}
            aria-label={`Download JSON of ${of}`}
            className="max-lg:h-(--vs-control-touch)"
          >
            <Download aria-hidden />
            Download JSON
          </Button>
        ) : (
          <>
            <CopyButton text={r.url} label="Copy link" accessibleLabel={`Copy link of ${of}`} />
            <Menu>
              <MenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Actions for ${of}`}
                  className="max-lg:size-(--vs-control-touch)"
                >
                  <MoreHorizontal aria-hidden />
                </Button>
              </MenuTrigger>
              <MenuContent align="end">
                <MenuItem asChild>
                  <a href={r.url} target="_blank" rel="noopener">
                    <ExternalLink aria-hidden />
                    Open the report
                  </a>
                </MenuItem>
                <MenuItem onSelect={onDownload}>
                  <Download aria-hidden />
                  Download JSON
                </MenuItem>
                <MenuSeparator />
                <MenuItem tone="critical" onSelect={onRevoke}>
                  Revoke link
                </MenuItem>
              </MenuContent>
            </Menu>
          </>
        )}
      </span>
    </li>
  );
}
