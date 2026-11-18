// the columns of the operator project list, one set per view. every number
// carries its definition on its header, so the table explains itself.

import type { Kpi, KpiKey } from "@/api/cohorts";
import type { ApiProject, OperatorTenantSummary, TenantStatus, WebProject } from "@/api/schemas";
import { CoverageCell } from "@/components/coverage";
import type { Column } from "@/components/data-table";
import { IntegrityCell } from "@/components/integrity";
import { Missing } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Tooltip } from "@/components/ui/tooltip";
import {
  countOf,
  countryName,
  formatChange,
  formatCohortPercentile,
  formatCount,
  formatDate,
  formatDuration,
  formatLatency,
  formatPercent,
  formatPoints,
  OPEN_BUCKET,
} from "@/lib/format";
import type { DayRange } from "@/lib/global";
import { MIN_COHORT, type ProjectsView } from "@/lib/operator-projects";
import { cohortLine, OnchainValue, SeriesSwatch, SOURCE_DEFINITION, SourceText } from "./shared";

const NO_DATA = "No data in this range";

function share(v: number | null, reason = NO_DATA) {
  return v === null ? <Missing reason={reason} /> : formatPercent(v);
}

function latency(seconds: number | null, over: boolean) {
  if (over) return OPEN_BUCKET;
  return seconds === null ? <Missing reason="No requests in this range" /> : formatLatency(seconds);
}

function change(k: Kpi, kind: "relative" | "points") {
  return (
    <span data-kpi="change">
      {k.change === null ? (
        <Missing reason="No value in the previous range" />
      ) : kind === "points" ? (
        formatPoints(k.change)
      ) : (
        formatChange(k.change)
      )}
    </span>
  );
}

// a percentile says little in a small cohort, so it is hidden below the
// minimum size, with the reason
function percentile(k: Kpi, cohortSize: number) {
  return (
    <span data-kpi="percentile">
      {cohortSize < MIN_COHORT ? (
        <Missing reason={`Fewer than ${MIN_COHORT} projects in the cohort`} />
      ) : k.percentile === null ? (
        <Missing reason={NO_DATA} />
      ) : (
        formatCohortPercentile(k.percentile)
      )}
    </span>
  );
}

// the project name, cut to the column with the full name in a tooltip. the
// status has its own column in every view; the web and api phone cards
// show it as a badge on the name line, so their three numbers stay numbers
function ProjectName({
  name,
  slot,
  status,
}: {
  name: string;
  slot: number;
  status?: TenantStatus;
}) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      <SeriesSwatch slot={slot} />
      <Tooltip content={name}>
        <span className="min-w-0 truncate">{name}</span>
      </Tooltip>
      {status ? (
        <span className="shrink-0 sm:hidden">
          <StatusBadge kind="project" status={status} operator />
        </span>
      ) : null}
    </span>
  );
}

const CHANGE_DEF = "Change against the previous range of the same length.";
const PERCENTILE_DEF = `Share of the projects in the cohort at or below this value. The cohort filters set the cohort. Shown for ${MIN_COHORT} or more projects.`;

const GROWTH: {
  id: string;
  label: string;
  kpi: KpiKey;
  kind: "relative" | "points";
  percentile: boolean;
  definition: string;
}[] = [
  {
    id: "visitors_growth",
    label: "Visitors change",
    kpi: "visitors",
    kind: "relative",
    percentile: false,
    definition: CHANGE_DEF,
  },
  {
    id: "pageviews_growth",
    label: "Page views change",
    kpi: "pageviews",
    kind: "relative",
    percentile: false,
    definition: CHANGE_DEF,
  },
  {
    id: "requests_growth",
    label: "Requests change",
    kpi: "requests",
    kind: "relative",
    percentile: false,
    definition: CHANGE_DEF,
  },
  {
    id: "error_rate_change",
    label: "Error rate change",
    kpi: "error_rate",
    kind: "points",
    percentile: false,
    definition: "Change of the 5xx rate against the previous range, in percentage points.",
  },
  {
    id: "onchain_operations_growth",
    label: "On-chain change",
    kpi: "onchain_operations",
    kind: "relative",
    percentile: false,
    definition: CHANGE_DEF,
  },
  {
    id: "visitors_percentile",
    label: "Visitors percentile",
    kpi: "visitors",
    kind: "relative",
    percentile: true,
    definition: PERCENTILE_DEF,
  },
  {
    id: "pageviews_percentile",
    label: "Page views percentile",
    kpi: "pageviews",
    kind: "relative",
    percentile: true,
    definition: PERCENTILE_DEF,
  },
  {
    id: "requests_percentile",
    label: "Requests percentile",
    kpi: "requests",
    kind: "relative",
    percentile: true,
    definition: PERCENTILE_DEF,
  },
  {
    id: "error_rate_percentile",
    label: "Error rate percentile",
    kpi: "error_rate",
    kind: "points",
    percentile: true,
    definition: `${PERCENTILE_DEF} A higher error rate percentile means more errors.`,
  },
  {
    id: "onchain_operations_percentile",
    label: "On-chain percentile",
    kpi: "onchain_operations",
    kind: "relative",
    percentile: true,
    definition: PERCENTILE_DEF,
  },
];

