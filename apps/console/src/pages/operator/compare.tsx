import { useQuery } from "@tanstack/react-query";
import { Columns2, Plus, X } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import type { Cohort } from "@/api/cohorts";
import { apiCompareQuery, webCompareQuery } from "@/api/queries";
import type {
  ApiCompare,
  ApiCompareProject,
  ApiNumbers,
  Breakdown,
  TenantRef,
  WebCompare,
  WebDimension,
} from "@/api/schemas";
import type { Series } from "@/components/api-charts";
import {
  type ChartMarker,
  CompareChart,
  type CompareValue,
  SERIES_BG,
  SERIES_COLOR,
  SeriesLine,
} from "@/components/compare-charts";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { CompareIntegrity } from "@/components/integrity";
import { Stagger, StaggerItem } from "@/components/motion";
import { StatusDot, statusText, statusTone } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatRate, formatSeconds, OPEN_BUCKET } from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";
import {
  compareSearch,
  countryName,
  type DayRange,
  formatDuration,
  formatPct,
  formatShare,
  MAX_COMPARE,
  readCompare,
} from "@/lib/global";
import { API_NOTES, SourceBadge } from "./global-api";
import {
  AGGREGATE_NOTE,
  type CompareHandover,
  cohortText,
  Missing,
  Notes,
  QUARANTINE_NOTE,
  RangeControl,
  VISITORS_NOTE,
} from "./global-shared";
import { CompareOnchainSection, ExportCompareButton } from "./onchain-parts";

const DIMENSIONS: { dim: WebDimension; label: string }[] = [
  { dim: "referrer", label: "Referrers" },
  { dim: "country", label: "Countries" },
  { dim: "device", label: "Devices" },
  { dim: "browser", label: "Browsers" },
  { dim: "os", label: "Operating systems" },
];

// the award date of each compared project that has one (contract 19.5),
// drawn on every daily chart so growth after funding is visible
function awardMarkers(projects: { tenant: TenantRef; cohort: Cohort }[]): ChartMarker[] {
  return projects.flatMap((p, slot) =>
    p.cohort.award_date
      ? [{ day: p.cohort.award_date, slot, label: `${p.tenant.name} awarded` }]
      : [],
  );
}

function seriesOf(tenants: TenantRef[]): Series[] {
  return tenants.map((t, i) => ({
    key: t.id,
    label: t.name,
    color: SERIES_COLOR[i] ?? "var(--vs-fg-3)",
  }));
}

const compact = (v: number) =>
  new Intl.NumberFormat("en", { notation: v >= 10_000 ? "compact" : "standard" }).format(v);
const pctAxis = (v: number) => `${Math.round(v * 1000) / 10}%`;

// the side-by-side view of up to five projects (contract 8.4 and 8.6). with
// one project it is that project's aggregate drill-down.
// the global web view over the comparison's range, so the projects found
// there compare over the same days
function addSearch(range: { from: string; to: string } | null): string {
  const p = new URLSearchParams();
  if (range?.from) p.set("from", range.from);
  if (range?.to) p.set("to", range.to);
  return p.toString();
}

