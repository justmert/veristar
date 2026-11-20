// the project's api numbers: requests, error rates and latency of its http
// services per route, for a range of utc minutes, hours or days against the
// previous range of equal length

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Search } from "lucide-react";
import { type ReactNode, useEffect, useId, useState } from "react";
import { Link, useSearchParams } from "react-router";
import {
  apiOverviewQuery,
  apiRoutesQuery,
  apiServicesQuery,
  apiStatusQuery,
  apiTimeseriesQuery,
  apiWarningsQuery,
} from "@/api/queries";
import type {
  ApiOverview,
  ApiPoint,
  ApiRange,
  ApiRouteItem,
  ApiServiceItem,
  ApiStatus,
  ApiWarnings,
  Percentile,
  WarningKind,
} from "@/api/schemas";
import { BreakdownList, type TimeBucket, TimeChart } from "@/components/charts";
import { type Column, DataTable } from "@/components/data-table";
import { NativeSelect } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Panel } from "@/components/panel";
import { parseUtcDayTime, RangeControl } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { StatCard, StatGrid } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { ZoomBlock, type ZoomGroup, type ZoomPart } from "@/components/zoom-block";
import {
  CUSTOM_MAX_DAYS,
  CUSTOM_MIN_DAY,
  type DashboardState,
  defaultOrder,
  formatBucket,
  formatRange,
  instantToField,
  isPartialBucket,
  latencyText,
  METHODS,
  OPEN_BUCKET_CEILING,
  PAGE_SIZE,
  PRESETS,
  percentileChange,
  plotPercentile,
  RESOLUTION_LABEL,
  RESOLUTION_NOTE,
  rateChange,
  rateText,
  readState,
  relativeChange,
  routesQuery,
  tickFormatter,
  toStatChange,
  validateCustomEnd,
  writeState,
} from "@/lib/api-metrics";
import {
  formatCount,
  formatDate,
  formatDateTimeUtc,
  formatLatency,
  formatPercent,
  OPEN_BUCKET,
} from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { useProject } from "@/pages/project-layout";
import { ApiSkeleton, PAGE_HEADERS } from "./page-frame";

// contract 7.3: the one line shown with every trace-derived badge
export const SPAN_NOTE =
  "Derived from sampled traces; counts are lower than the real request count when traces are sampled.";
// contract 7.3 span_data_ignored: the service has metric rows in the range
// and span rows that were left out. that happens when it switched from
// traces to metrics during the range, and also for every service that
// exports both signals, so the note names both cases
export const IGNORED_NOTE =
  "Counted from its metrics for the whole range. Numbers derived from its traces (sent alongside the metrics, or before it started sending metrics) are left out.";

const CADENCE = "Hourly and daily counts refresh every few minutes. Minute counts are live.";

export function ApiPage() {
  const { tenant } = useProject();
  const [params, setParams] = useSearchParams();
  const state = readState(params);
  const set = (next: DashboardState) => setParams(writeState(next));

  const services = useQuery(apiServicesQuery(tenant.id, state.range));
  const warnings = useQuery(apiWarningsQuery(tenant.id, state.range));
  const range = services.data?.range ?? null;
  const items = services.data?.items ?? [];
  const docs = siblingUrl(window.location.hostname, "docs");

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        {...PAGE_HEADERS.api}
        freshness={
          services.dataUpdatedAt ? { at: new Date(services.dataUpdatedAt), cadence: CADENCE } : null
        }
        items={rangeItems(range, state.range, services.isError)}
        secondary={<RangePicker state={state} set={set} />}
      />

      <Filters state={state} set={set} services={items} filtersOff={services.isError} />

      <DataNotes
        warnings={warnings.data ?? null}
        services={items}
        service={state.filters.service}
      />
      {warnings.error ? (
        <ErrorState
          compact
          title="The notes about these numbers did not load"
          error={warnings.error}
          onRetry={() => warnings.refetch()}
        />
      ) : null}

      {services.isPending ? (
        <ApiSkeleton />
      ) : services.error ? (
        <ErrorState
          title="The services did not load"
          error={services.error}
          onRetry={() => services.refetch()}
        />
      ) : items.length === 0 ? (
        <NoData tenantId={tenant.id} range={range} docs={docs} />
      ) : (
        <Dashboard tenantId={tenant.id} state={state} set={set} services={items} />
      )}
    </div>
  );
}

// the range in words and its resolution, each its own item of the meta
// line, and why the resolution is coarser than asked, when it is
function rangeItems(
  range: ApiRange | null,
  asked: DashboardState["range"],
  failed: boolean,
): ReactNode[] {
  if (range)
    return [
      <span key="range" aria-live="polite" data-range-line="">
        {formatRange(range)}
      </span>,
      <span key="res">{RESOLUTION_LABEL[range.resolution]}</span>,
      range.resolution_note ? (
        <span key="note">{RESOLUTION_NOTE[range.resolution_note]}</span>
      ) : null,
    ];
  if (!failed) return [];
  const preset = PRESETS.find((p) => p.value === asked.range);
  return [
    <span key="range">
      {preset
        ? `Last ${preset.label}, UTC`
        : asked.range === "custom"
          ? `${formatDateTimeUtc(asked.from)} to ${formatDateTimeUtc(asked.to)}`
          : "UTC"}
    </span>,
  ];
}