type Row = WebProject | ApiProject;

function growthColumns<R extends Row>(): Column<R>[] {
  return GROWTH.map((g) => ({
    key: g.id,
    header: g.label,
    definition: g.definition,
    sortKey: g.id,
    align: "right" as const,
    numeric: true,
    minWidth: "min-w-28",
    cell: (r: R) =>
      g.percentile ? percentile(r.kpis[g.kpi], r.cohort_size) : change(r.kpis[g.kpi], g.kind),
  }));
}

function commonColumns<R extends Row>(slot: (id: string) => number): Column<R>[] {
  return [
    {
      key: "name",
      header: "Project",
      sortKey: "name",
      primary: true,
      minWidth: "min-w-48",
      className: "max-w-72",
      cell: (r) => (
        <ProjectName name={r.tenant.name} slot={slot(r.tenant.id)} status={r.tenant.status} />
      ),
    },
    {
      key: "status",
      header: "Status",
      sortKey: "status",
      cell: (r) => <StatusBadge kind="project" status={r.tenant.status} operator />,
    },
    {
      key: "created_at",
      header: "Enrolled",
      sortKey: "created_at",
      cell: (r) => <span className="whitespace-nowrap">{formatDate(r.tenant.created_at)}</span>,
    },
    {
      key: "cohort",
      header: "Cohort",
      definition: "The SCF program, round, category and award date an operator confirmed.",
      minWidth: "min-w-40",
      className: "max-w-64",
      truncate: (r) => cohortLine(r.cohort) ?? "",
      cell: (r) =>
        cohortLine(r.cohort) ? (
          <span data-cohort="">{cohortLine(r.cohort)}</span>
        ) : (
          <Missing reason="No cohort is confirmed" />
        ),
    },
    {
      key: "sites",
      header: "Sites",
      sortKey: "sites",
      align: "right",
      numeric: true,
      cell: (r) => formatCount(r.sites),
    },
  ];
}

const num = (
  key: string,
  header: string,
  definition: string,
  cell: (r: WebProject) => React.ReactNode,
): Column<WebProject> => ({
  key,
  header,
  definition,
  sortKey: key,
  align: "right",
  numeric: true,
  minWidth: "min-w-24",
  cell,
});

