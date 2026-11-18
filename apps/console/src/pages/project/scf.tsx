// scf review: what scf reviewers see about the project, read through the
// operator views' own queries for the same range (contract 16.3), and the
// signed reports that carry the same figures to anyone (contract 20). the
// standing comes first, then the full integrity report with its appeal,
// the rules met, the figures, and the reports.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Ban, Check, Download, ExternalLink, FileSignature, MoreHorizontal } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import type { Kpi, KpiKey } from "@/api/cohorts";
import { exportPaths, type SCFView, scfViewQuery } from "@/api/onchain";
import { integrityQuery, sitesQuery } from "@/api/queries";
import { type ReportSummary, reportApi, reportKeysOf, reportsQuery } from "@/api/reports";
import type { IntegrityGrade } from "@/api/schemas";
import { OwnCohortPanel, percentileHiddenReason } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { type Column, DataTable } from "@/components/data-table";
import { Disclosure } from "@/components/disclosure";
import { FormMessage } from "@/components/error-panel";
import { GradeBadge, IntegrityPanel } from "@/components/integrity";
import { DayStrip } from "@/components/onchain";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PageHeader } from "@/components/page-header";
import { DateField } from "@/components/range-control";
import { Sparkline, StatCard, type StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
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
import { chainGap, DAYS_WITHOUT_DATA, shortKey, weeklyActiveText } from "@/lib/onchain";
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
  const [signing, setSigning] = useState(false);

  const header = (
    <PageHeader
      title={PAGE_HEADERS.scf.title}
      description={v ? `${formatDateRange(v.from, v.to)}, UTC` : undefined}
      primary={
        <Button onClick={() => setSigning(true)}>
          <FileSignature aria-hidden />
          Sign a report
        </Button>
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
  else if (!v.in_operator_views)
    body = (
      <>
        <EmptyState title="Not in the SCF views">
          This project is internal to the platform. SCF reviewers do not see it.
        </EmptyState>
        <OwnCohortPanel tenantId={tenant.id} record={v.cohort} isOwner={isOwner} />
      </>
    );
  else
    body = (
      <>
        <Verdict v={v} tenantId={tenant.id} />
        <OnDemand v={v} tenantId={tenant.id} isOwner={isOwner} />
      </>
    );

  return (
    <div className="flex min-w-0 flex-col gap-8">
      {header}
      {body}
      <SignedReports open={signing} onOpenChange={setSigning} />
    </div>
  );
}

// ---- the verdict: the grade and what lowered it, the cohort, and the
// three numbers that carry the project, each with its change

type KeyNumber = {
  key: KpiKey;
  label: string;
  value: number;
  change: number | null;
  percentile: number | null;
  spark: number[];
};

const KEY_ORDER: { key: KpiKey; label: string }[] = [
  { key: "visitors", label: "Visitors" },
  { key: "requests", label: "API requests" },
  { key: "onchain_operations", label: "On-chain operations" },
  { key: "pageviews", label: "Page views" },
];

// the first three counts the project has data for, in the order a funder
// reads them; a count of zero or one not measured is left out, not dashed
export function keyNumbers(v: SCFView): KeyNumber[] {
  const chainOk = v.onchain !== null && chainGap(v.onchain.ids_verified, v.onchain_series) === null;
  const own: Record<string, number | null> = {
    visitors: v.web?.visitors ?? null,
    pageviews: v.web?.pageviews ?? null,
    requests: v.api?.requests ?? null,
    onchain_operations: chainOk ? (v.onchain?.operations ?? null) : null,
  };
  const spark: Record<string, number[]> = {
    visitors: v.web_series.map((p) => p.visitors ?? 0),
    pageviews: v.web_series.map((p) => p.pageviews ?? 0),
    requests: v.api_series.map((p) => p.requests),
    onchain_operations: v.onchain_series.map((p) => p.operations ?? 0),
  };
  const out: KeyNumber[] = [];
  for (const k of KEY_ORDER) {
    if (k.key === "onchain_operations" && !chainOk) continue;
    const kpi = v.comparison?.kpis[k.key];
    const value = kpi?.value ?? own[k.key] ?? null;
    if (value === null || value <= 0) continue;
    out.push({
      ...k,
      value,
      change: kpi?.change ?? null,
      percentile: kpi?.percentile ?? null,
      spark: spark[k.key] ?? [],
    });
    if (out.length === 3) break;
  }
  return out;
}

function changeOf(r: number | null): StatChange | null {
  if (r === null) return null;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period: "previous range",
  };
}

// a spark only when it has a shape
function shaped(values: number[]): number[] | null {
  if (values.length < 2) return null;
  return Math.max(...values) === Math.min(...values) ? null : values;
}

function Verdict({ v, tenantId }: { v: SCFView; tenantId: string }) {
  const keys = keyNumbers(v);
  const c = v.comparison;
  const hidden = c ? percentileHiddenReason(c.cohort_size) : null;
  const cohort = v.cohort.confirmed;
  const graded = v.integrity && (v.sites?.sites ?? 0) > 0;
  return (
    <section aria-label="Standing" data-testid="verdict" className="flex min-w-0 flex-col gap-3">
      {cohort || v.rules_met.length > 0 ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-small">
          {cohort ? (
            <span className="flex flex-wrap items-baseline gap-x-2" data-cohort="">
              <span className="text-fg">
                {[cohort.program, cohort.round, cohort.category].filter(Boolean).join(", ")}
              </span>
              {c ? (
                <span className="text-fg-2 tabular">{countOf(c.cohort_size, "project")}</span>
              ) : null}
            </span>
          ) : null}
          {v.rules_met.map(({ rule }) => (
            <span key={rule.id} data-rule-met={rule.id}>
              <Pill tone="success">
                <Check aria-hidden className="size-3" />
                {rule.name}
              </Pill>
            </span>
          ))}
        </div>
      ) : null}
      <div className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {graded ? <GradeCard v={v} tenantId={tenantId} /> : null}
        {keys.map((k) => (
          <StatCard
            key={k.key}
            data-key-number={k.key}
            label={k.label}
            value={k.value}
            change={changeOf(k.change)}
            spark={
              shaped(k.spark) ? (
                <Sparkline values={shaped(k.spark) as number[]} label={`${k.label} per day`} />
              ) : undefined
            }
            footer={
              !hidden && k.percentile !== null ? (
                <span className="text-caption text-fg-2 tabular" data-percentile="">
                  {formatCohortPercentile(k.percentile)} in cohort
                </span>
              ) : null
            }
          />
        ))}
      </div>
    </section>
  );
}