// the range control in the header: the range in words is a meta item, so
// the words beside the control are hidden (see shared-requests.md)
function RangePicker({ state, set }: { state: DashboardState; set: (s: DashboardState) => void }) {
  return (
    <RangeControl
      value={state.range.range}
      presets={PRESETS}
      onPreset={(preset) => set({ ...state, range: { range: preset } })}
      custom={
        state.range.range === "custom"
          ? { from: instantToField(state.range.from), to: instantToField(state.range.to) }
          : null
      }
      onCustom={(r) => set({ ...state, range: { range: "custom", from: r.from, to: r.to } })}
      time
      minDay={CUSTOM_MIN_DAY}
      maxDays={CUSTOM_MAX_DAYS}
      validate={(r) => {
        const to = parseUtcDayTime(r.to);
        const e = to ? validateCustomEnd(to, new Date()) : null;
        return e ? { to: e } : {};
      }}
      className="[&>div:first-child>span]:hidden"
    />
  );
}

const FIGURES = ["Requests", "4xx rate", "5xx rate", "p95 latency"];

// no service sent anything in the range: the page is its first step
function NoData({
  tenantId,
  range,
  docs,
}: {
  tenantId: string;
  range: ApiRange | null;
  docs: string | null;
}) {
  return (
    <>
      <StatGrid aria-label="Summary" min="10rem">
        {FIGURES.map((l) => (
          <StatCard key={l} label={l} value={null} missingReason="No requests in this range" />
        ))}
      </StatGrid>
      <Panel
        title={
          <span className="flex items-baseline gap-2">
            Requests
            {range ? (
              <span className="text-small font-normal text-fg-3">
                {RESOLUTION_LABEL[range.resolution]}
              </span>
            ) : null}
          </span>
        }
      >
        <EmptyState
          title="No API requests in this range"
          className="min-h-80"
          action={
            <span className="flex flex-wrap items-center justify-center gap-2">
              <Button asChild>
                <Link to={`/projects/${tenantId}/connect-api`}>Connect API</Link>
              </Button>
              {docs ? (
                <Button asChild variant="ghost">
                  <a href={`${docs}/onboarding/api/quickstart`}>Docs</a>
                </Button>
              ) : null}
            </span>
          }
        />
      </Panel>
    </>
  );
}

// ---- filters: services as chips, the method as a switch, the route as a
// search field; one row, applied at once

// values are prefixed, so a method named like a sentinel cannot collide
// with "all"
const ALL = "all";
const opt = (v: string) => `v:${v}`;
const unopt = (v: string) => (v === ALL ? null : v.slice(2));

function serviceName(s: string): string {
  return s === "" ? "No service name" : s;
}

function methodName(m: string): string {
  return m === "_OTHER" ? "Other" : m === "" ? "(empty)" : m;
}

// the methods the switch shows; the others are in the select beside it
const MAIN_METHODS = ["GET", "POST", "PUT", "DELETE"];

// the same chip as the site chips of the overview and web pages
const CHIP =
  "inline-flex h-8 max-w-full items-center gap-2 rounded-full border px-3 text-small transition-colors duration-(--vs-duration-press) focus-ring max-lg:h-(--vs-control-touch)";

function chipTone(on: boolean): string {
  return on
    ? "border-line-focus bg-selected text-fg"
    : "border-line bg-raised text-fg-2 hover:text-fg";
}

function Filters({
  state,
  set,
  services,
  filtersOff = false,
}: {
  state: DashboardState;
  set: (s: DashboardState) => void;
  services: ApiServiceItem[];
  // the filters choose among the services, so they wait while those failed
  filtersOff?: boolean;
}) {
  const f = state.filters;
  const names = services.map((s) => s.service);
  // a filtered service with no data in this range still shows as chosen
  if (f.service !== null && !names.includes(f.service)) names.push(f.service);
  const byName = new Map(services.map((s) => [s.service, s]));
  const setFilter = (k: keyof typeof f, v: string | null) =>
    set({ ...state, filters: { ...f, [k]: v } });
  const main = [...MAIN_METHODS];
  if (f.method !== null && !main.includes(f.method)) main.push(f.method);
  const more = METHODS.filter((m) => !main.includes(m));
  const any = f.service !== null || f.method !== null || f.route !== null;

  return (
    <fieldset
      disabled={filtersOff}
      className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-2 disabled:opacity-60"
    >
      <legend className="sr-only">Filters</legend>
      <fieldset className="flex min-w-0 flex-wrap items-center gap-1.5">
        <legend className="sr-only">Service</legend>
        <button
          type="button"
          aria-pressed={f.service === null}
          onClick={() => setFilter("service", null)}
          className={cn(CHIP, chipTone(f.service === null))}
        >
          All services
        </button>
        {names.map((n) => (
          <button
            key={n}
            type="button"
            aria-pressed={f.service === n}
            data-service-chip={n}
            onClick={() => setFilter("service", f.service === n ? null : n)}
            className={cn(CHIP, chipTone(f.service === n))}
          >
            <span className="truncate font-mono text-[12.5px]">{serviceName(n)}</span>
            {byName.get(n)?.span_data_ignored ? (
              <span className="text-caption text-fg-3">metrics only</span>
            ) : null}
          </button>
        ))}
      </fieldset>
      <span aria-hidden className="mx-1.5 h-5 w-px bg-line-strong max-sm:hidden" />
      <Segmented
        label="Method"
        value={f.method === null ? ALL : opt(f.method)}
        options={[
          { value: ALL, label: "All" },
          ...main.map((m) => ({ value: opt(m), label: methodName(m) })),
        ]}
        onChange={(v) => setFilter("method", unopt(v))}
        className="max-w-full overflow-x-auto"
      />
      {more.length > 0 ? (
        <NativeSelect
          aria-label="More methods"
          size="sm"
          value=""
          onChange={(e) => {
            if (e.target.value) setFilter("method", unopt(e.target.value));
          }}
          className="w-auto"
        >
          <option value="">More</option>
          {more.map((m) => (
            <option key={m} value={opt(m)}>
              {methodName(m)}
            </option>
          ))}
        </NativeSelect>
      ) : null}
      {any ? (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => set({ ...state, filters: { service: null, route: null, method: null } })}
        >
          Clear filters
        </Button>
      ) : null}
      <RouteField value={f.route} onChange={(v) => setFilter("route", v)} />
    </fieldset>
  );
}