export function ComparePage() {
  const [params, setParams] = useSearchParams();
  const { tenants, range } = readCompare(params);
  const setRange = (r: DayRange) => setParams(compareSearch(tenants, r), { replace: true });
  const web = useQuery({ ...webCompareQuery(tenants, range), enabled: tenants.length > 0 });
  const apiq = useQuery({ ...apiCompareQuery(tenants, range), enabled: tenants.length > 0 });

  if (tenants.length === 0) {
    return (
      <section aria-labelledby="compare-h" className="flex flex-col gap-4">
        <h2 id="compare-h" className="text-section text-fg">
          Compare projects
        </h2>
        <StateBlock
          kind="empty"
          icon={<Columns2 aria-hidden />}
          title="No project is selected"
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button asChild variant="outline" size="sm">
                <Link to="/operator/web">Global web</Link>
              </Button>
              <Button asChild variant="outline" size="sm">
                <Link to="/operator/api">Global API</Link>
              </Button>
            </div>
          }
        >
          Select up to five projects in Global web or Global API and choose Compare.
        </StateBlock>
      </section>
    );
  }

  const resolved = web.data ? { from: web.data.from, to: web.data.to } : null;
  const refs = web.data?.projects.map((p) => p.tenant) ?? apiq.data?.projects.map((p) => p.tenant);
  const cohorts = web.data?.projects ?? apiq.data?.projects ?? [];
  const cohortOf = (id: string) => {
    const c = cohorts.find((p) => p.tenant.id === id)?.cohort;
    return c ? cohortText(c) : null;
  };
  const remove = (id: string) =>
    setParams(
      compareSearch(
        tenants.filter((t) => t !== id),
        resolved ?? range,
      ),
      { replace: true },
    );

  return (
    <section aria-labelledby="compare-h" className="flex flex-col gap-6">
      <h2 id="compare-h" className="text-section text-fg">
        {tenants.length === 1 ? "Project aggregates" : "Compare projects"}
      </h2>

      {/* the compared projects and the range stay in view while the charts scroll */}
      <div className="sticky top-0 z-20 -mx-4 flex flex-wrap items-center gap-2 border-b border-line bg-ground px-4 py-2.5 max-lg:top-(--vs-header) sm:-mx-8 sm:px-8">
        <span className="text-small text-fg-3">Compared projects</span>
        {refs ? (
          <ul aria-label="Compared projects" className="flex flex-wrap gap-2">
            {refs.map((t, i) => (
              <li
                key={t.id}
                className="inline-flex h-8 max-w-96 animate-reveal-in items-center gap-2 rounded-md border border-line-strong pr-1 pl-2.5 text-small text-fg"
              >
                <SeriesLine slot={i} />
                <span className="truncate font-medium">{t.name}</span>
                {cohortOf(t.id) ? (
                  <span data-cohort className="truncate text-caption text-fg-3 max-md:hidden">
                    {cohortOf(t.id)}
                  </span>
                ) : null}
                <span className="inline-flex items-center gap-1 text-caption text-fg-3">
                  <StatusDot tone={statusTone(t.status)} />
                  <span className="max-sm:sr-only">{statusText(t.status)}</span>
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={`Remove ${t.name} from the comparison`}
                  onClick={() => remove(t.id)}
                >
                  <X aria-hidden />
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        {tenants.length < MAX_COMPARE ? (
          <Button
            asChild
            variant="outline"
            size="sm"
            className="border-dashed text-fg-2 max-lg:h-(--vs-control-touch)"
          >
            <Link
              to={`/operator/web?${addSearch(resolved)}`}
              state={
                {
                  compare: (refs ?? []).map((t) => ({ id: t.id, name: t.name })),
                } satisfies CompareHandover
              }
            >
              <Plus aria-hidden />
              Add a project
            </Link>
          </Button>
        ) : null}
        <RangeControl resolved={resolved} onChange={setRange} className="lg:ml-auto" />
      </div>

      <section aria-labelledby="compare-web" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="compare-web" className="text-section text-fg">
            Web
          </h2>
          <span className="text-small text-fg-3">Per UTC day, from project aggregates</span>
          <span className="ml-auto">
            <ExportCompareButton view="web" tenants={tenants} resolved={resolved}>
              Export web CSV
            </ExportCompareButton>
          </span>
        </div>
        {web.isPending ? <Loading label="Loading web numbers" /> : null}
        {web.error ? <ErrorPanel error={web.error} onRetry={() => web.refetch()} /> : null}
        {web.data ? <WebSection data={web.data} /> : null}
      </section>

      <section aria-labelledby="compare-api" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="compare-api" className="text-section text-fg">
            API
          </h2>
          <span className="text-small text-fg-3">Every service, per UTC day</span>
          <span className="ml-auto">
            <ExportCompareButton view="api" tenants={tenants} resolved={resolved}>
              Export API CSV
            </ExportCompareButton>
          </span>
        </div>
        {apiq.isPending ? <Loading label="Loading API numbers" /> : null}
        {apiq.error ? <ErrorPanel error={apiq.error} onRetry={() => apiq.refetch()} /> : null}
        {apiq.data ? <ApiSection data={apiq.data} /> : null}
      </section>

      {web.data ? <CompareOnchainSection projects={web.data.projects} /> : null}

      <Notes
        items={[
          VISITORS_NOTE,
          QUARANTINE_NOTE,
          "A day not read by the aggregation yet (for example before a backfill) is a gap in the web charts and shows as not read in the tables; a day read without traffic shows 0.",
          ...API_NOTES.filter((n) => n !== AGGREGATE_NOTE),
          AGGREGATE_NOTE,
        ]}
      >
        <DbIpAttribution className="mt-1" />
      </Notes>
    </section>
  );
}

// ---- web

function WebSection({ data }: { data: WebCompare }) {
  const series = seriesOf(data.projects.map((p) => p.tenant));
  const markers = awardMarkers(data.projects);
  const point = (s: number, d: number) => data.projects[s]?.series[d];
  const count = (k: "pageviews" | "visitors" | "visits") => (s: number, d: number) =>
    point(s, d)?.[k] ?? null;
  const fmtCount = (v: number | "open") => (v === "open" ? OPEN_BUCKET : formatCount(v));
  return (
    <>
      <div className="grid gap-3 lg:grid-cols-2">
        <CompareChart
          kind="count"
          title="Page views"
          description="Page views of the primary band per UTC day."
          days={data.days}
          series={series}
          markers={markers}
          value={count("pageviews")}
          format={fmtCount}
          axisFormat={compact}
          missingText="not read"
          gapLabel="not read"
        />
        <CompareChart
          kind="count"
          title="Visitors"
          description="Visitors per UTC day, unique within the day and site."
          days={data.days}
          series={series}
          markers={markers}
          value={count("visitors")}
          format={fmtCount}
          axisFormat={compact}
          missingText="not read"
          gapLabel="not read"
        />
        <CompareChart
          kind="share"
          title="Bounce rate"
          description="Visits with one page view and no event, over all visits. Gaps are days without visits."
          days={data.days}
          series={series}
          markers={markers}
          value={(s, d) => point(s, d)?.bounce_rate ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatShare(v) ?? "-"))}
          axisFormat={pctAxis}
        />
        <CompareChart
          kind="count"
          title="Average visit duration"
          description="Time between the first and last page view of a visit, averaged per day."
          days={data.days}
          series={series}
          markers={markers}
          value={(s, d) => point(s, d)?.avg_visit_duration ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatDuration(v) ?? "-"))}
          axisFormat={(v) => formatDuration(v) ?? ""}
        />
      </div>
      <CompareIntegrity projects={data.projects} />
      <WebTotalsTable data={data} />
      {DIMENSIONS.map((d) => (
        <BreakdownRow
          key={d.dim}
          label={d.label}
          dim={d.dim}
          projects={data.projects.map((p) => ({ tenant: p.tenant, b: p.breakdowns[d.dim] }))}
        />
      ))}
    </>
  );
}