const GRADE_TONE: Record<IntegrityGrade, string> = {
  A: "text-success",
  B: "text-warning",
  C: "text-critical",
};

// the grade as the focal number, and under it the reasons that lowered
// it, each with its days; a grade nothing lowered shows its day counts
function GradeCard({ v, tenantId }: { v: SCFView; tenantId: string }) {
  const g = v.integrity;
  const report = useQuery(integrityQuery("tenant", tenantId, { from: v.from, to: v.to }));
  const reasons = report.data?.reasons.filter((r) => r.level !== "none") ?? [];
  if (!g) return null;
  return (
    <div
      data-testid="standing-grade"
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4"
    >
      <span className="text-caption text-fg-2">Integrity grade</span>
      {g.grade ? (
        <span className={cn("text-metric tabular", GRADE_TONE[g.grade])} data-grade={g.grade}>
          {g.grade}
        </span>
      ) : (
        <GradeBadge grade={null} className="w-fit" />
      )}
      {reasons.length > 0 ? (
        <ul className="flex flex-col gap-1" aria-label="What lowered the grade">
          {reasons.map((r) => (
            <li
              key={r.code}
              data-reason={r.code}
              className="flex items-baseline gap-2 text-caption text-fg-2"
            >
              <span className={cn("tabular", r.level === "C" ? "text-critical" : "text-warning")}>
                {r.level}
              </span>
              <span className="min-w-0 flex-1 text-fg">{r.text}</span>
              <span className="tabular">{countOf(r.days, "day")}</span>
            </li>
          ))}
        </ul>
      ) : g.graded_days > 0 ? (
        <span className="text-caption text-fg-2 tabular" data-day-counts="">
          {formatCount(g.days_a)} A · {formatCount(g.days_b)} B · {formatCount(g.days_c)} C
        </span>
      ) : null}
    </div>
  );
}