// the route filter: a route matches exactly, as stored, with its
// placeholders. it applies when typing pauses, and at once on enter.
const ROUTE_PAUSE = 500;

function RouteField({
  value,
  onChange,
}: {
  value: string | null;
  onChange: (v: string | null) => void;
}) {
  const id = useId();
  const [draft, setDraft] = useState(value ?? "");
  useEffect(() => setDraft(value ?? ""), [value]);
  const next = draft.trim() === "" ? null : draft.trim();
  useEffect(() => {
    if (next === value) return;
    const t = setTimeout(() => onChange(next), ROUTE_PAUSE);
    return () => clearTimeout(t);
  }, [next, value, onChange]);
  return (
    <div className="relative flex min-w-0 items-center max-sm:w-full sm:ml-auto sm:w-64">
      <label htmlFor={id} className="sr-only">
        Route
      </label>
      <Search aria-hidden className="pointer-events-none absolute left-2.5 size-3.5 text-fg-3" />
      <input
        id={id}
        type="search"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && next !== value) onChange(next);
        }}
        placeholder="Route, e.g. /accounts/{id}"
        title="The exact route, with its placeholders"
        spellCheck={false}
        autoComplete="off"
        className="h-8 w-full rounded-lg border border-line bg-raised pr-2.5 pl-8 font-mono text-[12.5px] text-fg placeholder:text-fg-3 focus-ring max-lg:h-(--vs-control-touch)"
      />
    </div>
  );
}

// ---- data notes: one quiet band of what to know about the numbers, only
// when something applies. a secret in a path is a problem, so it stays a
// notice of its own

type ShownWarning = Exclude<WarningKind, "span_derived" | "secret_in_path">;

const WARNING_TEXT: Record<ShownWarning, { tag: string; body: string }> = {
  lossy_histogram: {
    tag: "Approximate",
    body: "A histogram arrived with other bucket bounds than the standard ones. Percentiles marked approximate can be off by one bucket.",
  },
  cardinality_overflow: {
    tag: "Grouped as {other}",
    body: "More distinct routes or services arrived in a day than the daily limit. Make sure that your routes are templates, not raw paths with IDs.",
  },
  coarse_buckets: {
    tag: "Not reliable",
    body: "A histogram arrived with buckets much wider than the standard ones. Record http.server.request.duration in seconds with the standard bucket bounds.",
  },
};

const SECRET_TEXT =
  "This service replaced it before it stored the request. The URL can still be in your own logs and proxies. Treat that key as exposed and move its funds to a new account.";

type WarningTotal = ApiWarnings["totals"][number];

function Times({ t }: { t: WarningTotal }) {
  return (
    <>
      <span className="tabular">
        {formatCount(t.count)} {t.count === 1 ? "time" : "times"}
      </span>
      {t.last_day ? <span>last on {formatDate(t.last_day)}</span> : null}
    </>
  );
}

function DataNotes({
  warnings,
  services,
  service,
}: {
  warnings: ApiWarnings | null;
  services: ApiServiceItem[];
  service: string | null;
}) {
  const shown = (warnings?.totals ?? []).filter((t) => t.kind !== "span_derived" && t.count > 0);
  // trace-derived numbers are said for the services in view
  const scope = service === null ? services : services.filter((s) => s.service === service);
  const traces = scope.filter((s) => s.source === "span");
  if (shown.length === 0 && traces.length === 0) return null;
  return (
    <section aria-label="Data notes" className="flex flex-col gap-2 empty:hidden">
      {shown.some((t) => t.kind !== "secret_in_path") || traces.length > 0 ? (
        <div className="flex flex-col divide-y divide-line-subtle rounded-xl border border-line-strong px-3.5 text-small text-fg-2">
          {traces.length > 0 ? (
            <p data-note="span" className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5">
              <Pill tone="neutral">From traces</Pill>
              <span className="font-mono text-fg">
                {traces.map((s) => serviceName(s.service)).join(", ")}
              </span>
              <span className="min-w-0 flex-1">{SPAN_NOTE}</span>
            </p>
          ) : null}
          {shown
            .filter((t) => t.kind !== "secret_in_path")
            .map((t) => (
              <p
                key={t.kind}
                data-warning={t.kind}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5"
              >
                <Pill tone="neutral">{WARNING_TEXT[t.kind as ShownWarning].tag}</Pill>
                <span className="min-w-0 flex-1">{WARNING_TEXT[t.kind as ShownWarning].body}</span>
                <span className="flex gap-x-3 text-caption text-fg-3">
                  <Times t={t} />
                </span>
              </p>
            ))}
        </div>
      ) : null}
      {shown
        .filter((t) => t.kind === "secret_in_path")
        .map((t) => (
          <div key={t.kind} data-warning={t.kind}>
            <Notice
              tone="critical"
              title="A Stellar secret seed was in a request path"
              role="alert"
            >
              <p>{SECRET_TEXT}</p>
              <p className="mt-1 flex flex-wrap gap-x-3 text-caption text-fg-2">
                <Times t={t} />
              </p>
            </Notice>
          </div>
        ))}
    </section>
  );
}

function TracesBadge() {
  return (
    <Tooltip content={SPAN_NOTE}>
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the note opens on focus too */}
      <span tabIndex={0} className="inline-flex rounded-full focus-ring">
        <Pill tone="neutral">From traces</Pill>
      </span>
    </Tooltip>
  );
}

function ApproxBadge() {
  return <Pill tone="neutral">Approximate</Pill>;
}

// ---- the dashboard

type Metric = "requests" | "4xx" | "5xx" | "p95";

