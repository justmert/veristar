import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  Filter,
  Settings2,
  X,
} from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { ApiRangeQuery } from "@/api/endpoints";
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
  ApiRange,
  ApiRouteItem,
  ApiRouteSort,
  ApiServiceItem,
  ApiStatus,
  ApiWarnings,
  Percentile,
  WarningKind,
} from "@/api/schemas";
import {
  ErrorChart,
  LatencyChart,
  StatusClassBar,
  share,
  VolumeChart,
} from "@/components/api-charts";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import {
  type Change,
  type DashboardState,
  defaultOrder,
  formatPercentile,
  formatRange,
  formatRate,
  localInputToUtc,
  METHODS,
  PAGE_SIZE,
  PRESETS,
  percentileChange,
  RESOLUTION_LABEL,
  RESOLUTION_NOTE,
  rateChange,
  readState,
  relativeChange,
  routesQuery,
  utcToLocalInput,
  validateCustom,
  writeState,
} from "@/lib/api-metrics";
import { formatCount, formatDate } from "@/lib/format";
import { Notice, useProject } from "./project-layout";

// contract 7.3: the one line shown with every trace-derived badge
export const SPAN_NOTE =
  "Derived from sampled traces; counts are lower than the real request count when traces are sampled.";
// contract 7.3 span_data_ignored: the service has metric rows in the range
// and span rows that were left out. that happens when it switched from
// traces to metrics during the range, and also for every service that
// exports both signals, so the note names both cases
export const IGNORED_NOTE =
  "Counted from its metrics for the whole range. Numbers derived from its traces (sent alongside the metrics, or before it started sending metrics) are left out.";

export function ApiTrafficPage() {
  const { tenant } = useProject();
  const [params, setParams] = useSearchParams();
  const state = readState(params);
  const set = (next: DashboardState) => setParams(writeState(next));

  const services = useQuery(apiServicesQuery(tenant.id, state.range));
  const warnings = useQuery(apiWarningsQuery(tenant.id, state.range));

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">API traffic</h2>
          <p className="mt-1 max-w-prose text-sm text-muted-foreground">
            Requests, errors and latency of your HTTP API, per normalized route. Times are UTC;
            hourly and daily numbers are updated every few minutes.
          </p>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to={`/projects/${tenant.id}/api`}>
            <Settings2 aria-hidden />
            Setup and tokens
          </Link>
        </Button>
      </header>

      <Controls
        state={state}
        set={set}
        services={services.data?.items ?? []}
        range={services.data?.range ?? null}
      />

      {services.error ? (
        <ErrorPanel error={services.error} onRetry={() => services.refetch()} />
      ) : null}
      {warnings.error ? (
        <ErrorPanel
          title="Ingest warnings could not be loaded"
          error={warnings.error}
          onRetry={() => warnings.refetch()}
        />
      ) : null}

      {services.isPending ? <Loading label="Loading API traffic" /> : null}
      {services.data ? (
        <>
          <RangeNotes range={services.data.range} />
          {warnings.data ? <WarningBanners warnings={warnings.data} /> : null}
          {services.data.items.length === 0 ? (
            <EmptyState tenantId={tenant.id} range={services.data.range} />
          ) : (
            <Dashboard
              tenantId={tenant.id}
              state={state}
              set={set}
              services={services.data.items}
            />
          )}
        </>
      ) : null}
    </div>
  );
}

function EmptyState({ tenantId, range }: { tenantId: string; range: ApiRange }) {
  return (
    <div className="rounded-xl border border-dashed bg-card p-6 sm:p-8">
      <h3 className="font-semibold">No API requests in this range</h3>
      <p className="mt-1 max-w-prose text-muted-foreground">
        Nothing arrived from your services between {formatRange(range)}. Hourly and daily numbers
        are updated every few minutes; the 1 hour range shows the newest minutes. If your app does
        not send data yet, create a token and add the second exporter described on the setup page.
      </p>
      <Button asChild className="mt-4">
        <Link to={`/projects/${tenantId}/api`}>Set up API analytics</Link>
      </Button>
    </div>
  );
}

