// the project's api numbers: requests, error rates and latency of its http
// services per route, for a range of utc minutes, hours or days against the
// previous range of equal length

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Filter as FilterIcon, Server } from "lucide-react";
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
  StatusClass,
  WarningKind,
} from "@/api/schemas";
import { BreakdownList, type TimeBucket, TimeChart } from "@/components/charts";
import { type Column, DataTable } from "@/components/data-table";
import { type Filter, FilterBar } from "@/components/filter-bar";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { parseUtcDayTime, RangeControl } from "@/components/range-control";
import { Sparkline, StatCard, StatGrid } from "@/components/stat-card";
import { EmptyState, ErrorState, Missing } from "@/components/states";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
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
import { useProject } from "@/pages/project-layout";
import { Movers } from "./movers";
import { ApiSkeleton, PAGE_HEADERS } from "./page-frame";
import { SelectFilter } from "./select-filter";

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

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.api}
        freshness={
          services.dataUpdatedAt ? { at: new Date(services.dataUpdatedAt), cadence: CADENCE } : null
        }
      />

      <div className="flex flex-col gap-4">
        <Controls
          state={state}
          set={set}
          services={services.data?.items ?? []}
          filtersOff={services.isError}
        />
        {range ? (
          <RangeLine range={range} />
        ) : services.isError ? (
          <AskedRangeLine range={state.range} />
        ) : null}
        {warnings.data ? <WarningNotices warnings={warnings.data} /> : null}
        {warnings.error ? (
          <ErrorState
            compact
            title="The notes about these numbers did not load"
            error={warnings.error}
            onRetry={() => warnings.refetch()}
          />
        ) : null}
      </div>

      {services.isPending ? (
        <ApiSkeleton />
      ) : services.error ? (
        <ErrorState
          title="The services did not load"
          error={services.error}
          onRetry={() => services.refetch()}
        />
      ) : services.data.items.length === 0 ? (
        <EmptyState
          icon={<Server aria-hidden strokeWidth={1.75} />}
          title="No API requests in this range"
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}/connect-api`}>Connect API</Link>
            </Button>
          }
        >
          Nothing arrived from your services in this range. To send data, connect your API.
        </EmptyState>
      ) : (
        <Dashboard tenantId={tenant.id} state={state} set={set} services={services.data.items} />
      )}
    </div>
  );
}
// the resolved range in words with its resolution, and why the resolution
// is coarser than the range asks for, when it is
function RangeLine({ range }: { range: ApiRange }) {
  return (
    <p className="text-small text-fg-2" aria-live="polite">
      <span className="text-fg">{formatRange(range)}</span>, {RESOLUTION_LABEL[range.resolution]}
      {range.resolution_note ? <span> {RESOLUTION_NOTE[range.resolution_note]}</span> : null}
    </p>
  );
}

// the range the person asked for, while the server's answer is missing
function AskedRangeLine({ range }: { range: DashboardState["range"] }) {
  const preset = PRESETS.find((p) => p.value === range.range);
  return (
    <p className="text-small text-fg-2">
      <span className="text-fg">
        {preset
          ? `Last ${preset.label}`
          : range.range === "custom"
            ? `${formatDateTimeUtc(range.from)} to ${formatDateTimeUtc(range.to)}`
            : ""}
      </span>
      , UTC
    </p>
  );
}

// ---- controls: range and filters, applied at once

// select values are prefixed, so a service named like a sentinel cannot
// collide with "all"
const ALL = "all";
const opt = (v: string) => `v:${v}`;
const unopt = (v: string) => (v === ALL ? null : v.slice(2));

function serviceName(s: string): string {
  return s === "" ? "No service name" : s;
}

function Controls({
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
  const setFilter = (k: keyof typeof f, v: string | null) =>
    set({ ...state, filters: { ...f, [k]: v } });
  const shown = (v: string) => (v === "" ? "(empty)" : v);
  const methods = [
    ...METHODS,
    ...(f.method !== null && !METHODS.includes(f.method) ? [f.method] : []),
  ];

  const filters: Filter[] = [
    {
      key: "service",
      label: "Service",
      control: (
        <SelectFilter
          label="Service"
          value={f.service === null ? ALL : opt(f.service)}
          onChange={(v) => setFilter("service", unopt(v))}
        >
          <option value={ALL}>All services</option>
          {names.map((n) => (
            <option key={n} value={opt(n)}>
              {serviceName(n)}
            </option>
          ))}
        </SelectFilter>
      ),
      active:
        f.service === null
          ? null
          : { label: serviceName(f.service), onClear: () => setFilter("service", null) },
    },
    {
      key: "method",
      label: "Method",
      control: (
        <SelectFilter
          label="Method"
          value={f.method === null ? ALL : opt(f.method)}
          onChange={(v) => setFilter("method", unopt(v))}
        >
          <option value={ALL}>All methods</option>
          {methods.map((m) => (
            <option key={m} value={opt(m)}>
              {m === "_OTHER" ? "Other" : m}
            </option>
          ))}
        </SelectFilter>
      ),
      active:
        f.method === null
          ? null
          : { label: shown(f.method), onClear: () => setFilter("method", null) },
    },
    {
      key: "route",
      label: "Route",
      control: <RouteField value={f.route} onChange={(v) => setFilter("route", v)} />,
      active:
        f.route === null
          ? null
          : { label: shown(f.route), onClear: () => setFilter("route", null) },
    },
  ];

  return (
    <div className="flex flex-col gap-3">
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
      />
      <fieldset disabled={filtersOff} className="min-w-0 disabled:opacity-60">
        <legend className="sr-only">Filters</legend>
        <FilterBar
          filters={filters}
          onClearAll={() =>
            set({ ...state, filters: { service: null, route: null, method: null } })
          }
        />
      </fieldset>
    </div>
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
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-caption text-fg-2">
        Route
      </label>
      <Input
        id={id}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && next !== value) onChange(next);
        }}
        placeholder="/v1/accounts/{id}"
        title="The exact route, with its placeholders"
        spellCheck={false}
        autoComplete="off"
        className="h-(--vs-control-sm) w-full font-mono text-small max-lg:h-(--vs-control-touch) sm:w-56"
      />
    </div>
  );
}

// ---- warnings about the data's sources. data from traces is said where it
// applies (the services and routes it marks), not in a banner.

type ShownWarning = Exclude<WarningKind, "span_derived">;

const WARNING_TEXT: Record<
  ShownWarning,
  { title: string; body: string; tone: "info" | "warning" | "critical" }
> = {
  lossy_histogram: {
    tone: "info",
    title: "Some latency percentiles are approximate",
    body: "A histogram arrived with other bucket bounds than the standard ones. Percentiles marked approximate can be off by one bucket.",
  },
  cardinality_overflow: {
    tone: "warning",
    title: "Some routes or services are grouped as {other}",
    body: "More distinct routes or services arrived in a day than the daily limit. Make sure that your routes are templates, not raw paths with IDs.",
  },
  coarse_buckets: {
    tone: "warning",
    title: "Some latency percentiles are not reliable",
    body: "A histogram arrived with buckets much wider than the standard ones. Record http.server.request.duration in seconds with the standard bucket bounds.",
  },
  secret_in_path: {
    tone: "critical",
    title: "A Stellar secret seed was in a request path",
    body: "This service replaced it before it stored the request. The URL can still be in your own logs and proxies. Treat that key as exposed and move its funds to a new account.",
  },
};

function WarningNotices({ warnings }: { warnings: ApiWarnings }) {
  const shown = warnings.totals.filter((t) => t.kind !== "span_derived" && t.count > 0);
  if (shown.length === 0) return null;
  return (
    <section aria-label="Data warnings" className="flex flex-col gap-2">
      {shown.map((t) => {
        const w = WARNING_TEXT[t.kind as ShownWarning];
        return (
          <div key={t.kind} data-warning={t.kind}>
            <Notice tone={w.tone} title={w.title} role={w.tone === "critical" ? "alert" : "status"}>
              <p>{w.body}</p>
              <p className="mt-1 text-caption text-fg-2">
                {formatCount(t.count)} {t.count === 1 ? "time" : "times"} in this range
                {t.last_day ? `, last on ${formatDate(t.last_day)}` : ""}
              </p>
            </Notice>
          </div>
        );
      })}
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
    <div className="flex flex-col gap-8">
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
          points={series.data?.points ?? null}
          previous={previous}
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

      <div className="grid gap-4 lg:grid-cols-2">
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

// a sparkline only when it has a shape: two buckets or more that differ
export function sparkValues(
  points: ApiPoint[] | null,
  v: (p: ApiPoint) => number | null,
): number[] | null {
  if (!points || points.length < 2) return null;
  const values = points.map((p) => v(p) ?? 0);
  if (Math.max(...values) === Math.min(...values)) return null;
  return values;
}

function Summary({
  overview,
  dim,
  points,
  previous,
  metric,
  onMetric,
}: {
  overview: ApiOverview | null;
  dim: boolean;
  points: ApiPoint[] | null;
  previous: (ApiPoint | null)[] | null;
  metric: Metric;
  onMetric: (m: Metric) => void;
}) {
  const spark = (v: (p: ApiPoint) => number | null) => {
    const values = sparkValues(points, v);
    return values ? (
      <Sparkline values={values} previous={previous?.map((q) => (q ? (v(q) ?? 0) : 0))} />
    ) : null;
  };

  if (!overview)
    return (
      <section aria-label="Summary" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {["Requests", "4xx rate", "5xx rate", "p95 latency"].map((l) => (
          <StatCard key={l} label={l} value={null} state="loading" />
        ))}
      </section>
    );

  const cur = overview.current.summary;
  const prev = overview.previous.summary;
  const hadPrevious = prev.requests > 0;
  const p95Plot = (p: ApiPoint) => {
    const x = plotPercentile(p.p95);
    return x === null ? null : Math.min(x, OPEN_BUCKET_CEILING);
  };
  const percent = (k: "p50" | "p99") => {
    const t = latencyText(cur[k]);
    return (
      <span data-tile={k} className="flex items-baseline gap-1">
        <span className="text-fg-2">{k}</span>
        {t === null ? (
          <Missing reason="No requests in this range" />
        ) : (
          <span className="font-mono text-fg">{t}</span>
        )}
      </span>
    );
  };

  return (
    <section
      aria-labelledby="api-summary"
      className={cn(
        "flex flex-col gap-3 transition-opacity duration-(--vs-duration-change) ease-standard",
        dim && "opacity-60",
      )}
    >
      <h2 id="api-summary" className="sr-only">
        Summary
      </h2>
      <Movers
        items={[
          {
            key: "requests",
            label: "Requests",
            change: toStatChange(relativeChange(cur.requests, prev.requests), null),
          },
          {
            key: "4xx",
            label: "4xx rate",
            change: toStatChange(rateChange(cur.rate_4xx, prev.rate_4xx), true),
          },
          {
            key: "5xx",
            label: "5xx rate",
            change: toStatChange(rateChange(cur.rate_5xx, prev.rate_5xx), true),
          },
          {
            key: "p95",
            label: "p95 latency",
            change: toStatChange(percentileChange(cur.p95, prev.p95), true),
          },
        ]}
      />
      <StatGrid>
        <StatCard
          data-tile="Requests"
          label="Requests"
          definition="Requests that your services answered. Health checks and preflights are not included."
          value={cur.requests}
          change={toStatChange(relativeChange(cur.requests, prev.requests), null)}
          spark={spark((p) => p.requests)}
          selected={metric === "requests"}
          onSelect={() => onMetric("requests")}
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
              change={toStatChange(rateChange(r, pr), true)}
              spark={spark((p) => (k === "4xx" ? p.rate_4xx : p.rate_5xx))}
              selected={metric === k}
              onSelect={() => onMetric(k)}
              hideWhenEmpty
              footer={
                <span className="text-caption text-fg-2">
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
          change={toStatChange(percentileChange(cur.p95, prev.p95), true)}
          spark={spark(p95Plot)}
          selected={metric === "p95"}
          onSelect={() => onMetric("p95")}
          hideWhenEmpty
          footer={
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption">
              {percent("p50")}
              {percent("p99")}
              {cur.lossy ? <span className="text-fg-2">Approximate</span> : null}
            </span>
          }
        />
      </StatGrid>
      <p className="text-caption text-fg-2 empty:hidden">
        {hadPrevious ? `Compared with ${formatRange(overview.previous.range)}.` : null}
        {cur.infrastructure_requests > 0 ? (
          <span data-testid="infrastructure-requests">
            {" "}
            {formatCount(cur.infrastructure_requests)} health checks and preflights are not included
            {overview.traffic_classes.applied_from
              ? ` (kept apart since ${formatDate(overview.traffic_classes.applied_from)})`
              : ""}
            .
          </span>
        ) : null}
      </p>
    </section>
  );
}

// ---- the chart of the selected card

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
            tone: "series-1",
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
        description="Gaps are buckets without requests."
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
  const anyLossy = points.some((p) => p.lossy);
  const lines = [
    ["p95", "series-1"],
    ["p50", "series-2"],
    ["p99", "series-3"],
  ] as const;
  return (
    <TimeChart
      key={chartState}
      {...common}
      kind="line"
      title={`Latency ${per}`}
      description={anyLossy ? "Some buckets are approximate." : undefined}
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

// the page's most failing and slowest routes are marked in their rows, so
// the one to look at stands out without reading every number
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

const routeKey = (r: ApiRouteItem) => `${r.service}\u0000${r.method}\u0000${r.route}`;

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

  const latency = (p: Percentile) => {
    const t = latencyText(p);
    return t === null ? <Missing reason="No requests" /> : t;
  };
  const rate = (k: "4xx" | "5xx") => (r: ApiRouteItem) => {
    const v = k === "4xx" ? r.rate_4xx : r.rate_5xx;
    const n = k === "4xx" ? r.errors_4xx : r.errors_5xx;
    const t = rateText(v);
    return t === null ? (
      <Missing reason="No requests" />
    ) : (
      <Tooltip content={`${formatCount(n)} ${k === "4xx" ? "client" : "server"} errors`}>
        <span className={rateTone(k, v)}>{t}</span>
      </Tooltip>
    );
  };
  const num = { align: "right", numeric: true, minWidth: "min-w-20" } as const;

  const columns: Column<ApiRouteItem>[] = [
    {
      key: "route",
      header: "Route",
      sortKey: "route",
      primary: true,
      minWidth: "min-w-72",
      cell: (r) => (
        <span className="flex min-w-0 flex-col gap-1 py-2">
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="w-14 shrink-0 font-mono text-axis text-fg-2">{r.method}</span>
            <span className="min-w-0 font-mono break-all text-fg">{r.route}</span>
            {marks.failing === r ? <Pill tone="critical">Most 5xx</Pill> : null}
            {marks.slow === r ? <Pill tone="warning">Slowest p95</Pill> : null}
          </span>
          <span className="flex flex-wrap items-center gap-2 text-caption text-fg-2 sm:pl-16">
            {serviceName(r.service)}
            {r.source === "span" ? <TracesBadge /> : null}
            {r.lossy ? <ApproxBadge /> : null}
            {r.infrastructure_requests > 0 ? (
              <span>{formatCount(r.infrastructure_requests)} health checks not included</span>
            ) : null}
          </span>
        </span>
      ),
    },
    {
      key: "requests",
      header: "Requests",
      sortKey: "requests",
      align: "right",
      numeric: true,
      // 45,881,688 fits without a cut
      minWidth: "min-w-28",
      mobile: true,
      cell: (r) => formatCount(r.requests),
    },
    { key: "rate_4xx", header: "4xx rate", sortKey: "rate_4xx", ...num, cell: rate("4xx") },
    {
      key: "rate_5xx",
      header: "5xx rate",
      sortKey: "rate_5xx",
      ...num,
      mobile: true,
      cell: rate("5xx"),
    },
    { key: "p50", header: "p50", sortKey: "p50", ...num, cell: (r) => latency(r.p50) },
    {
      key: "p95",
      header: "p95",
      sortKey: "p95",
      ...num,
      mobile: true,
      cell: (r) => (
        <span className={marks.slow === r ? "text-warning" : undefined}>{latency(r.p95)}</span>
      ),
    },
    { key: "p99", header: "p99", sortKey: "p99", ...num, cell: (r) => latency(r.p99) },
    {
      key: "filter",
      header: "Filter",
      align: "right",
      className: "w-12",
      cell: (r) => (
        <Button
          variant="ghost"
          size="icon-sm"
          // shown on row hover or focus with a mouse, always on touch
          data-row-action=""
          aria-label={`Filter to ${r.method} ${r.route} of ${serviceName(r.service)}`}
          onClick={() =>
            set({ ...state, filters: { service: r.service, method: r.method, route: r.route } })
          }
        >
          <FilterIcon aria-hidden />
        </Button>
      ),
    },
  ];

  return (
    // the table itself is the "Routes" region; this only holds its heading
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h2 className="text-section text-fg">Routes</h2>
        <ul
          aria-label="Rate colors"
          className="flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-fg-2"
        >
          <li className="flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-bar bg-warning" />
            4xx rate 5% or more
          </li>
          <li className="flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-bar bg-critical" />
            Any 5xx
          </li>
        </ul>
      </div>
      <DataTable
        label="Routes"
        columns={columns}
        rows={data?.items ?? []}
        getRowKey={routeKey}
        rowLabel={(r) => `${r.method} ${r.route}`}
        rowProps={(r) => ({
          "data-route": r.route,
          "data-service": r.service,
          "data-method": r.method,
        })}
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
        empty={{ title: "No routes match", text: "Change the range or clear the filters." }}
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
    </div>
  );
}

// ---- status codes and services

const CLASS: Record<StatusClass, { label: string; bg: string }> = {
  "1xx": { label: "1xx informational", bg: "bg-series-3" },
  "2xx": { label: "2xx success", bg: "bg-series-1" },
  "3xx": { label: "3xx redirect", bg: "bg-series-4" },
  "4xx": { label: "4xx client error", bg: "bg-warning" },
  "5xx": { label: "5xx server error", bg: "bg-critical" },
  unknown: { label: "No status code", bg: "bg-series-prev" },
};

function Card({
  title,
  id,
  children,
  className,
}: {
  title: string;
  id: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      aria-labelledby={id}
      className={cn(
        "flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5",
        className,
      )}
    >
      <h2 id={id} className="text-title text-fg">
        {title}
      </h2>
      {children}
    </section>
  );
}

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
  const total = status?.requests ?? 0;
  const parts = status?.classes.filter((c) => c.requests > 0) ?? [];
  return (
    <Card
      title="Status codes"
      id="api-status"
      className={cn(
        "transition-opacity duration-(--vs-duration-change) ease-standard",
        dim && "opacity-60",
      )}
    >
      {parts.length > 0 ? (
        <div className="flex flex-col gap-2">
          <div
            role="img"
            aria-label={`Requests by status class: ${parts
              .map((c) => `${CLASS[c.class].label} ${formatCount(c.requests)}`)
              .join("; ")}`}
            className="flex h-2 w-full gap-1 overflow-hidden rounded-sm"
          >
            {parts.map((c) => (
              <div
                key={c.class}
                className={cn("h-full min-w-0.5 rounded-bar", CLASS[c.class].bg)}
                // widths are data, so they cannot be classes
                style={{ flexGrow: c.requests, flexBasis: 0 }}
              />
            ))}
          </div>
          <ul aria-label="Status classes" className="flex flex-wrap gap-x-4 gap-y-1 text-caption">
            {parts.map((c) => (
              <li key={c.class} data-class={c.class} className="flex items-center gap-1 text-fg-2">
                <span aria-hidden className={cn("size-2 rounded-bar", CLASS[c.class].bg)} />
                {CLASS[c.class].label}
                <span className="text-fg tabular">{formatCount(c.requests)}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <BreakdownList
        label="Status codes"
        state={state === "data" ? undefined : state}
        error={error}
        onRetry={onRetry}
        items={(status?.codes ?? []).map((c) => ({
          key: String(c.status_code),
          label:
            c.status_code === 0 ? (
              "No status code"
            ) : (
              <span className="font-mono">{c.status_code}</span>
            ),
          text: c.status_code === 0 ? "No status code" : String(c.status_code),
          value: c.requests,
        }))}
        total={total}
        valueLabel="Requests"
        emptyTitle="No requests in this range"
      />
    </Card>
  );
}

function ServicesSection({ services }: { services: ApiServiceItem[] }) {
  const anySpan = services.some((s) => s.source === "span");
  const total = services.reduce((a, s) => a + s.requests, 0);
  return (
    <Card title="Services" id="api-services">
      <BreakdownList
        label="Services"
        items={services.map((s) => ({
          key: s.service,
          text: serviceName(s.service),
          label: (
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate">{serviceName(s.service)}</span>
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
      {anySpan ? <p className="text-caption text-fg-2">From traces: {SPAN_NOTE}</p> : null}
    </Card>
  );
}