export function webColumns(range: DayRange, slot: (id: string) => number): Column<WebProject>[] {
  return [
    ...commonColumns<WebProject>(slot),
    {
      key: "integrity",
      header: "Grade",
      definition:
        "Integrity grade of the web traffic in the range. Select a grade to see its reasons.",
      sortKey: "integrity",
      // a grade shows only once given: a column of "Not graded" says nothing
      cell: (r) =>
        r.integrity.grade === null ? null : (
          <IntegrityCell tenant={r.tenant} integrity={r.integrity} range={range} />
        ),
    },
    num("pageviews", "Page views", "Page views of the counted traffic in the range.", (r) => (
      <span data-col="pageviews">{formatCount(r.pageviews)}</span>
    )),
    num(
      "visitors",
      "Visitors",
      "Visitors are unique per UTC day and site. The range total is the sum of the days.",
      (r) => <span data-col="visitors">{formatCount(r.visitors)}</span>,
    ),
    num("visits", "Visits", "Visits in the range.", (r) => formatCount(r.visits)),
    num("bounces", "Bounces", "Visits with one page view and no event.", (r) =>
      formatCount(r.bounces),
    ),
    num("totaltime", "Total visit time", "The sum of the duration of every visit.", (r) =>
      formatDuration(r.totaltime),
    ),
    num(
      "bounce_rate",
      "Bounce rate",
      "Visits with one page view and no event, as a share of all visits.",
      (r) => share(r.bounce_rate, "No visits in this range"),
    ),
    num(
      "avg_visit_duration",
      "Average visit",
      "Time from the first to the last page view of a visit, on average.",
      (r) =>
        r.avg_visit_duration === null ? (
          <Missing reason="No visits in this range" />
        ) : (
          formatDuration(r.avg_visit_duration)
        ),
    ),
    num(
      "quarantine_share",
      "Held back",
      "Page views from a network that sent too many visitors in one hour, as a share of all page views. They are not in the other numbers.",
      (r) => share(r.quarantine_share, "No page views in this range"),
    ),
    num(
      "quarantine_pageviews",
      "Held back page views",
      "Page views from a network that sent too many visitors in one hour.",
      (r) => formatCount(r.quarantine_pageviews),
    ),
    num(
      "quarantine_visitors",
      "Held back visitors",
      "Visitors from a network that sent too many visitors in one hour.",
      (r) => formatCount(r.quarantine_visitors),
    ),
    num("ingest_requests", "Gateway requests", "Tracker requests the gateway answered.", (r) =>
      formatCount(r.ingest_requests),
    ),
    num(
      "ingest_rejected",
      "Refused requests",
      "Tracker requests the gateway refused, for example from a wrong origin or with a privacy signal.",
      (r) => formatCount(r.ingest_rejected),
    ),
    num(
      "rejected_share",
      "Refused",
      "Refused tracker requests as a share of all tracker requests.",
      (r) => share(r.rejected_share, "No tracker requests in this range"),
    ),
    {
      key: "unmeasured_share",
      header: "Not measured",
      definition:
        "Estimated share of visits from browsers with Global Privacy Control. These visits are not measured. Shown from 100 tracker loads.",
      sortKey: "unmeasured_share",
      align: "right",
      numeric: true,
      cell: (r) => <CoverageCell coverage={r.coverage} />,
    },
    num("sites_verified", "Verified sites", "Sites with a verified domain.", (r) =>
      formatCount(r.sites_verified),
    ),
    num(
      "sites_with_stellar_toml",
      "Sites with stellar.toml",
      "Sites whose domain serves a stellar.toml file.",
      (r) => formatCount(r.sites_with_stellar_toml),
    ),
    ...growthColumns<WebProject>(),
    {
      key: "onchain",
      header: "On-chain",
      definition: "Operations of the declared accounts and contracts in the range.",
      align: "right",
      numeric: true,
      cell: (r) => <OnchainValue s={r.onchain} />,
    },
    {
      key: "countries",
      header: "Top countries",
      definition: "The three countries with the most visitors in the range.",
      minWidth: "min-w-48",
      className: "max-w-64",
      truncate: (r) => countryText(r),
      cell: (r) =>
        r.top.country.length === 0 ? (
          <Missing reason="No visitors in this range" />
        ) : (
          countryText(r)
        ),
    },
  ];
}

function countryText(r: WebProject): string {
  return r.top.country
    .slice(0, 3)
    .map((c) => `${countryName(c.key)} ${formatCount(c.value)}`)
    .join(", ");
}

const apiNum = (
  key: string,
  header: string,
  definition: string,
  cell: (r: ApiProject) => React.ReactNode,
): Column<ApiProject> => ({
  key,
  header,
  definition,
  sortKey: key,
  align: "right",
  numeric: true,
  minWidth: "min-w-24",
  cell,
});

const PCT_DEF = (p: string) =>
  `The request duration that ${p}% of requests stay under. Above 10 s shows as >10 s.`;