function Dashboard({
  tenantId,
  state,
  set,
  services,
}: {
  tenantId: string;
  state: DashboardState;
  set: (s: DashboardState) => void;
  services: ApiServiceItem[];
}) {
  const overview = useQuery(apiOverviewQuery(tenantId, state.range, state.filters));
  const series = useQuery(apiTimeseriesQuery(tenantId, state.range, state.filters));
  const status = useQuery(apiStatusQuery(tenantId, state.range, state.filters));
  // the previous period, bucket by bucket, for the dashed line: the same
  // timeseries route asked for the previous range the overview names
  const prevRange =
    overview.data && !overview.isPlaceholderData ? overview.data.previous.range : null;
  const prevQuery = useQuery({
    ...apiTimeseriesQuery(
      tenantId,
      prevRange ? { range: "custom", from: prevRange.from, to: prevRange.to } : state.range,
      state.filters,
    ),
    enabled: prevRange !== null,
  });
  const prevData =
    prevRange && prevQuery.data && prevQuery.data.range.from === prevRange.from
      ? prevQuery.data
      : null;
  const previous =
    series.data && prevData ? alignPrevious(series.data.points, series.data.range, prevData) : null;
  const [metric, setMetric] = useState<Metric>("requests");
  // a range of days whose requests all fall on its last day (a new
  // project) is drawn per hour of the last 24 hours: one bar at the end of
  // empty days says nothing
  const narrow = series.data ? lastDayOnly(series.data.points, series.data.range) : false;
  const hours = useQuery({
    ...apiTimeseriesQuery(tenantId, { range: "24h" }, state.filters),
    enabled: narrow,
  });
  const chart = narrow && hours.data ? hours.data : series.data;
  const chartPrevious = narrow ? null : previous;

  return (
    <div className="flex flex-col gap-4">
      {overview.error ? (
        <ErrorState
          title="The API numbers did not load"
          error={overview.error}
          onRetry={() => overview.refetch()}
        />
      ) : (
        <Summary
          overview={overview.data ?? null}
          dim={overview.isPlaceholderData}
          metric={metric}
          onMetric={setMetric}
        />
      )}

      <ApiChart
        key={metric}
        metric={metric}
        state={
          series.isPending || (narrow && hours.isPending)
            ? "loading"
            : series.error
              ? "error"
              : "data"
        }
        error={series.error}
        onRetry={() => series.refetch()}
        range={chart?.range ?? null}
        points={chart?.points ?? []}
        previous={chartPrevious}
        dim={series.isPlaceholderData}
      />

      <RoutesSection tenantId={tenantId} state={state} set={set} />

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <StatusSection
          status={status.data ?? null}
          state={status.isPending ? "loading" : status.error ? "error" : "data"}
          error={status.error}
          onRetry={() => status.refetch()}
          dim={status.isPlaceholderData}
        />
        <ServicesSection services={services} />
      </div>
    </div>
  );
}

// a range per day with requests on its last day only
export function lastDayOnly(points: ApiPoint[], range: ApiRange): boolean {
  if (range.resolution !== "day" || points.length < 2) return false;
  const busy = points.filter((p) => p.requests > 0);
  return busy.length === 1 && busy[0] === points[points.length - 1];
}

// the previous period, bucket by bucket: the same offset into the previous
// range as the current bucket has into the current one. ranges at another
// resolution (a retention step) are not compared bucket by bucket.
export function alignPrevious(
  points: ApiPoint[],
  range: ApiRange,
  previous: { range: ApiRange; points: ApiPoint[] } | null | undefined,
): (ApiPoint | null)[] | null {
  if (!previous || previous.range.resolution !== range.resolution) return null;
  const offset = Date.parse(range.from) - Date.parse(previous.range.from);
  const byAt = new Map(previous.points.map((p) => [Date.parse(p.at), p]));
  return points.map((p) => byAt.get(Date.parse(p.at) - offset) ?? null);
}

// a series with a shape: two buckets or more that differ
export function sparkValues(
  points: ApiPoint[] | null,
  v: (p: ApiPoint) => number | null,
): number[] | null {
  if (!points || points.length < 2) return null;
  const values = points.map((p) => v(p) ?? 0);
  if (Math.max(...values) === Math.min(...values)) return null;
  return values;
}