// ---- everything else, on demand: one line each until opened

function OnDemand({ v, tenantId, isOwner }: { v: SCFView; tenantId: string; isOwner: boolean }) {
  return (
    <div className="flex min-w-0 flex-col divide-y divide-line border-y border-line">
      {(v.sites?.sites ?? 0) > 0 ? (
        <Detail id="integrity" summary="Integrity by site">
          <IntegritySection v={v} />
        </Detail>
      ) : null}
      <Detail id="cohort" summary="Cohort">
        <div className="flex min-w-0 flex-col gap-4">
          <OwnCohortPanel tenantId={tenantId} record={v.cohort} isOwner={isOwner} />
          <CohortPlace v={v} />
          <RulesMet v={v} />
        </div>
      </Detail>
      <Detail id="figures" summary="Figures">
        <Figures v={v} />
      </Detail>
    </div>
  );
}

function Detail({ id, summary, children }: { id: string; summary: string; children: ReactNode }) {
  return (
    <Disclosure
      id={id}
      variant="row"
      summary={summary}
      summaryClassName="py-3 text-body font-medium"
      bodyClassName="pb-5 pl-0"
    >
      {children}
    </Disclosure>
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
// exactly as the operator lists compute them (contract 19.2 and 19.3). a row
// without data is left out, and a column no row fills is not drawn.
function CohortPlace({ v }: { v: SCFView }) {
  const c = v.comparison;
  if (!c) return null;
  const pc = v.percentile_cohort;
  const population =
    pc && (pc.program || pc.round)
      ? [pc.program, pc.round].filter(Boolean).join(", ")
      : "Every listed project";
  const hidden = percentileHiddenReason(c.cohort_size);
  const chain = v.onchain;
  const chainOk = chain !== null && chainGap(chain.ids_verified, v.onchain_series) === null;
  const rows = KPI_ROWS.map((r) => ({ ...r, k: c.kpis[r.key] })).filter(
    (r) =>
      (r.key !== "onchain_operations" || chainOk) &&
      (r.k.value !== null || r.k.previous !== null) &&
      !(r.kind === "count" && !r.k.value && !r.k.previous),
  );
  if (rows.length === 0) return null;
  const anyChange = rows.some((r) => r.k.change !== null);
  const anyPercentile = !hidden && rows.some((r) => r.k.percentile !== null);
  const columns: Column<KpiRow>[] = [
    { key: "metric", header: "Metric", primary: true, cell: (r) => r.label },
    {
      key: "value",
      header: "This range",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) => kpiText(r.k.value, r.kind),
    },
    {
      key: "previous",
      header: "Previous range",
      align: "right",
      numeric: true,
      cell: (r) => kpiText(r.k.previous, r.kind),
    },
  ];
  if (anyChange)
    columns.push({
      key: "change",
      header: "Change",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) =>
        r.k.change === null
          ? null
          : r.kind === "rate"
            ? formatPoints(r.k.change)
            : formatChange(r.k.change),
    });
  if (anyPercentile)
    columns.push({
      key: "percentile",
      header: "Percentile",
      definition: "The share of the cohort with a value at or below yours.",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) => (r.k.percentile === null ? null : formatCohortPercentile(r.k.percentile)),
    });
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <p className="text-small text-fg-2" data-testid="cohort-population">
        <span className="text-fg">{population}</span> · {countOf(c.cohort_size, "project")}
      </p>
      <DataTable<KpiRow>
        label="Key metrics"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.key}
        rowLabel={(r) => r.label}
        rowProps={(r) => ({ "data-kpi": r.key })}
      />
    </div>
  );
}

// ---- the integrity report, with its appeal

function IntegritySection({ v }: { v: SCFView }) {
  const { tenant } = useProject();
  const report = useQuery(integrityQuery("tenant", tenant.id, { from: v.from, to: v.to }));
  const sites = useQuery(sitesQuery(tenant.id));
  const names = Object.fromEntries((sites.data ?? []).map((s) => [s.id, s.domain]));
  if (report.isPending)
    return (
      <LoadingState label="Loading the integrity report">
        <Skeleton.Table rows={3} columns={3} />
      </LoadingState>
    );
  if (report.error)
    return (
      <ErrorState
        title="The integrity report did not load"
        error={report.error}
        onRetry={() => report.refetch()}
      />
    );
  return (
    <IntegrityPanel
      title="Integrity report"
      report={report.data}
      siteNames={names}
      appeal={sites.data ? { slug: tenant.slug, siteDomains: names } : undefined}
    />
  );
}