export function apiColumns(slot: (id: string) => number): Column<ApiProject>[] {
  return [
    ...commonColumns<ApiProject>(slot),
    apiNum(
      "requests",
      "Requests",
      "Requests of every service in the range, without infrastructure requests such as health checks.",
      (r) => <span data-col="requests">{formatCount(r.requests)}</span>,
    ),
    apiNum("requests_4xx", "4xx responses", "Requests answered with a 4xx status.", (r) =>
      formatCount(r.requests_4xx),
    ),
    apiNum("requests_5xx", "5xx responses", "Requests answered with a 5xx status.", (r) =>
      formatCount(r.requests_5xx),
    ),
    apiNum("rate_4xx", "4xx rate", "Requests answered with a 4xx status, as a share.", (r) =>
      share(r.rate_4xx, "No requests in this range"),
    ),
    apiNum("rate_5xx", "5xx rate", "Requests answered with a 5xx status, as a share.", (r) =>
      share(r.rate_5xx, "No requests in this range"),
    ),
    apiNum("p50", "p50 latency", PCT_DEF("50"), (r) => latency(r.p50_seconds, r.p50_over_10s)),
    apiNum("p95", "p95 latency", PCT_DEF("95"), (r) => latency(r.p95_seconds, r.p95_over_10s)),
    apiNum("p99", "p99 latency", PCT_DEF("99"), (r) => latency(r.p99_seconds, r.p99_over_10s)),
    apiNum("services", "Services", "Services that sent data in the range.", (r) =>
      formatCount(r.services),
    ),
    apiNum("routes", "Routes", "Distinct routes over every service of the project.", (r) =>
      formatCount(r.routes),
    ),
    ...growthColumns<ApiProject>(),
    {
      key: "source",
      header: "Data source",
      definition: SOURCE_DEFINITION,
      minWidth: "min-w-32",
      cell: (r) => (
        <span className="flex items-center gap-2 whitespace-nowrap">
          <SourceText source={r.source} spanServices={r.span_services} services={r.services} />
          {r.lossy ? (
            <Tooltip content="Some latency buckets are approximate.">
              <span className="text-caption text-fg-2">Approximate</span>
            </Tooltip>
          ) : null}
        </span>
      ),
    },
    {
      key: "onchain",
      header: "On-chain",
      definition: "Operations of the declared accounts and contracts in the range.",
      align: "right",
      numeric: true,
      cell: (r) => <OnchainValue s={r.onchain} />,
    },
  ];
}

export function statusColumns(slot: (id: string) => number): Column<OperatorTenantSummary>[] {
  return [
    {
      key: "name",
      header: "Project",
      sortKey: "name",
      primary: true,
      minWidth: "min-w-48",
      className: "max-w-80",
      cell: (t) => <ProjectName name={t.name} slot={slot(t.id)} />,
    },
    {
      key: "status",
      header: "Status",
      cell: (t) => (
        <span className="flex flex-wrap items-center gap-2">
          <StatusBadge kind="project" status={t.status} operator />
          {t.sites_in_review > 0 ? (
            <span className="text-caption whitespace-nowrap text-warning">
              {countOf(t.sites_in_review, "site")} in review
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: "sites",
      header: "Sites",
      definition: "Sites of the project.",
      align: "right",
      numeric: true,
      cell: (t) => formatCount(t.site_count),
    },
    {
      key: "sites_verified",
      header: "Verified sites",
      definition: "Sites with a verified domain.",
      align: "right",
      numeric: true,
      cell: (t) => formatCount(t.sites_verified),
    },
    {
      key: "sites_with_stellar_toml",
      header: "Sites with stellar.toml",
      definition: "Sites whose domain serves a stellar.toml file.",
      align: "right",
      numeric: true,
      cell: (t) => formatCount(t.sites_with_stellar_toml),
    },
    {
      key: "created_at",
      header: "Enrolled",
      sortKey: "created_at",
      cell: (t) => <span className="whitespace-nowrap">{formatDate(t.created_at)}</span>,
    },
    {
      key: "status_changed_at",
      header: "Status since",
      sortKey: "status_changed_at",
      cell: (t) => <span className="whitespace-nowrap">{formatDate(t.status_changed_at)}</span>,
    },
  ];
}

// picks the shown columns, in the view's order, and marks the phone ones
export function pick<R>(all: Column<R>[], shown: string[], phone: string[]): Column<R>[] {
  return all
    .filter((c) => shown.includes(c.key))
    .map((c) => ({ ...c, mobile: phone.includes(c.key) }));
}

export type { ProjectsView };
