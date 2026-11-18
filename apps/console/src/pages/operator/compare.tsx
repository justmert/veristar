import { useQuery } from "@tanstack/react-query";
import { ChevronDown, Columns2, Download, Plus, X } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import type { Cohort } from "@/api/cohorts";
import { exportPaths } from "@/api/onchain";
import { apiCompareQuery, webCompareQuery } from "@/api/queries";
import type {
  ApiCompare,
  ApiCompareProject,
  ApiNumbers,
  TenantRef,
  WebCompare,
  WebDimension,
} from "@/api/schemas";
import { BreakdownList, type TimeBucket, TimeChart } from "@/components/charts";
import { type Column, DataTable } from "@/components/data-table";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { CompareIntegrity } from "@/components/integrity";
import { PageHeader } from "@/components/page-header";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import {
  countOf,
  countryName,
  formatCompact,
  formatCount,
  formatDate,
  formatDuration,
  formatLatency,
  formatPercent,
  formatTick,
  niceTicks,
  OPEN_BUCKET,
  utcDay,
} from "@/lib/format";
import { compareSearch, type DayRange, MAX_COMPARE, readCompare } from "@/lib/global";
import { CompareOnchainSection } from "./onchain-parts";
import {
  type CompareHandover,
  cohortText,
  ListRange,
  SERIES_TONES,
  SeriesSwatch,
  SourceText,
} from "./shared";

const DIMENSIONS: { dim: WebDimension; label: string }[] = [
  { dim: "referrer", label: "Referrers" },
  { dim: "country", label: "Countries" },
  { dim: "device", label: "Devices" },
  { dim: "browser", label: "Browsers" },
  { dim: "os", label: "Operating systems" },
];

// up to five projects side by side (contract 8.4 and 8.6), web, api and
// on-chain, per utc day. each compared project links to its page.
export function ComparePage() {
  const [params, setParams] = useSearchParams();
  const { tenants, range } = readCompare(params);
  const setRange = (r: DayRange) => setParams(compareSearch(tenants, r), { replace: true });
  const web = useQuery({ ...webCompareQuery(tenants, range), enabled: tenants.length > 0 });
  const apiq = useQuery({ ...apiCompareQuery(tenants, range), enabled: tenants.length > 0 });

  if (tenants.length === 0) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Compare" />
        <EmptyState
          icon={<Columns2 aria-hidden />}
          title="No project is selected"
          action={
            <Button asChild variant="outline" size="sm">
              <Link to="/operator/projects?view=web">Open Projects</Link>
            </Button>
          }
        >
          Select up to {MAX_COMPARE} projects in Projects. Then choose Compare.
        </EmptyState>
      </div>
    );
  }

  const resolved = web.data
    ? { from: web.data.from, to: web.data.to }
    : apiq.data
      ? { from: apiq.data.from, to: apiq.data.to }
      : null;
  const refs = web.data?.projects ?? apiq.data?.projects ?? null;
  const remove = (id: string) =>
    setParams(
      compareSearch(
        tenants.filter((t) => t !== id),
        resolved ?? range,
      ),
      { replace: true },
    );
  const exportLink = (view: "web" | "api", label: string) =>
    resolved ? (
      <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
        <a href={exportPaths.compare(view, tenants, resolved)} download>
          <Download aria-hidden />
          {label}
        </a>
      </Button>
    ) : null;

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Compare"
        freshness={
          web.dataUpdatedAt > 0
            ? {
                at: new Date(web.dataUpdatedAt),
                cadence: "Web numbers are read every hour. API numbers refresh every 5 minutes.",
              }
            : null
        }
        secondary={
          <>
            {exportLink("web", "Export web CSV")}
            {exportLink("api", "Export API CSV")}
          </>
        }
      />

      {/* the chips are the one legend of every chart below, so they stay in
          view while the charts scroll */}
      <section
        aria-label="Compared projects"
        className="flex flex-col gap-3 bg-ground lg:sticky lg:top-0 lg:z-(--vs-layer-raised) lg:-my-2 lg:py-2"
      >
        <div className="flex flex-wrap items-center gap-2">
          {refs ? (
            <ul className="flex min-w-0 flex-wrap gap-2">
              {refs.map((p, i) => (
                <ProjectChip
                  key={p.tenant.id}
                  tenant={p.tenant}
                  cohort={p.cohort}
                  slot={i}
                  onRemove={() => remove(p.tenant.id)}
                />
              ))}
            </ul>
          ) : (
            <span className="text-small text-fg-2">{countOf(tenants.length, "project")}</span>
          )}
          {tenants.length < MAX_COMPARE ? (
            <Link
              to={`/operator/projects?${addSearch(resolved)}`}
              state={
                {
                  compare: (refs ?? []).map((p) => ({ id: p.tenant.id, name: p.tenant.name })),
                } satisfies CompareHandover
              }
              className="inline-flex h-8 items-center gap-1 rounded-md border border-dashed border-line-strong px-2 text-small text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover hover:text-fg focus-ring max-lg:h-(--vs-control-touch)"
            >
              <Plus aria-hidden className="size-4" />
              Add a project
            </Link>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <ListRange resolved={resolved} requested={range} onRange={setRange} />
          {resolved && resolved.to >= utcDay() ? <PartialKey /> : null}
        </div>
      </section>

      <Section id="compare-web" title="Web">
        {web.isPending ? (
          <ChartsLoading />
        ) : web.error ? (
          <ErrorState
            title="The web numbers did not load"
            error={web.error}
            onRetry={() => web.refetch()}
          />
        ) : (
          <WebSection data={web.data} />
        )}
      </Section>

      <Section id="compare-api" title="API">
        {apiq.isPending ? (
          <ChartsLoading />
        ) : apiq.error ? (
          <ErrorState
            title="The API numbers did not load"
            error={apiq.error}
            onRetry={() => apiq.refetch()}
          />
        ) : (
          <ApiSection data={apiq.data} />
        )}
      </Section>

      {web.data ? <CompareOnchainSection projects={web.data.projects} /> : null}

      <details className="max-w-prose text-small text-fg-2">
        <summary className="w-fit cursor-pointer rounded-sm font-medium transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring">
          About these numbers
        </summary>
        <ul className="mt-2 flex list-disc flex-col gap-1 pl-5">
          <li>Every number comes from daily totals of UTC days.</li>
          <li>A hatched day is still running. Its value grows until the day ends.</li>
          <li>A gap in a web chart is a day that the web analytics have not read yet.</li>
          <li>Visitors are unique per UTC day and site.</li>
          <li>Latency percentiles come from summed duration buckets, not from averages.</li>
        </ul>
      </details>
    </div>
  );
}