// the four figures; the selected one draws the chart. the change names the
// previous range it compares with, for assistive technology
function Summary({
  overview,
  dim,
  metric,
  onMetric,
}: {
  overview: ApiOverview | null;
  dim: boolean;
  metric: Metric;
  onMetric: (m: Metric) => void;
}) {
  if (!overview)
    return (
      <StatGrid aria-label="Summary" min="10rem">
        {FIGURES.map((l) => (
          <StatCard key={l} label={l} value={null} state="loading" />
        ))}
      </StatGrid>
    );

  const cur = overview.current.summary;
  const prev = overview.previous.summary;
  const period = `previous range, ${formatRange(overview.previous.range)}`;
  const infra =
    cur.infrastructure_requests > 0
      ? `${formatCount(cur.infrastructure_requests)} health checks and preflights are not included${
          overview.traffic_classes.applied_from
            ? `, kept apart since ${formatDate(overview.traffic_classes.applied_from)}`
            : ""
        }.`
      : null;
  const percent = (k: "p50" | "p99") => {
    const t = latencyText(cur[k]);
    return (
      <span data-tile={k} className="inline-flex items-baseline gap-1">
        <span>{k}</span>
        {t === null ? <Missing reason="No requests in this range" /> : <span>{t}</span>}
      </span>
    );
  };

  return (
    <StatGrid
      aria-label="Summary"
      min="10rem"
      className={cn(
        "transition-opacity duration-(--vs-duration-change) ease-standard",
        dim && "opacity-60",
      )}
    >
      <StatCard
        data-tile="Requests"
        label="Requests"
        // health checks and preflights stay out of the figure; the
        // definition says how many
        definition={`Requests that your services answered. Health checks and preflights are not included.${infra ? ` ${infra}` : ""}`}
        value={cur.requests}
        change={toStatChange(relativeChange(cur.requests, prev.requests), null, period)}
        selected={metric === "requests"}
        onSelect={() => onMetric("requests")}
        footer={
          infra ? (
            <span className="sr-only" data-testid="infrastructure-requests">
              {infra}
            </span>
          ) : null
        }
      />
      {(["4xx", "5xx"] as const).map((k) => {
        const r = k === "4xx" ? cur.rate_4xx : cur.rate_5xx;
        const pr = k === "4xx" ? prev.rate_4xx : prev.rate_5xx;
        const n = k === "4xx" ? cur.errors_4xx : cur.errors_5xx;
        return (
          <StatCard
            key={k}
            data-tile={`${k} rate`}
            label={`${k} rate`}
            definition={`Share of requests answered with a ${k} status.`}
            value={r}
            format={formatPercent}
            missingReason="No requests in this range"
            change={toStatChange(rateChange(r, pr), true, period)}
            selected={metric === k}
            onSelect={() => onMetric(k)}
            hideWhenEmpty
            footer={
              <span className="text-[12.5px] text-fg-3 tabular">
                {formatCount(n)} {k === "4xx" ? "client errors" : "server errors"}
              </span>
            }
          />
        );
      })}
      <StatCard
        data-tile="p95"
        label="p95 latency"
        definition="95 of 100 requests were faster than this. Percentiles are approximate."
        value={latencyText(cur.p95)}
        missingReason="No requests in this range"
        change={toStatChange(percentileChange(cur.p95, prev.p95), true, period)}
        selected={metric === "p95"}
        onSelect={() => onMetric("p95")}
        hideWhenEmpty
        footer={
          <span className="flex flex-wrap items-baseline gap-x-1 text-[12.5px] text-fg-3 tabular">
            {percent("p50")}
            <span>to</span>
            {percent("p99")}
            {cur.lossy ? <span className="ml-2">Approximate</span> : null}
          </span>
        }
      />
    </StatGrid>
  );
}

// ---- the chart of the selected figure

// the open latency bucket is drawn on the 10 s line; a value just above 10
// reads as ">10 s" in the readout and the table, never as an invented time
const OPEN_PLOT = OPEN_BUCKET_CEILING + 0.001;
export const plotLatency = (p: Percentile): number | null => (p === OPEN_BUCKET ? OPEN_PLOT : p);

function ApiChart({
  metric,
  state,
  error,
  onRetry,
  range,
  points,
  previous,
  dim,
}: {
  metric: Metric;
  state: "loading" | "error" | "data";
  error: unknown;
  onRetry: () => void;
  range: ApiRange | null;
  points: ApiPoint[];
  previous: (ApiPoint | null)[] | null;
  dim: boolean;
}) {
  const now = Date.now();
  const tick = range ? tickFormatter(range) : () => "";
  const buckets: TimeBucket[] = range
    ? points.map((p) => ({
        at: p.at,
        tick: tick(p.at),
        // a bucket from a re-bucketed histogram says so where its values are read
        label:
          metric === "p95" && p.lossy
            ? `${formatBucket(p.at, range)}, approximate`
            : formatBucket(p.at, range),
        partial: isPartialBucket(p.at, range, now),
      }))
    : [];
  const total = points.reduce((a, p) => a + p.requests, 0);
  const chartState = state === "data" && total === 0 ? "empty" : state;
  const per = range ? RESOLUTION_LABEL[range.resolution] : "";
  const common = {
    buckets,
    state: chartState,
    error,
    onRetry,
    emptyTitle: "No requests in this range",
    emptyText: "Requests show here a few minutes after your services answer them.",
    className: cn(
      "transition-opacity duration-(--vs-duration-change) ease-standard",
      dim && "opacity-60",
    ),
  } as const;

  if (metric === "requests")
    return (
      <TimeChart
        key={chartState}
        {...common}
        title={`Requests ${per}`}
        series={[
          {
            key: "requests",
            label: "Requests",
            tone: "series-3",
            values: points.map((p) => p.requests),
          },
        ]}
        previous={previous?.map((q) => (q ? q.requests : null)) ?? null}
      />
    );

  if (metric === "4xx" || metric === "5xx") {
    const order = metric === "4xx" ? (["4xx", "5xx"] as const) : (["5xx", "4xx"] as const);
    return (
      <TimeChart
        key={chartState}
        {...common}
        kind="line"
        title={`Error rates ${per}`}
        series={order.map((k) => ({
          key: k,
          label: `${k} rate`,
          tone: k === "4xx" ? ("warning" as const) : ("critical" as const),
          values: points.map((p) => (k === "4xx" ? p.rate_4xx : p.rate_5xx)),
        }))}
        previous={
          previous?.map((q) => (q ? (metric === "4xx" ? q.rate_4xx : q.rate_5xx) : null)) ?? null
        }
        previousLabel={`${metric} rate, previous period`}
        format={formatPercent}
        axisFormat={formatPercent}
        higherIsBetter={false}
      />
    );
  }

  const anyOpen = points.some((p) => [p.p50, p.p95, p.p99].includes(OPEN_BUCKET));
  const lines = [
    ["p95", "series-3"],
    ["p50", "series-1"],
    ["p99", "series-2"],
  ] as const;
  return (
    <TimeChart
      key={chartState}
      {...common}
      kind="line"
      title={`Latency ${per}`}
      series={lines.map(([k, tone]) => ({
        key: k,
        label: k,
        tone,
        values: points.map((p) => plotLatency(p[k])),
      }))}
      previous={previous?.map((q) => (q ? plotLatency(q.p95) : null)) ?? null}
      previousLabel="p95, previous period"
      format={formatLatency}
      axisFormat={formatLatency}
      higherIsBetter={false}
      ceiling={anyOpen ? { value: OPEN_BUCKET_CEILING, label: "Above 10 s" } : null}
    />
  );
}