// ---- controls: range presets, custom range and filters, one row above
// everything they scope

function Controls({
  state,
  set,
  services,
  range,
}: {
  state: DashboardState;
  set: (s: DashboardState) => void;
  services: ApiServiceItem[];
  range: ApiRange | null;
}) {
  const [custom, setCustom] = useState(state.range.range === "custom");
  useEffect(() => {
    if (state.range.range === "custom") setCustom(true);
  }, [state.range.range]);

  const choose = (value: string) => {
    if (value === "custom") {
      setCustom(true);
      return;
    }
    setCustom(false);
    const preset = PRESETS.find((p) => p.value === value);
    if (preset) set({ ...state, range: { range: preset.value } });
  };
  const selected = custom ? "custom" : state.range.range;

  return (
    <div className="space-y-4 rounded-xl border bg-card p-4 sm:p-5">
      <fieldset>
        <legend className="text-sm font-medium">Range</legend>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {[
            ...PRESETS.map((p) => ({ value: p.value as string, label: p.label })),
            { value: "custom", label: "Custom" },
          ].map((o) => (
            <label key={o.value} className="cursor-pointer">
              <input
                type="radio"
                name="api-range"
                value={o.value}
                checked={selected === o.value}
                onChange={() => choose(o.value)}
                className="peer sr-only"
              />
              <span className="inline-flex h-8 items-center rounded-md border px-3 text-sm peer-checked:border-primary peer-checked:bg-primary peer-checked:text-primary-foreground peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-ring hover:bg-muted peer-checked:hover:bg-primary">
                {o.label}
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      {custom ? (
        <CustomRange
          key={state.range.range === "custom" ? `${state.range.from}|${state.range.to}` : "new"}
          current={state.range}
          resolved={range}
          onApply={(r) => set({ ...state, range: r })}
        />
      ) : null}

      <Filters state={state} set={set} services={services} />
    </div>
  );
}

function CustomRange({
  current,
  resolved,
  onApply,
}: {
  current: ApiRangeQuery;
  resolved: ApiRange | null;
  onApply: (r: ApiRangeQuery) => void;
}) {
  const start =
    current.range === "custom"
      ? { from: current.from, to: current.to }
      : resolved
        ? { from: resolved.from, to: resolved.to }
        : null;
  const [from, setFrom] = useState(start ? utcToLocalInput(start.from) : "");
  const [to, setTo] = useState(start ? utcToLocalInput(start.to) : "");
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  const fromId = useId();
  const toId = useId();

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateCustom(from, to, new Date());
    setErrors(found);
    const f = localInputToUtc(from);
    const t = localInputToUtc(to);
    if (Object.keys(found).length > 0 || !f || !t) return;
    onApply({ range: "custom", from: f, to: t });
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label="Custom range"
      className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-start"
    >
      <div className="space-y-1">
        <label htmlFor={fromId} className="text-sm font-medium">
          From (UTC)
        </label>
        <input
          id={fromId}
          type="datetime-local"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          aria-invalid={errors.from ? true : undefined}
          aria-describedby={errors.from ? `${fromId}-err` : undefined}
          className="h-9 w-full rounded-md border border-input bg-transparent px-2 text-sm"
        />
        {errors.from ? (
          <p id={`${fromId}-err`} className="text-sm text-destructive">
            {errors.from}
          </p>
        ) : null}
      </div>
      <div className="space-y-1">
        <label htmlFor={toId} className="text-sm font-medium">
          To (UTC)
        </label>
        <input
          id={toId}
          type="datetime-local"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          aria-invalid={errors.to ? true : undefined}
          aria-describedby={errors.to ? `${toId}-err` : undefined}
          className="h-9 w-full rounded-md border border-input bg-transparent px-2 text-sm"
        />
        {errors.to ? (
          <p id={`${toId}-err`} className="text-sm text-destructive">
            {errors.to}
          </p>
        ) : null}
      </div>
      <Button type="submit" className="sm:mt-6">
        Apply range
      </Button>
      <p className="text-xs text-muted-foreground sm:col-span-3">
        Up to 6 hours is shown per minute, up to 31 days per hour, longer ranges per day. The start
        and end move out to whole buckets.
      </p>
    </form>
  );
}

// select values are prefixed, so a service named like a sentinel cannot
// collide with "all"
const ALL = "all";
const opt = (v: string) => `v:${v}`;
const unopt = (v: string) => (v === ALL ? null : v.slice(2));

function Filters({
  state,
  set,
  services,
}: {
  state: DashboardState;
  set: (s: DashboardState) => void;
  services: ApiServiceItem[];
}) {
  const f = state.filters;
  const [route, setRoute] = useState(f.route ?? "");
  useEffect(() => setRoute(f.route ?? ""), [f.route]);
  const serviceId = useId();
  const methodId = useId();
  const routeId = useId();
  const names = services.map((s) => s.service);
  // a filtered service with no data in this range still shows as chosen
  if (f.service !== null && !names.includes(f.service)) names.push(f.service);

  function submitRoute(e: FormEvent) {
    e.preventDefault();
    const r = route.trim();
    set({ ...state, filters: { ...f, route: r === "" ? null : r } });
  }

  const active = f.service !== null || f.route !== null || f.method !== null;

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,14rem)_minmax(0,9rem)_minmax(0,1fr)]">
        <div className="space-y-1">
          <label htmlFor={serviceId} className="text-sm font-medium">
            Service
          </label>
          <select
            id={serviceId}
            value={f.service === null ? ALL : opt(f.service)}
            onChange={(e) => set({ ...state, filters: { ...f, service: unopt(e.target.value) } })}
            className="h-9 w-full rounded-md border border-input bg-card px-2 text-sm"
          >
            <option value={ALL}>All services</option>
            {names.map((n) => {
              const s = services.find((x) => x.service === n);
              return (
                <option key={n} value={opt(n)}>
                  {n === "" ? "(no service name)" : n}
                  {s?.source === "span" ? " (from traces)" : ""}
                </option>
              );
            })}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor={methodId} className="text-sm font-medium">
            Method
          </label>
          <select
            id={methodId}
            value={f.method === null ? ALL : opt(f.method)}
            onChange={(e) => set({ ...state, filters: { ...f, method: unopt(e.target.value) } })}
            className="h-9 w-full rounded-md border border-input bg-card px-2 text-sm"
          >
            <option value={ALL}>All methods</option>
            {[
              ...METHODS,
              ...(f.method !== null && !METHODS.includes(f.method) ? [f.method] : []),
            ].map((m) => (
              <option key={m} value={opt(m)}>
                {m === "_OTHER" ? "Other (_OTHER)" : m}
              </option>
            ))}
          </select>
        </div>
        <form onSubmit={submitRoute} aria-label="Route filter" className="space-y-1">
          <label htmlFor={routeId} className="text-sm font-medium">
            Route
          </label>
          <div className="flex gap-2">
            <input
              id={routeId}
              value={route}
              onChange={(e) => setRoute(e.target.value)}
              placeholder="/v1/accounts/{id}"
              autoComplete="off"
              spellCheck={false}
              maxLength={1024}
              className="h-9 min-w-0 flex-1 rounded-md border border-input bg-transparent px-2 font-mono text-sm"
            />
            <Button type="submit" variant="outline">
              Apply
            </Button>
          </div>
        </form>
      </div>
      <p className="text-xs text-muted-foreground">
        Routes match exactly, as normalized (placeholders like {"{id}"}). Pick one from the routes
        table below with its filter button.
      </p>
      {active ? (
        <ul aria-label="Active filters" className="flex flex-wrap gap-2">
          {(
            [
              ["service", "Service"],
              ["method", "Method"],
              ["route", "Route"],
            ] as const
          ).map(([k, label]) =>
            f[k] === null ? null : (
              <li key={k}>
                <span className="inline-flex items-center gap-1 rounded-full border bg-muted py-0.5 pr-1 pl-2.5 text-xs">
                  {label}: <span className="font-mono">{f[k] === "" ? "(empty)" : f[k]}</span>
                  <button
                    type="button"
                    aria-label={`Remove the ${label.toLowerCase()} filter`}
                    onClick={() => set({ ...state, filters: { ...f, [k]: null } })}
                    className="rounded-full p-0.5 hover:bg-background"
                  >
                    <X aria-hidden className="size-3.5" />
                  </button>
                </span>
              </li>
            ),
          )}
          <li>
            <button
              type="button"
              onClick={() =>
                set({ ...state, filters: { service: null, route: null, method: null } })
              }
              className="text-xs text-primary underline underline-offset-2"
            >
              Clear filters
            </button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}

// ---- notes about the range and the data's sources

function RangeNotes({ range }: { range: ApiRange }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        <span className="font-medium text-foreground">{formatRange(range)}</span>,{" "}
        {RESOLUTION_LABEL[range.resolution]}
      </p>
      {range.resolution_note ? (
        <Notice tone="info">{RESOLUTION_NOTE[range.resolution_note]}</Notice>
      ) : null}
    </div>
  );
}

const WARNING_TEXT: Record<WarningKind, { title: string; body: string }> = {
  span_derived: {
    title: "Some numbers come from traces",
    body: "A service that sends no metrics is counted from its server spans. Its numbers are marked From traces and are lower than the real request count when traces are sampled.",
  },
  lossy_histogram: {
    title: "Some latency percentiles are approximate",
    body: "A histogram arrived with bucket bounds other than the standard ones and was re-bucketed. Percentiles marked approximate can be off by up to one bucket.",
  },
  cardinality_overflow: {
    title: "Some routes or services were grouped as {other}",
    body: "More distinct routes or services arrived in a day than the daily limit, and the rest were counted under {other}. Check that your routes are templates, not raw paths with ids.",
  },
  secret_in_path: {
    title: "A Stellar secret seed appeared in a request path",
    body: "It was replaced with {redacted-secret} before anything was stored here, but it travelled in a URL and may sit in your own logs and proxies. Treat that key as exposed and move its funds to a new account.",
  },
};

export function WarningBanners({ warnings }: { warnings: ApiWarnings }) {
  const shown = warnings.totals.filter((t) => t.count > 0);
  if (shown.length === 0) return null;
  return (
    <section aria-label="Ingest warnings" className="space-y-2">
      {shown.map((t) => (
        <div
          key={t.kind}
          data-warning={t.kind}
          className={`flex gap-3 rounded-lg border p-4 text-sm ${
            t.kind === "secret_in_path"
              ? "border-destructive/40 bg-destructive/5"
              : "border-warn/40 bg-warn/8"
          }`}
        >
          <AlertTriangle
            aria-hidden
            className={`mt-0.5 size-4 shrink-0 ${t.kind === "secret_in_path" ? "text-destructive" : "text-warn"}`}
          />
          <div>
            <p className="font-medium">{WARNING_TEXT[t.kind].title}</p>
            <p className="mt-1 text-muted-foreground">{WARNING_TEXT[t.kind].body}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {formatCount(t.count)} {t.count === 1 ? "time" : "times"} in this range
              {t.last_day ? `, last on ${formatDate(`${t.last_day}T00:00:00Z`)}` : ""}
            </p>
          </div>
        </div>
      ))}
    </section>
  );
}

function serviceName(s: string): string {
  return s === "" ? "(no service name)" : s;
}

export function TracesBadge({ id }: { id?: string }) {
  return (
    <span
      title={SPAN_NOTE}
      aria-describedby={id}
      className="inline-flex shrink-0 items-center rounded-full border border-warn/50 bg-warn/10 px-2 py-0.5 text-xs font-medium whitespace-nowrap text-foreground"
    >
      From traces
    </span>
  );
}

function ApproxBadge() {
  return (
    <span className="rounded-full bg-muted px-1.5 py-0.5 text-[0.7rem] font-medium text-muted-foreground">
      approximate
    </span>
  );
}

// ---- the dashboard proper

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

  return (
    <div className="space-y-6">
      <ServicesSection services={services} />

      {overview.error ? (
        <ErrorPanel error={overview.error} onRetry={() => overview.refetch()} />
      ) : overview.data ? (
        <OverviewTiles overview={overview.data} dim={overview.isPlaceholderData} />
      ) : (
        <Loading label="Loading the summary" />
      )}

      {series.error ? (
        <ErrorPanel error={series.error} onRetry={() => series.refetch()} />
      ) : series.data ? (
        <div className="grid gap-4 lg:grid-cols-2">
          <VolumeChart
            range={series.data.range}
            points={series.data.points}
            dim={series.isPlaceholderData}
          />
          <ErrorChart
            range={series.data.range}
            points={series.data.points}
            dim={series.isPlaceholderData}
          />
          <div className="min-w-0 lg:col-span-2">
            <LatencyChart
              range={series.data.range}
              points={series.data.points}
              dim={series.isPlaceholderData}
            />
          </div>
        </div>
      ) : (
        <Loading label="Loading the charts" />
      )}

      {status.error ? (
        <ErrorPanel error={status.error} onRetry={() => status.refetch()} />
      ) : status.data ? (
        <StatusSection status={status.data} dim={status.isPlaceholderData} />
      ) : (
        <Loading label="Loading status codes" />
      )}

      <RoutesSection tenantId={tenantId} state={state} set={set} />
    </div>
  );
}