function WebTotalsTable({ data }: { data: WebCompare }) {
  const cols: {
    label: string;
    value: (t: WebCompare["projects"][number]["totals"]) => string | null;
  }[] = [
    { label: "Page views", value: (t) => formatCount(t.pageviews) },
    { label: "Visitors", value: (t) => formatCount(t.visitors) },
    { label: "Visits", value: (t) => formatCount(t.visits) },
    { label: "Bounce rate", value: (t) => formatShare(t.bounce_rate) },
    { label: "Average visit duration", value: (t) => formatDuration(t.avg_visit_duration) },
    { label: "Quarantined page views", value: (t) => formatCount(t.quarantine_pageviews) },
    { label: "Quarantined share", value: (t) => formatShare(t.quarantine_share) },
  ];
  return (
    <TotalsTable
      label="Web totals"
      metrics={cols.map((c) => c.label)}
      rows={data.projects.map((p) => ({
        tenant: p.tenant,
        cells: cols.map((c) => c.value(p.totals)),
      }))}
    />
  );
}

// the range totals: one row per project in its series slot, one column per
// metric, so a column reads down across the projects
function TotalsTable({
  label,
  metrics,
  rows,
}: {
  label: string;
  metrics: string[];
  rows: { tenant: TenantRef; cells: (string | null)[] }[];
}) {
  return (
    <section
      aria-label={label}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable region needs focus (wcag 2.1.1)
      tabIndex={0}
      className="relative overflow-x-auto rounded-lg border border-line bg-raised focus-ring"
    >
      <table className="w-full text-left text-small tabular">
        <thead>
          <tr>
            <th
              scope="col"
              className="h-9 border-b border-line px-4 text-caption font-medium text-fg-3"
            >
              Project
            </th>
            {metrics.map((m) => (
              <th
                key={m}
                scope="col"
                className="h-9 border-b border-line px-3 text-right text-caption font-medium whitespace-nowrap text-fg-3 last:pr-4"
              >
                {m}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr
              key={r.tenant.id}
              data-tenant={r.tenant.id}
              className="transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover [&:last-child>*]:border-0"
            >
              <th
                scope="row"
                className="h-10 border-b border-line-subtle px-4 font-medium whitespace-nowrap text-fg"
              >
                <span className="inline-flex items-center gap-2">
                  <SeriesLine slot={i} />
                  {r.tenant.name}
                </span>
              </th>
              {r.cells.map((c, j) => (
                <td
                  key={metrics[j]}
                  data-metric={metrics[j]}
                  className="border-b border-line-subtle px-3 text-right font-mono whitespace-nowrap text-fg last:pr-4"
                >
                  {c === null ? <Missing /> : c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function keyText(dim: WebDimension, key: string): string {
  return dim === "country" ? countryName(key) : key;
}

// one dimension across the projects: each project's top keys with a bar
// scaled to that project's largest value. values are umami's y, distinct
// visitors of a day whose page views carry the key, summed over the days.
function BreakdownRow({
  label,
  dim,
  projects,
}: {
  label: string;
  dim: WebDimension;
  projects: { tenant: TenantRef; b: Breakdown }[];
}) {
  return (
    <section
      aria-label={label}
      className="flex flex-col gap-3 rounded-lg border border-line bg-raised px-4 py-4"
    >
      <div className="flex flex-col gap-0.5">
        <h3 className="text-title text-fg">{label}</h3>
        <p className="text-caption text-fg-3">
          Top values over the range, counted in daily visitors.
          {dim === "referrer"
            ? " Only other sites count; visits from a project's own pages do not."
            : ""}
        </p>
      </div>
      <Stagger className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        {projects.map(({ tenant, b }, i) => {
          const max = Math.max(1, ...b.items.map((x) => x.value));
          return (
            <StaggerItem key={tenant.id} className="min-w-0">
              <p className="flex items-center gap-1.5 text-small font-medium text-fg">
                <SeriesLine slot={i} />
                <span className="truncate">{tenant.name}</span>
              </p>
              {b.items.length === 0 ? (
                <p className="mt-1 text-small text-fg-3">None in this range.</p>
              ) : (
                <ul className="mt-2 flex flex-col gap-2" aria-label={`${label} of ${tenant.name}`}>
                  {b.items.map((x) => (
                    <li key={x.key} className="text-small">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="min-w-0 truncate text-fg-2" title={keyText(dim, x.key)}>
                          {keyText(dim, x.key)}
                        </span>
                        <span className="shrink-0 font-mono text-fg tabular">
                          {formatCount(x.value)}
                        </span>
                      </div>
                      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-line-subtle">
                        <div
                          className={`h-full rounded-full ${SERIES_BG[i] ?? "bg-fg-3"}`}
                          // the width is data; react writes it through the cssom
                          style={{ width: `${(x.value / max) * 100}%` }}
                        />
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              {b.total_keys > b.items.length ? (
                <p className="mt-1.5 text-caption text-fg-3">
                  {formatCount(b.total_keys - b.items.length)} more
                </p>
              ) : null}
            </StaggerItem>
          );
        })}
      </Stagger>
    </section>
  );
}

// ---- api

function pctValue(seconds: number | null, over: boolean): CompareValue {
  if (over) return "open";
  return seconds;
}

function ApiSection({ data }: { data: ApiCompare }) {
  const series = seriesOf(data.projects.map((p) => p.tenant));
  const markers = awardMarkers(data.projects);
  const point = (s: number, d: number) => data.projects[s]?.series[d];
  const rate = (k: "rate_4xx" | "rate_5xx") => (s: number, d: number) => point(s, d)?.[k] ?? null;
  return (
    <>
      <div className="grid gap-3 lg:grid-cols-2">
        <CompareChart
          kind="count"
          title="Requests"
          description="Requests per UTC day over every service."
          days={data.days}
          series={series}
          markers={markers}
          value={(s, d) => point(s, d)?.requests ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : formatCount(v))}
          axisFormat={compact}
        />
        <CompareChart
          kind="measure"
          title="p95 latency"
          description="95th percentile of request duration per day, from summed histograms. Gaps are days without requests."
          days={data.days}
          series={series}
          markers={markers}
          value={(s, d) => {
            const p = point(s, d);
            return p ? pctValue(p.p95_seconds, p.p95_over_10s) : null;
          }}
          format={(v) => (v === "open" ? OPEN_BUCKET : formatSeconds(v))}
          axisFormat={formatSeconds}
        />
        <CompareChart
          kind="share"
          title="4xx rate"
          description="Share of requests answered with a 4xx status. Gaps are days without requests."
          days={data.days}
          series={series}
          markers={markers}
          value={rate("rate_4xx")}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatRate(v) ?? "-"))}
          axisFormat={pctAxis}
        />
        <CompareChart
          kind="share"
          title="5xx rate"
          description="Share of requests answered with a 5xx status. Gaps are days without requests."
          days={data.days}
          series={series}
          markers={markers}
          value={rate("rate_5xx")}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatRate(v) ?? "-"))}
          axisFormat={pctAxis}
        />
      </div>
      <ApiTotalsTable data={data} />
      <Stagger className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {data.projects.map((p, i) => (
          <StaggerItem key={p.tenant.id} className="min-w-0">
            <ServicesCard project={p} slot={i} />
          </StaggerItem>
        ))}
      </Stagger>
    </>
  );
}

function pctText(n: ApiNumbers, k: "p50" | "p95" | "p99"): string | null {
  return formatPct(n[`${k}_seconds`], n[`${k}_over_10s`]);
}

function ApiTotalsTable({ data }: { data: ApiCompare }) {
  const cols: { label: string; value: (t: ApiCompareProject["totals"]) => string | null }[] = [
    { label: "Requests", value: (t) => formatCount(t.requests) },
    { label: "Infrastructure requests", value: (t) => formatCount(t.infrastructure_requests) },
    { label: "4xx rate", value: (t) => formatRate(t.rate_4xx) },
    { label: "5xx rate", value: (t) => formatRate(t.rate_5xx) },
    { label: "p50", value: (t) => pctText(t, "p50") },
    { label: "p95", value: (t) => pctText(t, "p95") },
    { label: "p99", value: (t) => pctText(t, "p99") },
    { label: "Services", value: (t) => formatCount(t.services) },
    { label: "Routes", value: (t) => formatCount(t.routes) },
  ];
  return (
    <TotalsTable
      label="API totals"
      metrics={cols.map((c) => c.label)}
      rows={data.projects.map((p) => ({
        tenant: p.tenant,
        cells: cols.map((c) => c.value(p.totals)),
      }))}
    />
  );
}

function ServicesCard({ project, slot }: { project: ApiCompareProject; slot: number }) {
  const t = project.totals;
  return (
    <section
      aria-label={`Services of ${project.tenant.name}`}
      className="flex h-full min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised px-4 py-3.5"
    >
      <p className="flex items-center gap-1.5 text-body font-medium text-fg">
        <SeriesLine slot={slot} />
        <span className="truncate">{project.tenant.name}</span>
      </p>
      <div className="flex flex-wrap items-center gap-2 text-caption text-fg-3">
        <SourceBadge source={t.source} spanServices={t.span_services} services={t.services} />
        {t.lossy ? <span>some latency buckets are approximate</span> : null}
      </div>
      {project.services.length === 0 ? (
        <p className="text-small text-fg-3">No requests in this range.</p>
      ) : (
        <ul className="flex flex-col text-small">
          {project.services.map((s) => (
            <li key={s.service} className="border-b border-line-subtle py-1.5 last:border-0">
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate font-mono text-caption text-fg">
                  {s.service === "" ? "(no service name)" : s.service}
                </span>
                <span className="shrink-0 font-mono text-fg tabular">
                  {formatCount(s.requests)}
                </span>
              </div>
              <p className="text-caption text-fg-3">
                {formatCount(s.routes)} {s.routes === 1 ? "route" : "routes"}, 5xx{" "}
                {formatRate(s.rate_5xx) ?? "-"}, p95 {pctText(s, "p95") ?? "-"}
                {s.source === "span" ? ", from traces" : ""}
                {s.lossy ? ", approximate" : ""}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