// ---- routes

// the page's most failing and slowest routes. the slowest p95 is drawn in
// the warning colour, so the one to look at stands out without a badge
export function standouts(items: ApiRouteItem[]) {
  const num = (p: Percentile) => plotPercentile(p) ?? -1;
  let failing: ApiRouteItem | null = null;
  let slow: ApiRouteItem | null = null;
  for (const r of items) {
    const f = r.rate_5xx ?? 0;
    if (f > 0 && (!failing || f > (failing.rate_5xx ?? 0))) failing = r;
    if (num(r.p95) > 0 && (!slow || num(r.p95) > num(slow.p95))) slow = r;
  }
  // a mark only means something when one route stands apart from the rest
  const top = slow ? num(slow.p95) : -1;
  const unique = items.filter((r) => num(r.p95) === top).length === 1;
  const topFail = failing?.rate_5xx ?? 0;
  const uniqueFail = items.filter((r) => (r.rate_5xx ?? 0) === topFail).length === 1;
  return {
    failing: uniqueFail ? failing : null,
    slow: items.length > 1 && unique ? slow : null,
  };
}

// rates at or above these marks are coloured; the legend above the table
// says so, so the colour never stands alone
export const RATE_4XX_MARK = 0.05;

export function rateTone(k: "4xx" | "5xx", r: number | null): string | undefined {
  if (r === null) return undefined;
  if (k === "4xx") return r >= RATE_4XX_MARK ? "text-warning" : undefined;
  return r > 0 ? "text-critical" : undefined;
}

// where a latency sits on the log axis from 1 ms to 10 s, as a share of
// the axis; the open bucket sits at its end
export function latencyAt(p: Percentile): number | null {
  if (p === OPEN_BUCKET) return 1;
  if (p === null || p <= 0) return null;
  return Math.min(1, Math.max(0, (Math.log10(p) + 3) / 4));
}

const routeKey = (r: ApiRouteItem) => `${r.service}\u0000${r.method}\u0000${r.route}`;

// the latency of a route as a range on a log scale: p50 to p99, with p95
// marked. the exact values show on hover and to assistive technology
function LatencyRange({ r }: { r: ApiRouteItem }) {
  const lo = latencyAt(r.p50);
  const hi = latencyAt(r.p99);
  const mid = latencyAt(r.p95);
  const text = (p: Percentile) => latencyText(p) ?? "none";
  const words = `p50 ${text(r.p50)}, p95 ${text(r.p95)}, p99 ${text(r.p99)}`;
  return (
    <Tooltip content={words}>
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the exact values open on focus too */}
      <span tabIndex={0} className="relative block h-[18px] w-full min-w-24 rounded-sm focus-ring">
        <span className="sr-only">{words}</span>
        <span aria-hidden className="absolute inset-x-0 top-2 h-0.5 rounded-[1px] bg-line" />
        {lo !== null && hi !== null ? (
          <span
            aria-hidden
            className="absolute top-1.5 h-1.5 rounded-[3px] bg-line-strong"
            // positions are data, so they cannot be classes
            style={{ left: `${lo * 100}%`, width: `${Math.max(1, (hi - lo) * 100)}%` }}
          />
        ) : null}
        {mid !== null ? (
          <span
            aria-hidden
            className="absolute top-0.5 -ml-[1.5px] h-3.5 w-[3px] rounded-[2px] bg-fg"
            style={{ left: `${mid * 100}%` }}
          />
        ) : null}
      </span>
    </Tooltip>
  );
}

const LATENCY_SORTS = ["p50", "p95", "p99"] as const;