// the project list over the comparison's range, so the projects found there
// compare over the same days
function addSearch(range: DayRange | null): string {
  const p = new URLSearchParams({ view: "web" });
  if (range?.from) p.set("from", range.from);
  if (range?.to) p.set("to", range.to);
  return p.toString();
}

function ProjectChip({
  tenant,
  cohort,
  slot,
  onRemove,
}: {
  tenant: TenantRef;
  cohort: Cohort;
  slot: number;
  onRemove: () => void;
}) {
  const c = cohortText(cohort);
  return (
    <li
      data-tenant={tenant.id}
      className="inline-flex h-8 max-w-full min-w-0 animate-fade-in items-center gap-2 rounded-md border border-line-strong pr-1 pl-2 text-small max-lg:h-(--vs-control-touch)"
    >
      <SeriesSwatch slot={slot} />
      <Link
        to={`/operator/projects/${tenant.id}`}
        className="min-w-0 truncate rounded-sm font-medium text-fg underline-offset-4 hover:underline focus-ring"
      >
        {tenant.name}
      </Link>
      {c ? (
        <span data-cohort className="truncate text-caption text-fg-2 max-md:hidden">
          {c}
        </span>
      ) : null}
      {tenant.status !== "active" ? (
        <StatusBadge kind="project" status={tenant.status} operator />
      ) : null}
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        className="max-lg:size-(--vs-control-touch)"
        aria-label={`Remove ${tenant.name} from the comparison`}
        onClick={onRemove}
      >
        <X aria-hidden />
      </Button>
    </li>
  );
}

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex min-w-0 flex-col gap-4">
      <h2 id={id} className="text-section text-fg">
        {title}
      </h2>
      {children}
    </section>
  );
}