function ServicesSection({ services }: { services: ApiServiceItem[] }) {
  const noteId = useId();
  const anySpan = services.some((s) => s.source === "span");
  return (
    <section aria-labelledby={`${noteId}-h`} className="rounded-xl border bg-card p-4 sm:p-5">
      <h3 id={`${noteId}-h`} className="font-semibold">
        Services
      </h3>
      <ul aria-label="Services" className="mt-3 divide-y">
        {services.map((s) => (
          <li key={s.service} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
            <span className="min-w-0 flex-1 truncate font-medium">{serviceName(s.service)}</span>
            {s.source === "span" ? <TracesBadge id={noteId} /> : null}
            {s.lossy ? <ApproxBadge /> : null}
            <span className="tabular text-muted-foreground">
              {formatCount(s.requests)} {s.requests === 1 ? "request" : "requests"}
            </span>
            {s.span_data_ignored ? (
              <span className="basis-full text-xs text-muted-foreground">{IGNORED_NOTE}</span>
            ) : null}
          </li>
        ))}
      </ul>
      {anySpan ? (
        <p id={noteId} className="mt-2 text-xs text-muted-foreground">
          From traces: {SPAN_NOTE}
        </p>
      ) : null}
    </section>
  );
}

function OverviewTiles({ overview, dim }: { overview: ApiOverview; dim: boolean }) {
  const cur = overview.current.summary;
  const prev = overview.previous.summary;
  const pctTile = (k: "p50" | "p95" | "p99") => ({
    label: k,
    value: percentileText(cur[k]),
    approximate: cur.lossy && cur[k] !== null,
    previous: percentileText(prev[k]),
    change: percentileChange(cur[k], prev[k]),
    upIsBad: true,
  });
  const tiles = [
    {
      label: "Requests",
      value: formatCount(cur.requests),
      approximate: false,
      previous: formatCount(prev.requests),
      change: relativeChange(cur.requests, prev.requests),
      upIsBad: null,
    },
    {
      label: "4xx rate",
      value: formatRate(cur.rate_4xx) ?? "No requests",
      sub: `${formatCount(cur.errors_4xx)} client errors`,
      approximate: false,
      previous: formatRate(prev.rate_4xx) ?? "no requests",
      change: rateChange(cur.rate_4xx, prev.rate_4xx),
      upIsBad: true,
    },
    {
      label: "5xx rate",
      value: formatRate(cur.rate_5xx) ?? "No requests",
      sub: `${formatCount(cur.errors_5xx)} server errors`,
      approximate: false,
      previous: formatRate(prev.rate_5xx) ?? "no requests",
      change: rateChange(cur.rate_5xx, prev.rate_5xx),
      upIsBad: true,
    },
    pctTile("p50"),
    pctTile("p95"),
    pctTile("p99"),
  ];
  return (
    <section
      aria-labelledby="api-summary"
      className={`transition-opacity ${dim ? "opacity-60" : ""}`}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h3 id="api-summary" className="font-semibold">
          Summary
        </h3>
        <p className="text-xs text-muted-foreground">
          Compared with the previous range of equal length, {formatRange(overview.previous.range)}
        </p>
      </div>
      <dl className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        {tiles.map((t) => (
          <div key={t.label} data-tile={t.label} className="rounded-xl border bg-card p-3 sm:p-4">
            <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
              {t.label}
              {t.approximate ? <ApproxBadge /> : null}
            </dt>
            <dd className="mt-1 text-2xl font-semibold tracking-tight">{t.value}</dd>
            {"sub" in t && t.sub ? (
              <dd className="text-xs text-muted-foreground">{t.sub}</dd>
            ) : null}
            <dd className="mt-1.5 text-xs text-muted-foreground">
              <ChangeLine change={t.change} upIsBad={t.upIsBad} />
              <span className="block">previous: {t.previous}</span>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function percentileText(p: Percentile): string {
  return formatPercentile(p) ?? "No requests";
}

// direction is said in words as well as by the arrow and its colour
function ChangeLine({ change, upIsBad }: { change: Change; upIsBad: boolean | null }) {
  if (change.direction === null)
    return change.text ? <span className="block">{change.text}</span> : null;
  if (change.direction === "same") return <span className="block">{change.text}</span>;
  const bad = upIsBad === null ? null : (change.direction === "up") === upIsBad;
  const tone = bad === null ? "text-foreground" : bad ? "text-destructive" : "text-good";
  const Icon = change.direction === "up" ? ArrowUp : ArrowDown;
  return (
    <span className={`flex items-center gap-1 font-medium ${tone}`}>
      <Icon aria-hidden className="size-3.5" />
      <span className="sr-only">{change.direction === "up" ? "up" : "down"}</span>
      {change.text}
    </span>
  );
}

function StatusSection({ status, dim }: { status: ApiStatus; dim: boolean }) {
  const max = Math.max(1, ...status.codes.map((c) => c.requests));
  return (
    <section
      aria-labelledby="api-status"
      className={`grid gap-4 transition-opacity lg:grid-cols-2 ${dim ? "opacity-60" : ""}`}
    >
      <h3 id="api-status" className="sr-only">
        Status codes
      </h3>
      <div className="rounded-xl border bg-card p-4 sm:p-5">
        <h4 className="font-semibold">By status class</h4>
        <p className="mt-0.5 mb-3 text-sm text-muted-foreground">
          {formatCount(status.requests)} requests in the range.
        </p>
        <StatusClassBar status={status} />
      </div>
      <div className="rounded-xl border bg-card p-4 sm:p-5">
        <h4 className="font-semibold">By status code</h4>
        {status.codes.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">No requests in the range.</p>
        ) : (
          <div className="relative mt-3 max-h-80 overflow-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-xs text-muted-foreground">
                <tr>
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    Code
                  </th>
                  <th scope="col" className="w-full py-1.5 pr-3 font-medium">
                    <span className="sr-only">Share bar</span>
                  </th>
                  <th scope="col" className="py-1.5 pr-3 text-right font-medium">
                    Requests
                  </th>
                  <th scope="col" className="py-1.5 text-right font-medium">
                    Share
                  </th>
                </tr>
              </thead>
              <tbody>
                {status.codes.map((c) => (
                  <tr key={c.status_code} data-code={c.status_code}>
                    <th scope="row" className="tabular py-1.5 pr-3 font-medium">
                      {c.status_code === 0 ? "none" : c.status_code}
                    </th>
                    <td className="py-1.5 pr-3" aria-hidden>
                      <div
                        className={`h-2 rounded-full ${CODE_BAR[c.class]}`}
                        // the bar's length is the value, so it is data, not a class
                        style={{ width: `${Math.max(1, (c.requests / max) * 100)}%` }}
                      />
                    </td>
                    <td className="tabular py-1.5 pr-3 text-right">{formatCount(c.requests)}</td>
                    <td className="tabular py-1.5 text-right text-muted-foreground">
                      {share(c.requests, status.requests)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

const CODE_BAR: Record<ApiStatus["codes"][number]["class"], string> = {
  "1xx": "bg-chart-1xx",
  "2xx": "bg-chart-1",
  "3xx": "bg-chart-3xx",
  "4xx": "bg-chart-4xx",
  "5xx": "bg-chart-5xx",
  unknown: "bg-chart-none",
};

// ---- routes table

const COLUMNS: { sort: ApiRouteSort; label: string; numeric: boolean }[] = [
  { sort: "route", label: "Route", numeric: false },
  { sort: "requests", label: "Requests", numeric: true },
  { sort: "rate_4xx", label: "4xx rate", numeric: true },
  { sort: "rate_5xx", label: "5xx rate", numeric: true },
  { sort: "p50", label: "p50", numeric: true },
  { sort: "p95", label: "p95", numeric: true },
  { sort: "p99", label: "p99", numeric: true },
];

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
  const setCursors = (f: (c: (string | null)[]) => (string | null)[]) =>
    setNav({ sig, cursors: f(cursors) });
  const cursor = cursors[cursors.length - 1] ?? null;
  const routes = useQuery(
    apiRoutesQuery(tenantId, state.range, state.filters, routesQuery(state, cursor)),
  );
  const page = cursors.length - 1;

  const sortBy = (sort: ApiRouteSort) => {
    const order =
      state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : defaultOrder(sort);
    set({ ...state, sort, order });
  };

  const data = routes.data;
  const first = page * PAGE_SIZE + 1;
  const last = data ? page * PAGE_SIZE + data.items.length : 0;

  return (
    <section aria-labelledby="api-routes" className="rounded-xl border bg-card">
      <div className="flex flex-wrap items-baseline justify-between gap-2 p-4 pb-2 sm:px-5">
        <h3 id="api-routes" className="font-semibold">
          Routes
        </h3>
        {data ? (
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {data.total === 0
              ? "No routes match"
              : `${formatCount(first)} to ${formatCount(last)} of ${formatCount(data.total)}`}
          </p>
        ) : null}
      </div>
      {routes.error ? (
        <div className="p-4 sm:px-5">
          <ErrorPanel error={routes.error} onRetry={() => routes.refetch()} />
        </div>
      ) : null}
      {routes.isPending ? (
        <div className="px-4 sm:px-5">
          <Loading label="Loading routes" />
        </div>
      ) : null}
      {data ? (
        <section
          aria-label="Routes table"
          // the table scrolls sideways on a narrow screen, so keyboard users
          // must be able to focus it to scroll. relative keeps the sr-only
          // header text inside it, or it widens the page on a phone
          // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable region needs focus (wcag 2.1.1)
          tabIndex={0}
          className={`relative overflow-x-auto transition-opacity ${routes.isPlaceholderData ? "opacity-60" : ""}`}
        >
          <table className="w-full min-w-[46rem] text-left text-sm">
            <thead className="border-y bg-muted/50 text-xs text-muted-foreground">
              <tr>
                {COLUMNS.map((c) => {
                  const on = state.sort === c.sort;
                  return (
                    <th
                      key={c.sort}
                      scope="col"
                      aria-sort={on ? (state.order === "asc" ? "ascending" : "descending") : "none"}
                      className={`px-2 py-1 font-medium ${c.numeric ? "text-right" : ""} ${c.sort === "route" ? "pl-4 sm:pl-5" : ""}`}
                    >
                      <button
                        type="button"
                        onClick={() => sortBy(c.sort)}
                        className={`inline-flex items-center gap-1 rounded px-1 py-1 hover:text-foreground ${on ? "text-foreground" : ""}`}
                      >
                        {c.label}
                        {on ? (
                          state.order === "asc" ? (
                            <ArrowUp aria-hidden className="size-3.5" />
                          ) : (
                            <ArrowDown aria-hidden className="size-3.5" />
                          )
                        ) : (
                          <ArrowUpDown aria-hidden className="size-3.5 opacity-50" />
                        )}
                      </button>
                    </th>
                  );
                })}
                <th scope="col" className="px-2 py-1 pr-4 sm:pr-5">
                  <span className="sr-only">Filter</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {data.items.map((r) => (
                <RouteRow
                  key={`${r.service}\u0000${r.method}\u0000${r.route}`}
                  item={r}
                  onFilter={() =>
                    set({
                      ...state,
                      filters: { service: r.service, method: r.method, route: r.route },
                    })
                  }
                />
              ))}
            </tbody>
          </table>
        </section>
      ) : null}
      {data && (page > 0 || data.next_cursor !== null) ? (
        <nav aria-label="Routes pages" className="flex justify-end gap-2 border-t p-3 sm:px-5">
          <Button
            variant="outline"
            size="sm"
            disabled={page === 0}
            onClick={() => setCursors((c) => c.slice(0, -1))}
          >
            <ChevronLeft aria-hidden />
            Previous
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={data.next_cursor === null || routes.isPlaceholderData}
            onClick={() => {
              const next = data.next_cursor;
              if (next !== null) setCursors((c) => [...c, next]);
            }}
          >
            Next
            <ChevronRight aria-hidden />
          </Button>
        </nav>
      ) : null}
    </section>
  );
}

function Cell({ children }: { children: ReactNode }) {
  return <td className="tabular px-3 py-2 text-right whitespace-nowrap">{children}</td>;
}

function Missing() {
  return (
    <>
      <span aria-hidden>-</span>
      <span className="sr-only">none</span>
    </>
  );
}

function RouteRow({ item, onFilter }: { item: ApiRouteItem; onFilter: () => void }) {
  const pct = (p: Percentile) => {
    const t = formatPercentile(p);
    return t === null ? <Missing /> : t;
  };
  const rate = (r: number | null, n: number) => {
    const t = formatRate(r);
    return t === null ? <Missing /> : <span title={`${formatCount(n)} requests`}>{t}</span>;
  };
  return (
    <tr data-route={item.route} data-service={item.service} data-method={item.method}>
      <th scope="row" className="max-w-[24rem] py-2 pr-3 pl-4 text-left font-normal sm:pl-5">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{item.method}</span>
          <span className="font-mono break-all">{item.route}</span>
        </span>
        <span className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          {serviceName(item.service)}
          {item.source === "span" ? <TracesBadge /> : null}
          {item.lossy ? <ApproxBadge /> : null}
        </span>
      </th>
      <Cell>{formatCount(item.requests)}</Cell>
      <Cell>{rate(item.rate_4xx, item.errors_4xx)}</Cell>
      <Cell>{rate(item.rate_5xx, item.errors_5xx)}</Cell>
      <Cell>{pct(item.p50)}</Cell>
      <Cell>{pct(item.p95)}</Cell>
      <Cell>{pct(item.p99)}</Cell>
      <td className="py-2 pr-4 pl-2 text-right sm:pr-5">
        <Button
          variant="ghost"
          size="sm"
          aria-label={`Filter to ${item.method} ${item.route} of ${serviceName(item.service)}`}
          onClick={onFilter}
        >
          <Filter aria-hidden />
        </Button>
      </td>
    </tr>
  );
}