function RoutesSection({
  tenantId,
  state,
  set,
}: {
  tenantId: string;
  state: DashboardState;
  set: (s: DashboardState) => void;
}) {
  // cursors of the pages before the current one, for going back (the
  // contract's cursors only point forward). a new range, filter or sort
  // starts again at the first page.
  const sig = JSON.stringify([state.range, state.filters, state.sort, state.order]);
  const [nav, setNav] = useState<{ sig: string; cursors: (string | null)[] }>({
    sig,
    cursors: [null],
  });
  const cursors = nav.sig === sig ? nav.cursors : [null];
  const cursor = cursors[cursors.length - 1] ?? null;
  const routes = useQuery(
    apiRoutesQuery(tenantId, state.range, state.filters, routesQuery(state, cursor)),
  );
  const page = cursors.length - 1;
  const data = routes.data;
  const marks = data ? standouts(data.items) : { failing: null, slow: null };
  const max = Math.max(1, ...(data?.items ?? []).map((r) => r.requests));
  // the latency column sorts by the percentile chosen beside the legend
  const latencySort = (LATENCY_SORTS as readonly string[]).includes(state.sort)
    ? state.sort
    : "p95";

  const rate = (k: "4xx" | "5xx") => (r: ApiRouteItem) => {
    const v = k === "4xx" ? r.rate_4xx : r.rate_5xx;
    const n = k === "4xx" ? r.errors_4xx : r.errors_5xx;
    const t = rateText(v);
    return t === null ? (
      <Missing reason="No requests" />
    ) : (
      <Tooltip content={`${formatCount(n)} ${k === "4xx" ? "client" : "server"} errors`}>
        <span className={rateTone(k, v) ?? "text-fg-2"}>{t}</span>
      </Tooltip>
    );
  };

  const columns: Column<ApiRouteItem>[] = [
    {
      key: "method",
      header: "Method",
      className: "w-16",
      cell: (r) => (
        <span className="inline-flex w-fit rounded-[5px] border border-line-strong px-[7px] py-0.5 font-mono text-[11.5px] font-medium text-fg-2">
          {methodName(r.method)}
        </span>
      ),
    },
    {
      key: "route",
      header: "Route",
      sortKey: "route",
      primary: true,
      minWidth: "min-w-64",
      cell: (r) => (
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 py-2">
          <span className="min-w-0 font-mono break-all text-fg">{r.route}</span>
          <span className="rounded-full border border-line-strong px-[7px] py-px font-mono text-[11.5px] whitespace-nowrap text-fg-2">
            {serviceName(r.service)}
          </span>
          {r.source === "span" ? <TracesBadge /> : null}
          {r.lossy ? <ApproxBadge /> : null}
          {r.infrastructure_requests > 0 ? (
            <span className="text-caption text-fg-3">
              {formatCount(r.infrastructure_requests)} health checks not included
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: "requests",
      header: "Requests",
      sortKey: "requests",
      minWidth: "min-w-48",
      mobile: true,
      cell: (r) => (
        <span className="flex items-center gap-2.5">
          <span
            aria-hidden
            className="h-1.5 flex-1 overflow-hidden rounded-[3px] bg-line max-sm:hidden"
          >
            <i
              className="block h-full rounded-[3px] bg-series-3"
              // the width is data
              style={{ width: `${Math.max(1, (r.requests / max) * 100)}%` }}
            />
          </span>
          <span className="min-w-16 text-right tabular">{formatCount(r.requests)}</span>
        </span>
      ),
    },
    {
      key: "rate_4xx",
      header: "4xx",
      sortKey: "rate_4xx",
      align: "right",
      numeric: true,
      minWidth: "min-w-16",
      cell: rate("4xx"),
    },
    {
      key: "rate_5xx",
      header: "5xx",
      sortKey: "rate_5xx",
      align: "right",
      numeric: true,
      minWidth: "min-w-16",
      mobile: true,
      cell: rate("5xx"),
    },
    {
      key: "latency",
      header: "Latency",
      sortKey: latencySort,
      definition:
        "From p50 to p99 on a log scale from 1 ms to 10 s, with p95 marked. The number is the p95.",
      minWidth: "min-w-56",
      mobile: true,
      cell: (r) => (
        <span className="flex items-center gap-3">
          <span className="flex-1 max-sm:hidden">
            <LatencyRange r={r} />
          </span>
          <span
            className={cn(
              "min-w-16 text-right tabular",
              marks.slow === r ? "text-warning" : "text-fg",
            )}
          >
            {latencyText(r.p95) ?? <Missing reason="No requests" />}
          </span>
        </span>
      ),
    },
  ];

  return (
    <section aria-labelledby="api-routes" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h2 id="api-routes" className="flex items-baseline gap-2 text-body font-semibold text-fg">
          Routes
          {data ? (
            <span className="text-small font-normal text-fg-3 tabular">
              {formatCount(data.total)}
            </span>
          ) : null}
        </h2>
        <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-caption text-fg-2">
          <ul aria-label="Legend" className="flex flex-wrap items-center gap-x-3.5 gap-y-1">
            <li className="flex items-center gap-1.5">
              <span aria-hidden className="size-2 rounded-full bg-warning" />
              4xx 5% or more
            </li>
            <li className="flex items-center gap-1.5">
              <span aria-hidden className="size-2 rounded-full bg-critical" />
              any 5xx
            </li>
            <li className="flex items-center gap-1.5">
              <span aria-hidden className="h-1.5 w-3.5 rounded-[3px] bg-line-strong" />
              p50 to p99
            </li>
            <li className="flex items-center gap-1.5">
              <span aria-hidden className="h-3 w-[3px] rounded-[2px] bg-fg" />
              p95
            </li>
          </ul>
          <NativeSelect
            aria-label="Sort latency by"
            size="sm"
            value={latencySort}
            onChange={(e) => {
              const sort = e.target.value as DashboardState["sort"];
              set({ ...state, sort, order: defaultOrder(sort) });
            }}
            className="w-auto"
          >
            {LATENCY_SORTS.map((p) => (
              <option key={p} value={p}>
                Sort by {p}
              </option>
            ))}
          </NativeSelect>
        </div>
      </div>
      <DataTable
        label="Routes"
        columns={columns}
        rows={data?.items ?? []}
        getRowKey={routeKey}
        rowLabel={(r) => `${r.method} ${r.route} of ${serviceName(r.service)}`}
        rowProps={(r) => ({
          "data-route": r.route,
          "data-service": r.service,
          "data-method": r.method,
        })}
        rowActions={(r) => [
          {
            label: "Filter to this route",
            onSelect: () =>
              set({ ...state, filters: { service: r.service, method: r.method, route: r.route } }),
          },
        ]}
        sort={{ key: state.sort, order: state.order }}
        onSort={(key) => {
          const sort = key as DashboardState["sort"];
          const order =
            state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : defaultOrder(sort);
          set({ ...state, sort, order });
        }}
        state={routes.isPending ? "loading" : routes.error ? "error" : "data"}
        error={routes.error}
        onRetry={() => routes.refetch()}
        empty={{ title: "No routes match" }}
        footer={
          data
            ? {
                kind: "pager",
                page,
                pageSize: PAGE_SIZE,
                total: data.total,
                onPage: (to) => {
                  if (to < page) setNav({ sig, cursors: cursors.slice(0, -1) });
                  // a page still loading has no cursor of its own yet
                  else if (!routes.isPlaceholderData && data.next_cursor !== null)
                    setNav({ sig, cursors: [...cursors, data.next_cursor] });
                },
              }
            : undefined
        }
        className={cn(
          "transition-opacity duration-(--vs-duration-change) ease-standard",
          routes.isPlaceholderData && "opacity-60",
        )}
      />
    </section>
  );
}

// ---- status codes and services

// the reason phrase of the common codes, so a code is named where it shows
const REASON: Record<number, string> = {
  400: "Bad Request",
  401: "Unauthorized",
  402: "Payment Required",
  403: "Forbidden",
  404: "Not Found",
  405: "Method Not Allowed",
  406: "Not Acceptable",
  408: "Request Timeout",
  409: "Conflict",
  410: "Gone",
  411: "Length Required",
  412: "Precondition Failed",
  413: "Content Too Large",
  414: "URI Too Long",
  415: "Unsupported Media Type",
  416: "Range Not Satisfiable",
  417: "Expectation Failed",
  421: "Misdirected Request",
  422: "Unprocessable Content",
  423: "Locked",
  425: "Too Early",
  426: "Upgrade Required",
  428: "Precondition Required",
  429: "Too Many Requests",
  431: "Request Header Fields Too Large",
  451: "Unavailable For Legal Reasons",
  499: "Client Closed Request",
  500: "Internal Server Error",
  501: "Not Implemented",
  502: "Bad Gateway",
  503: "Service Unavailable",
  504: "Gateway Timeout",
  505: "HTTP Version Not Supported",
  507: "Insufficient Storage",
  508: "Loop Detected",
  511: "Network Authentication Required",
  200: "OK",
  201: "Created",
  202: "Accepted",
  204: "No Content",
  206: "Partial Content",
  301: "Moved Permanently",
  302: "Found",
  303: "See Other",
  304: "Not Modified",
  307: "Temporary Redirect",
  308: "Permanent Redirect",
};

export function codeName(code: number): string {
  if (code === 0) return "No status code";
  const r = REASON[code];
  return r ? `${code} ${r}` : String(code);
}

const GROUP_4XX = "Client errors, 4xx";
const GROUP_5XX = "Server errors, 5xx";
const GROUP_NONE = "No status code";
const SUCCEEDED = new Set(["1xx", "2xx", "3xx"]);

function StatusSection({
  status,
  state,
  error,
  onRetry,
  dim,
}: {
  status: ApiStatus | null;
  state: "loading" | "error" | "data";
  error: unknown;
  onRetry: () => void;
  dim: boolean;
}) {
  if (state === "error")
    return (
      <ErrorState compact title="The status codes did not load" error={error} onRetry={onRetry} />
    );
  if (!status)
    return (
      <Panel title="Status codes">
        <LoadingState label="Loading the status codes">
          <Skeleton.Table rows={3} columns={2} />
        </LoadingState>
      </Panel>
    );
  const ok = status.codes.filter((c) => SUCCEEDED.has(c.class) && c.requests > 0);
  const okCount = status.classes
    .filter((c) => SUCCEEDED.has(c.class))
    .reduce((a, c) => a + c.requests, 0);
  const shade = new Map<string, number>();
  const parts: ZoomPart[] = status.codes
    .filter((c) => !SUCCEEDED.has(c.class) && c.requests > 0)
    .map((c) => {
      const group = c.class === "4xx" ? GROUP_4XX : c.class === "5xx" ? GROUP_5XX : GROUP_NONE;
      const i = shade.get(group) ?? 0;
      shade.set(group, i + 1);
      return {
        key: String(c.status_code),
        label: codeName(c.status_code),
        count: c.requests,
        group,
        shade: i,
      };
    });
  const groups: ZoomGroup[] = [
    { label: GROUP_4XX, tone: "warning", definition: "Requests answered with a 4xx status." },
    { label: GROUP_5XX, tone: "critical", definition: "Requests answered with a 5xx status." },
    ...(parts.some((p) => p.group === GROUP_NONE)
      ? [
          {
            label: GROUP_NONE,
            tone: "muted" as const,
            definition: "Requests stored without a status code.",
          },
        ]
      : []),
  ];
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col gap-2 transition-opacity duration-(--vs-duration-change) ease-standard",
        dim && "opacity-60",
      )}
      data-testid="api-status"
    >
      <ZoomBlock
        title="Status codes"
        unit="requests"
        main={{
          label: "Succeeded",
          count: okCount,
          definition: "Requests answered with a 1xx, 2xx or 3xx status.",
        }}
        groups={groups}
        parts={parts}
        caption="Errors, by code"
        cleanText="Every request succeeded."
      />
      {ok.length > 0 ? (
        <ul
          aria-label="Success codes"
          className="flex flex-wrap gap-x-4 gap-y-1 px-1 text-caption text-fg-3"
        >
          {ok.map((c) => (
            <li key={c.status_code} data-key={c.status_code} className="flex gap-1.5">
              <span>{codeName(c.status_code)}</span>
              <span className="text-fg-2 tabular">{formatCount(c.requests)}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function ServicesSection({ services }: { services: ApiServiceItem[] }) {
  const total = services.reduce((a, s) => a + s.requests, 0);
  return (
    <Panel
      title="Services"
      actions={<span className="text-small text-fg-3 tabular">{formatCount(services.length)}</span>}
    >
      <BreakdownList
        label="Services"
        items={services.map((s) => ({
          key: s.service,
          text: serviceName(s.service),
          label: (
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-mono">{serviceName(s.service)}</span>
              {s.source === "span" ? <TracesBadge /> : null}
              {s.lossy ? <ApproxBadge /> : null}
              {s.span_data_ignored ? (
                <Tooltip content={IGNORED_NOTE}>
                  {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the note opens on focus too */}
                  <span tabIndex={0} className="inline-flex rounded-full focus-ring">
                    <Pill tone="neutral">Metrics only</Pill>
                  </span>
                </Tooltip>
              ) : null}
            </span>
          ),
          value: s.requests,
        }))}
        total={total}
        valueLabel="Requests"
        limit={20}
      />
    </Panel>
  );
}