function ChartsLoading() {
  return (
    <LoadingState label="Loading the numbers">
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton.Chart />
        <Skeleton.Chart />
      </div>
      <Skeleton.Table rows={3} />
    </LoadingState>
  );
}

// ---- charts

// the one partial key of the page: the hatching that marks the running day
// in every chart
function PartialKey() {
  return (
    <span data-legend="partial" className="flex items-center gap-1 text-caption text-fg-2">
      <svg aria-hidden width="10" height="10" className="rounded-bar">
        <defs>
          <pattern
            id="compare-partial"
            width="4"
            height="4"
            patternUnits="userSpaceOnUse"
            patternTransform="rotate(45)"
          >
            <rect width="4" height="4" className="fill-series-prev" />
            <rect width="1.5" height="4" fill="var(--vs-series-partial)" />
          </pattern>
        </defs>
        <rect width="10" height="10" fill="url(#compare-partial)" />
      </svg>
      Partial: today is still running
    </span>
  );
}

// the axis of a chart uses the same ticks as the chart: from 0 to the
// largest value of every series
function ticksOf(values: (number | null)[][]): number[] {
  return niceTicks(Math.max(0, ...values.flat().map((v) => v ?? 0)));
}

// a rate axis keeps the decimals its steps need: 0.25%, 0.5%, 0.75%, not
// 0.3%, 0.5%, 0.8%
export function percentAxis(values: (number | null)[][]): (n: number) => string {
  const ticks = ticksOf(values).map((t) => t * 100);
  const places =
    [0, 1, 2, 3].find((d) => ticks.every((t) => Math.abs(Number(t.toFixed(d)) - t) < 1e-9)) ?? 4;
  return (n) => (n === 0 ? "0%" : `${(n * 100).toFixed(places)}%`);
}

// a latency axis in one unit: milliseconds while the largest tick is under
// a second, else seconds
export function latencyAxis(values: (number | null)[][]): (n: number) => string {
  const top = ticksOf(values).at(-1) ?? 0;
  if (top < 1) return (n) => `${Math.round(n * 1000)} ms`;
  return (n) => `${Number(n.toFixed(2))} s`;
}

function bucketsOf(days: string[]): TimeBucket[] {
  const today = utcDay();
  return days.map((d) => ({
    at: d,
    label: `${formatDate(d)}, UTC`,
    tick: formatTick(d, "day"),
    partial: d === today,
  }));
}

function markersOf(projects: { tenant: TenantRef; cohort: Cohort }[]) {
  return projects.flatMap((p) =>
    p.cohort.award_date ? [{ at: p.cohort.award_date, label: `${p.tenant.name} awarded` }] : [],
  );
}

// a chart only draws when a complete day has a value: a range whose only
// value is the running day says so, instead of an empty frame
export function chartState(
  buckets: TimeBucket[],
  series: (number | null)[][],
): "data" | "empty" | "no-complete" {
  const has = (i: number) => series.some((s) => s[i] !== null && s[i] !== undefined);
  if (!buckets.some((_, i) => has(i))) return "empty";
  if (!buckets.some((b, i) => !b.partial && has(i))) return "no-complete";
  return "data";
}

function CompareChart({
  title,
  description,
  buckets,
  projects,
  values,
  format,
  axisFormat,
  markers,
  ceiling,
  higherIsBetter = true,
}: {
  title: string;
  description: string;
  buckets: TimeBucket[];
  projects: TenantRef[];
  values: (number | null)[][];
  format: (n: number) => string;
  axisFormat: (n: number) => string;
  markers: { at: string; label: string }[];
  ceiling?: { value: number; label: string };
  higherIsBetter?: boolean;
}) {
  const st = chartState(buckets, values);
  return (
    <TimeChart
      title={title}
      description={description}
      headingLevel={3}
      kind="line"
      buckets={buckets}
      series={projects.map((t, i) => ({
        key: t.id,
        label: t.name,
        tone: SERIES_TONES[i] ?? "series-1",
        values: values[i] ?? [],
      }))}
      format={format}
      axisFormat={axisFormat}
      higherIsBetter={higherIsBetter}
      markers={markers}
      ceiling={ceiling}
      state={st === "data" ? "data" : "empty"}
      legend={false}
      emptyTitle={st === "no-complete" ? "No complete day yet" : "No data in this range"}
      emptyText={
        st === "no-complete"
          ? "Only today has data. Today is still running. The totals below hold its value."
          : "Choose a longer range, or wait for the first data."
      }
      height={180}
    />
  );
}