// ---- the operators' saved rules the project meets today

const METRIC_TEXT: Record<string, string> = {
  visitors: "Verified visitors",
  requests: "API requests",
  onchain_operations: "On-chain operations",
};

function RulesMet({ v }: { v: SCFView }) {
  if (v.rules_met.length === 0) return null;
  return (
    <ul
      aria-label="Rules you meet"
      className="flex flex-col divide-y divide-line-subtle rounded-lg border border-line bg-raised px-4 sm:px-5"
    >
      {v.rules_met.map(({ rule, match }) => (
        <li key={rule.id} data-rule={rule.id} className="flex min-w-0 flex-col gap-1 py-3">
          <span className="flex flex-wrap items-baseline justify-between gap-x-3">
            <span className="text-body font-medium text-fg">{rule.name}</span>
            <span className="text-caption text-fg-2">
              {METRIC_TEXT[rule.metric] ?? rule.metric} ≥ {formatCount(rule.min_per_day)} a day,{" "}
              {countOf(rule.min_days, "day")} × {countOf(rule.months, "month")}
              {rule.min_grade ? `, grade ${rule.min_grade}` : ""}
            </span>
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
  );
}

// ---- the figures of the project's rows in the operator views, past the
// key numbers above; a figure with no value is left out

const DEFINED =
  "rounded-sm underline decoration-line-strong decoration-dotted underline-offset-2 focus-ring";

type Fig = { label: string; value: ReactNode; definition?: string; name?: string } | null;

function FigureList({ items }: { items: Fig[] }) {
  const shown = items.filter((f): f is NonNullable<Fig> => f !== null);
  if (shown.length === 0) return null;
  return (
    <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
      {shown.map((f) => (
        <div key={f.label} className="flex min-w-0 flex-col gap-1" data-figure={f.name}>
          <dt className="text-caption text-fg-2">
            {f.definition ? (
              <Tooltip content={f.definition}>
                {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
                <span tabIndex={0} className={DEFINED}>
                  {f.label}
                  <span className="sr-only">. {f.definition}</span>
                </span>
              </Tooltip>
            ) : (
              f.label
            )}
          </dt>
          <dd className="min-w-0 text-body text-fg tabular">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function FigureGroup({
  title,
  meta,
  children,
}: {
  title: string;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="flex min-w-0 flex-col gap-3 py-4 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-caption text-fg-2">{title}</h3>
        {meta}
      </div>
      {children}
    </section>
  );
}

// one definition per label: "Distinct routes" is not the route rows of the
// api page, which list each method of a route
const DISTINCT_ROUTES =
  "Distinct service and route pairs with application requests. A route with two methods counts once. A route that only health checks reached does not count.";

const fig = (label: string, value: ReactNode | null, more?: Partial<NonNullable<Fig>>): Fig =>
  value === null || value === undefined ? null : { label, value, ...more };
const pct = (v: number | null) => (v === null ? null : formatPercent(v));

function Figures({ v }: { v: SCFView }) {
  const web = v.web;
  const api = v.api;
  const chain = v.onchain;
  const shown = new Set(keyNumbers(v).map((k) => k.key));
  const award = v.comparison?.cohort.award_date ?? null;
  const marker = award ? { day: award, label: `Award date ${formatDate(award)}` } : null;
  const awardInRange = marker !== null && v.days.includes(marker.day);
  const chainOk = chain !== null && chain.ids_verified > 0;
  const webDays = v.web_series.some((p) => p.aggregated && (p.pageviews ?? 0) > 0);
  const chainDays = v.onchain_series.some((p) => (p.operations ?? 0) > 0);
  return (
    <div className="flex min-w-0 flex-col divide-y divide-line-subtle">
      {v.sites && v.sites.sites > 0 ? (
        <FigureGroup title="Sites">
          <FigureList
            items={[
              fig("Sites", formatCount(v.sites.sites)),
              fig("Verified sites", formatCount(v.sites.sites_verified)),
              fig("Sites with stellar.toml", formatCount(v.sites.sites_with_stellar_toml)),
            ]}
          />
        </FigureGroup>
      ) : null}
      {web && v.has_web_data ? (
        <FigureGroup title="Web">
          <FigureList
            items={[
              shown.has("pageviews") ? null : fig("Page views", formatCount(web.pageviews)),
              shown.has("visitors") ? null : fig("Visitors", formatCount(web.visitors)),
              fig("Visits", formatCount(web.visits)),
              fig("Bounce rate", pct(web.bounce_rate)),
              fig(
                "Average visit",
                web.avg_visit_duration === null ? null : formatDuration(web.avg_visit_duration),
              ),
              fig("Quarantined share", pct(web.quarantine_share), {
                definition: "The share of page views held apart because they look automated.",
              }),
              fig("Gateway requests", formatCount(web.ingest_requests), {
                definition: "Events that reached the Veristar edge, counted or not.",
              }),
              fig("Refused share", pct(web.rejected_share), {
                definition: "The share of gateway requests the edge refused.",
              }),
            ]}
          />
          {webDays ? (
            <div className="flex flex-col gap-1">
              <span className="text-caption text-fg-2">
                Daily page views, UTC days
                {awardInRange && award ? (
                  <span data-award>, award date {formatDate(award)} dashed</span>
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
          ) : null}
        </FigureGroup>
      ) : null}
      {api && api.requests > 0 ? (
        <FigureGroup title="API">
          <FigureList
            items={[
              shown.has("requests") ? null : fig("Requests", formatCount(api.requests)),
              fig("4xx rate", pct(api.rate_4xx)),
              fig("5xx rate", pct(api.rate_5xx)),
              fig(
                "p95 latency",
                api.p95_over_10s
                  ? OPEN_BUCKET
                  : api.p95_seconds === null
                    ? null
                    : formatLatency(api.p95_seconds),
              ),
              fig("Services", formatCount(api.services)),
              fig("Distinct routes", formatCount(api.routes), {
                definition: DISTINCT_ROUTES,
                name: "routes",
              }),
            ]}
          />
        </FigureGroup>
      ) : null}
      {chain && chain.ids > 0 ? (
        <FigureGroup
          title="On-chain"
          meta={v.link_two_way ? <Pill tone="success">stellar.toml linked both ways</Pill> : null}
        >
          <FigureList
            items={[
              chainOk && !shown.has("onchain_operations")
                ? fig("Operations", formatCount(chain.operations))
                : null,
              chainOk ? fig("Transactions", formatCount(chain.transactions)) : null,
              fig(
                "Verified IDs",
                `${formatCount(chain.ids_verified)} of ${formatCount(chain.ids)}`,
              ),
              chainOk
                ? fig("Days without data", formatCount(chain.days_missing), {
                    definition: DAYS_WITHOUT_DATA,
                    name: "days-missing",
                  })
                : null,
              chain.weekly_active_accounts.average === null
                ? null
                : fig("Weekly active accounts", weeklyActiveText(chain.weekly_active_accounts)),
            ]}
          />
          {chainOk && chainDays ? (
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
          ) : null}
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
                    <span className="text-caption text-fg-2">{functionsDaysText(c.functions)}</span>
                  </div>
                  <FunctionsTable
                    summary={c.functions}
                    label={`Calls per function of ${c.strkey}`}
                  />
                </div>
              ))}
            </div>
          ) : null}
        </FigureGroup>
      ) : null}
    </div>
  );
}

// ---- signed reports of the same figures: a list of rows, and a dialog
// that signs a new one and hands over its link

export function SignedReports({
  open,
  onOpenChange,
}: {
  // the page's header button opens the dialog; alone, the section has its own
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const { tenant } = useProject();
  const list = useQuery(reportsQuery(tenant.id));
  const queryClient = useQueryClient();
  const [ownOpen, setOwnOpen] = useState(false);
  const controlled = open !== undefined;
  const signing = controlled ? open : ownOpen;
  const setSigning = (o: boolean) => (controlled ? onOpenChange?.(o) : setOwnOpen(o));
  const [revoking, setRevoking] = useState<ReportSummary | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: reportKeysOf.list(tenant.id) });
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

  const items = list.data?.items ?? [];
  return (
    <section aria-labelledby="signed-reports-h" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="signed-reports-h" className="text-title text-fg">
          Signed reports
          {items.length > 0 ? (
            <span className="ml-2 text-small text-fg-2 tabular">{formatCount(items.length)}</span>
          ) : null}
        </h2>
        {controlled ? null : (
          <Button variant="outline" size="sm" onClick={() => setSigning(true)}>
            <FileSignature aria-hidden />
            Sign a report
          </Button>
        )}
      </div>
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
      ) : items.length === 0 ? (
        <p className="text-small text-fg-2" data-reports="none">
          No report yet
        </p>
      ) : (
        <ul
          className="flex flex-col divide-y divide-line rounded-lg border border-line bg-raised"
          aria-label="Reports"
        >
          {items.map((r) => (
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

      <SignDialog open={signing} onOpenChange={setSigning} onSigned={refresh} />

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            // a refusal shown for one report must not greet the next
            revoke.reset();
          }
        }}
        title={
          revoking
            ? `Revoke the link to the report for ${formatDateRange(revoking.from, revoking.to)}?`
            : "Revoke the report link?"
        }
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

// the period of closed utc days, then the signed report's link in the same
// dialog
function SignDialog({
  open,
  onOpenChange,
  onSigned,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSigned: () => Promise<unknown>;
}) {
  const { tenant } = useProject();
  const initial = closedDays(30, new Date());
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  const create = useMutation({
    mutationFn: () => reportApi.create(tenant.id, { from, to }),
    onSuccess: async (r) => {
      toast.success(`Report for ${formatDateRange(r.from, r.to)} signed.`);
      await onSigned();
    },
  });

  function close(o: boolean) {
    onOpenChange(o);
    if (!o) {
      create.reset();
      setErrors({});
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    if (create.data) {
      close(false);
      return;
    }
    const found = validatePeriod(from, to, new Date());
    setErrors(found);
    if (!found.from && !found.to) create.mutate();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={create.data ? "Report signed" : "Sign a report"}
      onSubmit={submit}
      footer={
        create.data ? (
          <Button type="submit">Done</Button>
        ) : (
          <>
            <Button type="button" variant="ghost" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" pending={create.isPending}>
              <FileSignature aria-hidden />
              {create.isPending ? "Signing" : "Generate and sign"}
            </Button>
          </>
        )
      }
    >
      {create.data ? (
        <div role="status" className="flex min-w-0 flex-wrap items-center gap-2 text-small">
          <a
            href={create.data.url}
            className="min-w-0 break-all rounded-sm font-mono text-accent-text underline-offset-4 hover:underline focus-ring"
          >
            {create.data.url}
          </a>
          <CopyButton text={create.data.url} label="Copy link" size="xs" />
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <DateField label="From" value={from} onChange={setFrom} error={errors.from} />
            <DateField
              label="To"
              value={to}
              onChange={setTo}
              error={errors.to}
              hint={`${formatDate(lastFinalDay(new Date()))} or earlier, at most ${MAX_REPORT_DAYS} days`}
            />
          </div>
          <FormMessage error={create.error} />
        </div>
      )}
    </Dialog>
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
      <span className="flex min-w-48 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-body font-medium text-fg">{formatDateRange(r.from, r.to)}</span>
          {revoked ? <Pill tone="neutral">Link revoked</Pill> : null}
        </span>
        <span className="text-caption text-fg-2">
          Generated {formatDateTimeUtc(r.created_at)}, {countOf(r.view_count, "view")}
        </span>
      </span>
      {/* the same slots on every row, right aligned: copy link while the
          link is live, download always in one place, then the menu */}
      <span className="flex items-center gap-1">
        {revoked ? null : (
          <CopyButton text={r.url} label="Copy link" accessibleLabel={`Copy link of ${of}`} />
        )}
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
        {revoked ? (
          <span aria-hidden className="size-(--vs-control-sm) max-lg:size-(--vs-control-touch)" />
        ) : (
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
              <MenuSeparator />
              <MenuItem tone="critical" onSelect={onRevoke}>
                <Ban aria-hidden />
                Revoke link
              </MenuItem>
            </MenuContent>
          </Menu>
        )}
      </span>
    </li>
  );
}