// ---- web

function WebSection({ data }: { data: WebCompare }) {
  const buckets = bucketsOf(data.days);
  const projects = data.projects.map((p) => p.tenant);
  const markers = markersOf(data.projects);
  const col = (k: "pageviews" | "visitors" | "bounce_rate" | "avg_visit_duration") =>
    data.projects.map((p) => p.series.map((s) => s[k]));
  const notRead = data.projects.some((p) => p.series.some((s) => !s.aggregated));
  const gaps = notRead ? " A gap is a day that is not read yet." : "";
  return (
    <>
      <div className="grid gap-4 lg:grid-cols-2">
        <CompareChart
          title="Page views"
          description={`Page views per UTC day.${gaps}`}
          buckets={buckets}
          projects={projects}
          values={col("pageviews")}
          format={formatCount}
          axisFormat={formatCompact}
          markers={markers}
        />
        <CompareChart
          title="Visitors"
          description={`Visitors per UTC day, unique per site.${gaps}`}
          buckets={buckets}
          projects={projects}
          values={col("visitors")}
          format={formatCount}
          axisFormat={formatCompact}
          markers={markers}
        />
        <CompareChart
          title="Bounce rate"
          description="Visits with one page view and no event. A gap is a day without visits."
          buckets={buckets}
          projects={projects}
          values={col("bounce_rate")}
          format={formatPercent}
          axisFormat={percentAxis(col("bounce_rate"))}
          markers={markers}
          higherIsBetter={false}
        />
        <CompareChart
          title="Average visit"
          description="Time from the first to the last page view, on average."
          buckets={buckets}
          projects={projects}
          values={col("avg_visit_duration")}
          format={formatDuration}
          axisFormat={formatDuration}
          markers={markers}
        />
      </div>
      <TotalsTable
        label="Web totals"
        projects={data.projects}
        columns={[
          { key: "pageviews", header: "Page views", value: (p) => formatCount(p.totals.pageviews) },
          { key: "visitors", header: "Visitors", value: (p) => formatCount(p.totals.visitors) },
          { key: "visits", header: "Visits", value: (p) => formatCount(p.totals.visits) },
          {
            key: "bounce_rate",
            header: "Bounce rate",
            value: (p) =>
              p.totals.bounce_rate === null ? (
                <Missing reason="No visits in this range" />
              ) : (
                formatPercent(p.totals.bounce_rate)
              ),
          },
          {
            key: "avg",
            header: "Average visit",
            value: (p) =>
              p.totals.avg_visit_duration === null ? (
                <Missing reason="No visits in this range" />
              ) : (
                formatDuration(p.totals.avg_visit_duration)
              ),
          },
          {
            key: "held",
            header: "Held back",
            definition:
              "Page views from a network that sent too many visitors in one hour. They are not in the other numbers.",
            value: (p) =>
              p.totals.quarantine_share === null ? (
                <Missing reason="No page views in this range" />
              ) : (
                `${formatPercent(p.totals.quarantine_share)} (${formatCount(p.totals.quarantine_pageviews)})`
              ),
          },
        ]}
        mobile={["pageviews", "visitors", "bounce_rate"]}
      />
      <CompareIntegrity projects={data.projects} />
      <Disclosure title="Referrers, countries, devices, browsers and systems">
        <div className="flex flex-col gap-6">
          {DIMENSIONS.map((d) => (
            <section key={d.dim} aria-label={d.label} className="flex flex-col gap-3">
              <h3 className="text-title text-fg">{d.label}</h3>
              {d.dim === "country" ? <DbIpAttribution /> : null}
              <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
                {data.projects.map((p, i) => {
                  const b = p.breakdowns[d.dim];
                  return (
                    <div key={p.tenant.id} className="flex min-w-0 flex-col gap-2">
                      <p className="flex min-w-0 items-center gap-2 text-small font-medium text-fg">
                        <SeriesSwatch slot={i} />
                        <span className="truncate">{p.tenant.name}</span>
                      </p>
                      <BreakdownList
                        label={`${d.label} of ${p.tenant.name}`}
                        valueLabel="Visitors"
                        state={b.items.length === 0 ? "empty" : "data"}
                        emptyTitle="None in this range"
                        emptyText={
                          d.dim === "referrer"
                            ? "Only other sites count as referrers."
                            : "No visitors in this range."
                        }
                        limit={5}
                        items={b.items.map((x) => {
                          const text = d.dim === "country" ? countryName(x.key) : x.key;
                          return { key: x.key, label: text, text, value: x.value };
                        })}
                      />
                      {b.total_keys > b.items.length ? (
                        <p className="text-caption text-fg-2">
                          {countOf(b.total_keys - b.items.length, "more value")} not shown
                        </p>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      </Disclosure>
    </>
  );
}

// ---- api

// a percentile above 10 s has no number: it is drawn on the 10 s line and
// read as ">10 s"
const OVER = 10.000001;

// the 10 s line, drawn only when a value is above it
const CEILING = { value: 10, label: ">10 s above" };

function pct(n: ApiNumbers, k: "p50" | "p95" | "p99"): number | null {
  if (n[`${k}_over_10s`]) return OVER;
  return n[`${k}_seconds`];
}

function latencyCell(n: ApiNumbers, k: "p50" | "p95" | "p99") {
  const v = pct(n, k);
  if (v === null) return <Missing reason="No requests in this range" />;
  return v > 10 ? OPEN_BUCKET : formatLatency(v);
}

function rateCell(v: number | null) {
  return v === null ? <Missing reason="No requests in this range" /> : formatPercent(v);
}

function ApiSection({ data }: { data: ApiCompare }) {
  const buckets = bucketsOf(data.days);
  const projects = data.projects.map((p) => p.tenant);
  const markers = markersOf(data.projects);
  const col = (f: (s: ApiCompareProject["series"][number]) => number | null) =>
    data.projects.map((p) => p.series.map(f));
  const p95 = col((s) => pct(s, "p95"));
  return (
    <>
      <div className="grid gap-4 lg:grid-cols-2">
        <CompareChart
          title="Requests"
          description="Requests per UTC day over every service."
          buckets={buckets}
          projects={projects}
          values={col((s) => s.requests)}
          format={formatCount}
          axisFormat={formatCompact}
          markers={markers}
        />
        <CompareChart
          title="p95 latency"
          description="The duration that 95% of requests stay under. A gap is a day without requests."
          buckets={buckets}
          projects={projects}
          values={p95}
          format={formatLatency}
          axisFormat={latencyAxis(p95)}
          markers={markers}
          ceiling={p95.flat().some((v) => v !== null && v > 10) ? CEILING : undefined}
          higherIsBetter={false}
        />
        <CompareChart
          title="4xx rate"
          description="Requests answered with a 4xx status. A gap is a day without requests."
          buckets={buckets}
          projects={projects}
          values={col((s) => s.rate_4xx)}
          format={formatPercent}
          axisFormat={percentAxis(col((s) => s.rate_4xx))}
          markers={markers}
          higherIsBetter={false}
        />
        <CompareChart
          title="5xx rate"
          description="Requests answered with a 5xx status. A gap is a day without requests."
          buckets={buckets}
          projects={projects}
          values={col((s) => s.rate_5xx)}
          format={formatPercent}
          axisFormat={percentAxis(col((s) => s.rate_5xx))}
          markers={markers}
          higherIsBetter={false}
        />
      </div>
      <TotalsTable
        label="API totals"
        projects={data.projects}
        columns={[
          { key: "requests", header: "Requests", value: (p) => formatCount(p.totals.requests) },
          {
            key: "infra",
            header: "Infrastructure",
            definition:
              "Requests left out of the other numbers: health checks, probes and CORS checks.",
            value: (p) => formatCount(p.totals.infrastructure_requests),
          },
          { key: "rate_4xx", header: "4xx rate", value: (p) => rateCell(p.totals.rate_4xx) },
          { key: "rate_5xx", header: "5xx rate", value: (p) => rateCell(p.totals.rate_5xx) },
          { key: "p50", header: "p50", value: (p) => latencyCell(p.totals, "p50") },
          { key: "p95", header: "p95", value: (p) => latencyCell(p.totals, "p95") },
          { key: "p99", header: "p99", value: (p) => latencyCell(p.totals, "p99") },
          { key: "services", header: "Services", value: (p) => formatCount(p.totals.services) },
          { key: "routes", header: "Routes", value: (p) => formatCount(p.totals.routes) },
        ]}
        mobile={["requests", "rate_5xx", "p95"]}
      />
      <Disclosure title="Services of each project">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {data.projects.map((p, i) => (
            <ServicesCard key={p.tenant.id} project={p} slot={i} />
          ))}
        </div>
      </Disclosure>
    </>
  );
}

function ServicesCard({ project, slot }: { project: ApiCompareProject; slot: number }) {
  const t = project.totals;
  return (
    <section
      aria-label={`Services of ${project.tenant.name}`}
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4"
    >
      <p className="flex min-w-0 items-center gap-2 text-body font-medium text-fg">
        <SeriesSwatch slot={slot} />
        <span className="truncate">{project.tenant.name}</span>
      </p>
      <p className="text-caption text-fg-2">
        <SourceText source={t.source} spanServices={t.span_services} services={t.services} />
        {t.lossy ? ". Some latency buckets are approximate." : null}
      </p>
      {project.services.length === 0 ? (
        <p className="text-small text-fg-2">No requests in this range.</p>
      ) : (
        <ul className="flex flex-col">
          {project.services.map((s) => (
            <li key={s.service} className="border-b border-line-subtle py-2 last:border-0">
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate font-mono text-caption text-fg">
                  {s.service === "" ? "Service without a name" : s.service}
                </span>
                <span className="shrink-0 font-mono text-small text-fg tabular">
                  {formatCount(s.requests)}
                </span>
              </div>
              <p className="text-caption text-fg-2">
                {countOf(s.routes, "route")}, 5xx {rateText(s.rate_5xx)}, p95 {latencyText(s)}
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

function rateText(v: number | null): string {
  return v === null ? "not measured" : formatPercent(v);
}

function latencyText(n: ApiNumbers): string {
  const v = pct(n, "p95");
  if (v === null) return "not measured";
  return v > 10 ? OPEN_BUCKET : formatLatency(v);
}

// ---- shared

type TotalsRow = { tenant: TenantRef };

// the range totals: one row per project in its series slot, one column per
// metric, so a column reads down across the projects
function TotalsTable<R extends TotalsRow>({
  label,
  projects,
  columns,
  mobile,
}: {
  label: string;
  projects: R[];
  columns: { key: string; header: string; definition?: string; value: (p: R) => ReactNode }[];
  mobile: string[];
}) {
  const slots = new Map(projects.map((p, i) => [p.tenant.id, i]));
  const cols: Column<R>[] = [
    {
      key: "name",
      header: "Project",
      primary: true,
      minWidth: "min-w-48",
      className: "max-w-72",
      cell: (p) => (
        <span className="flex min-w-0 items-center gap-2">
          <SeriesSwatch slot={slots.get(p.tenant.id) ?? -1} />
          <span className="truncate">{p.tenant.name}</span>
        </span>
      ),
    },
    ...columns.map((c) => ({
      key: c.key,
      header: c.header,
      definition: c.definition,
      align: "right" as const,
      numeric: true,
      mobile: mobile.includes(c.key),
      cell: c.value,
    })),
  ];
  return (
    <DataTable
      label={label}
      columns={cols}
      rows={projects}
      getRowKey={(p) => p.tenant.id}
      rowLabel={(p) => p.tenant.name}
      rowHref={(p) => `/operator/projects/${p.tenant.id}`}
      rowProps={(p) => ({ "data-tenant": p.tenant.id })}
    />
  );
}

function Disclosure({ title, children }: { title: string; children: ReactNode }) {
  return (
    <details className="group rounded-lg border border-line bg-raised">
      <summary className="flex cursor-pointer items-center justify-between gap-3 rounded-lg px-4 py-3 text-body font-medium text-fg transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring max-lg:min-h-(--vs-control-touch)">
        {title}
        <ChevronDown
          aria-hidden
          className="size-4 text-fg-2 transition-transform duration-(--vs-duration-change) ease-standard group-open:rotate-180"
        />
      </summary>
      <div className="border-t border-line p-4">{children}</div>
    </details>
  );
}
