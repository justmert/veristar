import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade, stagger } from "@veristar/brand/motion";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  Filter,
  Search,
  Server,
  X,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
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
  ApiPoint,
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
  alignPrevious,
  ErrorChart,
  LatencyChart,
  PlotSkeleton,
  StatusClassBar,
  VolumeChart,
} from "@/components/api-charts";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { NativeSelect } from "@/components/form-field";
import { StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Sparkline, StatTile, type Trend } from "@/components/stat-tile";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  type Change,
  type DashboardState,
  defaultOrder,
  formatRange,
  latencyText,
  localInputToUtc,
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
  utcToLocalInput,
  validateCustom,
  writeState,
} from "@/lib/api-metrics";
import { formatCount, formatDate, formatShareOf } from "@/lib/format";
import { useProject } from "@/pages/project-layout";

// contract 7.3: the one line shown with every trace-derived badge
export const SPAN_NOTE =
  "Derived from sampled traces; counts are lower than the real request count when traces are sampled.";
// contract 7.3 span_data_ignored: the service has metric rows in the range
// and span rows that were left out. that happens when it switched from
// traces to metrics during the range, and also for every service that
// exports both signals, so the note names both cases
export const IGNORED_NOTE =
  "Counted from its metrics for the whole range. Numbers derived from its traces (sent alongside the metrics, or before it started sending metrics) are left out.";

export function ApiPage() {
  const { tenant } = useProject();
  const [params, setParams] = useSearchParams();
  const state = readState(params);
  const set = (next: DashboardState) => setParams(writeState(next));

  const services = useQuery(apiServicesQuery(tenant.id, state.range));
  const warnings = useQuery(apiWarningsQuery(tenant.id, state.range));

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-4">
        <PageHeader
          title="API traffic"
          description="Requests, errors and latency of your HTTP API, per normalized route. Times are UTC; hourly and daily numbers are updated every few minutes."
        />
      </div>

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
          {services.data.range.resolution_note ? (
            <Notice tone="info">{RESOLUTION_NOTE[services.data.range.resolution_note]}</Notice>
          ) : null}
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
    <StateBlock
      kind="empty"
      icon={<Server aria-hidden strokeWidth={1.75} />}
      title="No API requests in this range"
      action={
        <Button asChild>
          <Link to={`/projects/${tenantId}/connect-api`}>Set up API analytics</Link>
        </Button>
      }
    >
      Nothing arrived from your services between {formatRange(range)}. Hourly and daily numbers are
      updated every few minutes; the 1 hour range shows the newest minutes. If your app does not
      send data yet, create a token and add the second exporter described on the Connect tab.
    </StateBlock>
  );
}

// ---- controls: range, custom range and filters in one row above
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
    <div className="flex flex-col gap-3">
      <Filters
        state={state}
        set={set}
        services={services}
        range={
          <Segmented
            label="Range"
            value={selected}
            onChange={choose}
            options={[
              ...PRESETS.map((p) => ({ value: p.value as string, label: p.value })),
              { value: "custom", label: "Custom" },
            ]}
            hint={range ? RESOLUTION_LABEL[range.resolution] : undefined}
            className="max-w-full overflow-x-auto"
          />
        }
      />
      <AnimatePresence initial={false}>
        {custom ? (
          <motion.div key="custom" {...fade}>
            <CustomRange
              key={state.range.range === "custom" ? `${state.range.from}|${state.range.to}` : "new"}
              current={state.range}
              resolved={range}
              onApply={(r) => set({ ...state, range: r })}
            />
          </motion.div>
        ) : null}
      </AnimatePresence>
      {range ? (
        <p className="text-small text-fg-2">
          <span className="font-medium text-fg">{formatRange(range)}</span>,{" "}
          {RESOLUTION_LABEL[range.resolution]}
        </p>
      ) : null}
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
      className="grid gap-3 rounded-lg border border-line bg-raised p-4 sm:grid-cols-[1fr_1fr_auto] sm:items-start"
    >
      <div className="flex flex-col gap-1">
        <label htmlFor={fromId} className="text-small font-medium text-fg">
          From (UTC)
        </label>
        <Input
          id={fromId}
          type="datetime-local"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          aria-invalid={errors.from ? true : undefined}
          aria-describedby={errors.from ? `${fromId}-err` : undefined}
          className="font-mono max-lg:h-(--vs-control-touch)"
        />
        {errors.from ? (
          <p id={`${fromId}-err`} className="text-small text-critical">
            {errors.from}
          </p>
        ) : null}
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={toId} className="text-small font-medium text-fg">
          To (UTC)
        </label>
        <Input
          id={toId}
          type="datetime-local"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          aria-invalid={errors.to ? true : undefined}
          aria-describedby={errors.to ? `${toId}-err` : undefined}
          className="font-mono max-lg:h-(--vs-control-touch)"
        />
        {errors.to ? (
          <p id={`${toId}-err`} className="text-small text-critical">
            {errors.to}
          </p>
        ) : null}
      </div>
      <Button type="submit" className="max-lg:h-(--vs-control-touch) sm:mt-6">
        Apply range
      </Button>
      <p className="text-caption font-normal text-fg-3 sm:col-span-3">
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

// a compact select with its name inside the control's frame, as the board's
// filter buttons; a native select works on phones and with every reader
function FilterSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex h-(--vs-control-sm) items-center rounded-md border border-line-input bg-sunken pl-2 transition-colors duration-(--vs-duration-reveal) ease-standard focus-within:border-fg-3 hover:border-fg-3 max-lg:h-(--vs-control-touch)">
      <label htmlFor={id} className="text-small whitespace-nowrap text-fg-3">
        {label}
      </label>
      <NativeSelect
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        size="sm"
        bare
        className="h-full max-w-48"
      >
        {children}
      </NativeSelect>
    </div>
  );
}

function Filters({
  state,
  set,
  services,
  range,
}: {
  state: DashboardState;
  set: (s: DashboardState) => void;
  services: ApiServiceItem[];
  range: ReactNode;
}) {
  const f = state.filters;
  const [route, setRoute] = useState(f.route ?? "");
  useEffect(() => setRoute(f.route ?? ""), [f.route]);
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
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {range}
        <FilterSelect
          label="Service"
          value={f.service === null ? ALL : opt(f.service)}
          onChange={(v) => set({ ...state, filters: { ...f, service: unopt(v) } })}
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
        </FilterSelect>
        <FilterSelect
          label="Method"
          value={f.method === null ? ALL : opt(f.method)}
          onChange={(v) => set({ ...state, filters: { ...f, method: unopt(v) } })}
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
        </FilterSelect>
        <form
          onSubmit={submitRoute}
          aria-label="Route filter"
          className="flex min-w-0 flex-1 basis-64 items-center gap-1"
        >
          <label htmlFor={routeId} className="sr-only">
            Route
          </label>
          <div className="flex h-(--vs-control-sm) min-w-0 flex-1 items-center gap-2 rounded-md border border-line-input bg-sunken px-2 transition-colors duration-(--vs-duration-reveal) ease-standard focus-within:border-fg-3 hover:border-fg-3 max-lg:h-(--vs-control-touch)">
            <Search aria-hidden className="size-3.5 shrink-0 text-fg-3" />
            <input
              id={routeId}
              value={route}
              onChange={(e) => setRoute(e.target.value)}
              placeholder="Route, like /v1/accounts/{id}"
              autoComplete="off"
              spellCheck={false}
              maxLength={1024}
              className="h-full min-w-0 flex-1 bg-transparent font-mono text-caption font-normal text-fg outline-none placeholder:text-fg-3"
            />
          </div>
          <Button
            type="submit"
            variant="outline"
            size="sm"
            className="max-lg:h-(--vs-control-touch)"
          >
            Apply
          </Button>
        </form>
      </div>
      <p className="text-caption font-normal text-fg-3">
        Routes match exactly, as normalized (placeholders like {"{id}"}). Pick one from the routes
        table below with its filter button.
      </p>
      {active ? (
        <ul
          aria-label="Active filters"
          className="flex animate-fade-in flex-wrap items-center gap-2"
        >
          {(
            [
              ["service", "Service"],
              ["method", "Method"],
              ["route", "Route"],
            ] as const
          ).map(([k, label]) =>
            f[k] === null ? null : (
              <li key={k}>
                <span className="inline-flex h-7 items-center gap-1 rounded-full border border-line-strong bg-raised pr-1 pl-2 text-caption font-normal text-fg-2">
                  {label}:{" "}
                  <span className="font-mono text-fg">{f[k] === "" ? "(empty)" : f[k]}</span>
                  <button
                    type="button"
                    aria-label={`Remove the ${label.toLowerCase()} filter`}
                    onClick={() => set({ ...state, filters: { ...f, [k]: null } })}
                    className="rounded-full p-1 text-fg-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover hover:text-fg focus-ring"
                  >
                    <X aria-hidden className="size-3.5" />
                  </button>
                </span>
              </li>
            ),
          )}
          <li>
            <Button
              variant="link"
              size="xs"
              onClick={() =>
                set({ ...state, filters: { service: null, route: null, method: null } })
              }
            >
              Clear filters
            </Button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}

// ---- warnings about the data's sources

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
  coarse_buckets: {
    title: "Some latency percentiles are not reliable",
    body: "A histogram arrived with buckets much wider than the standard ones, for example millisecond bounds on a metric in seconds. Its requests were placed at the top of their wide bucket, so percentiles for it can be ten times too high or more. Record http.server.request.duration in seconds with the standard bucket bounds.",
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
    <section aria-label="Ingest warnings" className="flex flex-col gap-2">
      {shown.map((t) => (
        <div key={t.kind} data-warning={t.kind}>
          <Notice
            tone={t.kind === "secret_in_path" ? "critical" : "warning"}
            title={WARNING_TEXT[t.kind].title}
          >
            <p>{WARNING_TEXT[t.kind].body}</p>
            <p className="mt-1 text-caption font-normal text-fg-3">
              {formatCount(t.count)} {t.count === 1 ? "time" : "times"} in this range
              {t.last_day ? `, last on ${formatDate(`${t.last_day}T00:00:00Z`)}` : ""}
            </p>
          </Notice>
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
    <span title={SPAN_NOTE} aria-describedby={id} className="inline-flex">
      <Pill tone="warning">From traces</Pill>
    </span>
  );
}

function ApproxBadge() {
  return <Pill tone="info">Approximate</Pill>;
}

// ---- the dashboard proper

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
  const [showPrev, setShowPrev] = useState(true);
  const togglePrev = () => setShowPrev((v) => !v);

  return (
    <div className="flex flex-col gap-5">
      {overview.error ? (
        <ErrorPanel error={overview.error} onRetry={() => overview.refetch()} />
      ) : (
        <OverviewTiles
          overview={overview.data ?? null}
          dim={overview.isPlaceholderData}
          points={series.data?.points ?? null}
          previous={previous}
          metric={metric}
          onMetric={setMetric}
        />
      )}

      {series.error ? (
        <ErrorPanel error={series.error} onRetry={() => series.refetch()} />
      ) : series.data ? (
        <AnimatePresence mode="wait" initial={false}>
          <motion.div key={metric === "4xx" || metric === "5xx" ? "errors" : metric} {...fade}>
            {metric === "requests" ? (
              <VolumeChart
                range={series.data.range}
                points={series.data.points}
                dim={series.isPlaceholderData}
                previous={previous}
                showPrevious={showPrev}
                onTogglePrevious={togglePrev}
              />
            ) : metric === "p95" ? (
              <LatencyChart
                range={series.data.range}
                points={series.data.points}
                dim={series.isPlaceholderData}
                previous={previous}
                showPrevious={showPrev}
                onTogglePrevious={togglePrev}
              />
            ) : (
              <ErrorChart
                range={series.data.range}
                points={series.data.points}
                dim={series.isPlaceholderData}
                previous={previous}
                showPrevious={showPrev}
                onTogglePrevious={togglePrev}
                focus={metric}
              />
            )}
          </motion.div>
        </AnimatePresence>
      ) : (
        <Panel title="Requests">
          <PlotSkeleton label="Loading the charts" />
        </Panel>
      )}

      <RoutesSection tenantId={tenantId} state={state} set={set} />

      {status.error ? (
        <ErrorPanel error={status.error} onRetry={() => status.refetch()} />
      ) : status.data ? (
        <StatusSection status={status.data} dim={status.isPlaceholderData} />
      ) : (
        <Loading label="Loading status codes" />
      )}

      <ServicesSection services={services} />
    </div>
  );
}

function ServicesSection({ services }: { services: ApiServiceItem[] }) {
  const noteId = useId();
  const anySpan = services.some((s) => s.source === "span");
  const max = Math.max(1, ...services.map((s) => s.requests));
  return (
    <Panel title="Services" flush>
      <motion.ul
        aria-label="Services"
        initial="initial"
        animate="animate"
        variants={stagger}
        className="divide-y divide-line-subtle border-t border-line"
      >
        {services.map((s) => (
          <StaggerItem
            as="li"
            key={s.service}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 sm:px-5"
          >
            <span className="min-w-0 flex-1 truncate text-body font-medium text-fg">
              {serviceName(s.service)}
            </span>
            {s.source === "span" ? <TracesBadge id={noteId} /> : null}
            {s.lossy ? <ApproxBadge /> : null}
            <span className="flex items-center gap-2">
              <span
                aria-hidden
                className="hidden h-1.5 w-24 overflow-hidden rounded-bar bg-hover sm:block"
              >
                <span
                  className="block h-full rounded-bar bg-series-1"
                  // the bar is the service's share of the busiest one: data
                  style={{ width: `${(s.requests / max) * 100}%` }}
                />
              </span>
              <span className="font-mono text-small text-fg-2">
                {formatCount(s.requests)} {s.requests === 1 ? "request" : "requests"}
              </span>
            </span>
            {s.span_data_ignored ? (
              <span className="basis-full text-caption font-normal text-fg-3">{IGNORED_NOTE}</span>
            ) : null}
          </StaggerItem>
        ))}
      </motion.ul>
      {anySpan ? (
        <p
          id={noteId}
          className="border-t border-line px-4 py-3 text-caption font-normal text-fg-3 sm:px-5"
        >
          From traces: {SPAN_NOTE}
        </p>
      ) : null}
    </Panel>
  );
}

function percentileText(p: Percentile): string {
  return latencyText(p) ?? "No requests";
}

function trend(change: Change, upIsBad: boolean | null): Trend | null {
  if (change.direction === null || !change.text) return null;
  const direction = change.direction === "same" ? "flat" : change.direction;
  const good = upIsBad === null || direction === "flat" ? null : (direction === "up") !== upIsBad;
  return { text: change.text, direction, good, period: "previous range" };
}

// a line under a tile. a change with nothing to compare against has no
// arrow, so it is said in words here
function Note({ children }: { children: ReactNode }) {
  return <span className="text-caption font-normal text-fg-3">{children}</span>;
}

function OverviewTiles({
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
  const spark = (v: (p: ApiPoint) => number | null, label: string) =>
    points && points.length > 1 ? (
      <Sparkline
        label={label}
        values={points.map((p) => v(p) ?? 0)}
        previous={previous ? previous.map((q) => (q ? (v(q) ?? 0) : 0)) : undefined}
      />
    ) : null;

  if (!overview)
    return (
      <section aria-label="Summary" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {["Requests", "4xx rate", "5xx rate", "p95 latency"].map((l) => (
          <StatTile key={l} label={l} value="" state="loading" />
        ))}
      </section>
    );

  const cur = overview.current.summary;
  const prev = overview.previous.summary;
  const rate = (r: number) => rateText(r) ?? "";
  const reqChange = relativeChange(cur.requests, prev.requests);
  const p95Change = percentileChange(cur.p95, prev.p95);
  const words = (c: Change) => (c.direction === null && c.text ? <Note>{c.text}</Note> : null);
  const pct = (k: "p50" | "p99") => {
    const c = percentileChange(cur[k], prev[k]);
    return (
      <span
        data-tile={k}
        className="flex flex-wrap items-baseline gap-x-1 text-caption font-normal"
      >
        <span className="text-fg-3">{k}</span>
        <span className="font-mono text-fg">{percentileText(cur[k])}</span>
        {c.text && c.direction !== null ? <span className="text-fg-3">{c.text}</span> : null}
      </span>
    );
  };

  return (
    <section
      aria-labelledby="api-summary"
      className={cn(
        "flex flex-col gap-2 transition-opacity duration-(--vs-duration-change) ease-standard",
        dim && "opacity-60",
      )}
    >
      <h2 id="api-summary" className="sr-only">
        Summary
      </h2>
      <p className="text-caption font-normal text-fg-3">
        Compared with the previous range of equal length, {formatRange(overview.previous.range)}.
        Select a figure to chart it.
      </p>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div data-tile="Requests" className="flex min-w-0">
          <StatTile
            className="flex-1"
            label="Requests"
            value={formatCount(cur.requests)}
            numeric={cur.requests}
            format={(n) => formatCount(Math.round(n))}
            trend={trend(reqChange, null)}
            pressed={metric === "requests"}
            onPress={() => onMetric("requests")}
            spark={spark((p) => p.requests, "Requests per bucket, with the previous period")}
            footer={
              <span className="flex flex-col">
                {words(reqChange) ?? <Note>Previous range: {formatCount(prev.requests)}</Note>}
              </span>
            }
          />
        </div>
        {(["4xx", "5xx"] as const).map((k) => {
          const r = k === "4xx" ? cur.rate_4xx : cur.rate_5xx;
          const pr = k === "4xx" ? prev.rate_4xx : prev.rate_5xx;
          const n = k === "4xx" ? cur.errors_4xx : cur.errors_5xx;
          const c = rateChange(r, pr);
          return (
            <div key={k} data-tile={`${k} rate`} className="flex min-w-0">
              <StatTile
                className="flex-1"
                label={`${k} rate`}
                hint={`Share of requests answered with a ${k} status`}
                value={rateText(r) ?? "No requests"}
                numeric={r ?? undefined}
                format={rate}
                trend={trend(c, true)}
                pressed={metric === k}
                onPress={() => onMetric(k)}
                spark={spark(
                  (p) => (k === "4xx" ? p.rate_4xx : p.rate_5xx),
                  `${k} rate per bucket, with the previous period`,
                )}
                footer={
                  <span className="flex flex-col">
                    <Note>
                      {formatCount(n)} {k === "4xx" ? "client errors" : "server errors"}
                    </Note>
                    {words(c) ?? <Note>Previous range: {rateText(pr) ?? "no requests"}</Note>}
                  </span>
                }
              />
            </div>
          );
        })}
        <div data-tile="p95" className="flex min-w-0">
          <StatTile
            className="flex-1"
            label="p95 latency"
            hint="95 of 100 requests were faster than this"
            value={percentileText(cur.p95)}
            trend={trend(p95Change, true)}
            pressed={metric === "p95"}
            onPress={() => onMetric("p95")}
            spark={spark((p) => {
              const v = plotPercentile(p.p95);
              return v === null ? null : Math.min(v, OPEN_BUCKET_CEILING);
            }, "p95 latency per bucket, with the previous period")}
            footer={
              <span className="flex flex-col gap-1">
                {words(p95Change) ?? <Note>Previous range: {percentileText(prev.p95)}</Note>}
                {pct("p50")}
                {pct("p99")}
                {cur.lossy ? <Note>Percentiles are approximate</Note> : null}
              </span>
            }
          />
        </div>
      </div>
      <InfrastructureNote overview={overview} />
    </section>
  );
}

// infrastructure requests stay out of every figure above; this line says
// how many there were and since when they are counted apart
function InfrastructureNote({ overview }: { overview: ApiOverview }) {
  const n = overview.current.summary.infrastructure_requests;
  const from = overview.traffic_classes.applied_from;
  return (
    <p data-testid="infrastructure-requests" className="text-caption font-normal text-fg-3">
      {formatCount(n)} infrastructure {n === 1 ? "request" : "requests"} (CORS preflights, health
      and probe routes, known health checkers) are not part of these figures.
      {from ? ` They are counted apart from ${formatDate(from)} on.` : null}
    </p>
  );
}

function StatusSection({ status, dim }: { status: ApiStatus; dim: boolean }) {
  const max = Math.max(1, ...status.codes.map((c) => c.requests));
  return (
    <section
      aria-labelledby="api-status"
      className={cn(
        "grid gap-4 transition-opacity duration-(--vs-duration-change) ease-standard lg:grid-cols-2",
        dim && "opacity-60",
      )}
    >
      <h2 id="api-status" className="sr-only">
        Status codes
      </h2>
      <Panel
        title="By status class"
        description={`${formatCount(status.requests)} requests in the range.`}
      >
        <StatusClassBar status={status} />
      </Panel>
      <Panel title="By status code" flush>
        {status.codes.length === 0 ? (
          <p className="px-4 pb-4 text-small text-fg-2 sm:px-5">No requests in the range.</p>
        ) : (
          <div className="relative max-h-80 overflow-auto">
            <table className="w-full text-left text-small">
              <thead className="text-caption text-fg-2">
                <tr className="border-y border-line">
                  <th scope="col" className="h-9 pr-3 pl-4 font-medium sm:pl-5">
                    Code
                  </th>
                  <th scope="col" className="w-full pr-3 font-medium">
                    <span className="sr-only">Share bar</span>
                  </th>
                  <th scope="col" className="pr-3 text-right font-medium">
                    Requests
                  </th>
                  <th scope="col" className="pr-4 text-right font-medium sm:pr-5">
                    Share
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {status.codes.map((c) => (
                  <tr key={c.status_code} data-code={c.status_code} className="hover:bg-hover">
                    <th scope="row" className="h-9 pr-3 pl-4 font-mono font-medium text-fg sm:pl-5">
                      {c.status_code === 0 ? "none" : c.status_code}
                    </th>
                    <td className="pr-3" aria-hidden>
                      <div
                        className={cn("h-1.5 rounded-bar", CODE_BAR[c.class])}
                        // the bar's length is the value, so it is data, not a class
                        style={{ width: `${Math.max(1, (c.requests / max) * 100)}%` }}
                      />
                    </td>
                    <td className="pr-3 text-right font-mono text-fg">{formatCount(c.requests)}</td>
                    <td className="pr-4 text-right font-mono text-fg-3 sm:pr-5">
                      {formatShareOf(c.requests, status.requests)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </section>
  );
}

const CODE_BAR: Record<ApiStatus["codes"][number]["class"], string> = {
  "1xx": "bg-series-3",
  "2xx": "bg-series-1",
  "3xx": "bg-series-4",
  "4xx": "bg-warning",
  "5xx": "bg-critical",
  unknown: "bg-series-prev",
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

// the page's most failing and slowest routes are marked in their rows, so
// the one to look at stands out without reading every number
function standouts(items: ApiRouteItem[]) {
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
  const max = data ? Math.max(1, ...data.items.map((r) => r.requests)) : 1;
  const marks = data ? standouts(data.items) : { failing: null, slow: null };
  const sortLabel = COLUMNS.find((c) => c.sort === state.sort)?.label ?? state.sort;

  return (
    <section
      aria-labelledby="api-routes"
      className="flex min-w-0 flex-col rounded-lg border border-line bg-raised"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-3 sm:px-5">
        <h2 id="api-routes" className="text-title text-fg">
          Routes
        </h2>
        {data ? (
          <p className="text-caption font-normal text-fg-3" aria-live="polite">
            {data.total === 0
              ? "No routes match"
              : `${formatCount(first)} to ${formatCount(last)} of ${formatCount(data.total)}, sorted by ${sortLabel}`}
          </p>
        ) : null}
      </div>
      {routes.error ? (
        <div className="px-4 pb-4 sm:px-5">
          <ErrorPanel error={routes.error} onRetry={() => routes.refetch()} />
        </div>
      ) : null}
      {routes.isPending ? (
        <div className="px-4 pb-4 sm:px-5">
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
          className={cn(
            "relative overflow-x-auto transition-opacity duration-(--vs-duration-change) ease-standard focus-ring",
            routes.isPlaceholderData && "opacity-60",
          )}
        >
          <table className="w-full min-w-[58rem] text-left text-small">
            <thead className="text-caption text-fg-2">
              <tr className="border-y border-line">
                {COLUMNS.map((c) => {
                  const on = state.sort === c.sort;
                  return (
                    <th
                      key={c.sort}
                      scope="col"
                      aria-sort={on ? (state.order === "asc" ? "ascending" : "descending") : "none"}
                      className={cn(
                        "h-9 px-2 font-medium",
                        c.numeric && "text-right",
                        c.sort === "route" && "pl-3 sm:pl-4",
                        c.sort === "requests" && "w-56",
                      )}
                    >
                      <button
                        type="button"
                        onClick={() => sortBy(c.sort)}
                        className={cn(
                          "inline-flex items-center gap-1 rounded-sm px-1 py-1 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring",
                          on && "text-fg",
                        )}
                      >
                        {c.label}
                        {on ? (
                          state.order === "asc" ? (
                            <ArrowUp aria-hidden className="size-3.5" />
                          ) : (
                            <ArrowDown aria-hidden className="size-3.5" />
                          )
                        ) : (
                          <ArrowUpDown aria-hidden className="size-3.5 text-fg-3" />
                        )}
                      </button>
                    </th>
                  );
                })}
                <th scope="col" className="px-2 pr-4 sm:pr-5">
                  <span className="sr-only">Filter</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {data.items.map((r) => (
                <RouteRow
                  key={`${r.service}\u0000${r.method}\u0000${r.route}`}
                  item={r}
                  max={max}
                  failing={marks.failing === r}
                  slow={marks.slow === r}
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
        <nav
          aria-label="Routes pages"
          className="flex justify-end gap-2 border-t border-line px-4 py-3 sm:px-5"
        >
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

function Cell({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <td className={cn("px-3 text-right font-mono whitespace-nowrap", className ?? "text-fg-2")}>
      {children}
    </td>
  );
}

function Missing() {
  return (
    <>
      <span aria-hidden>-</span>
      <span className="sr-only">none</span>
    </>
  );
}

function RouteRow({
  item,
  max,
  failing,
  slow,
  onFilter,
}: {
  item: ApiRouteItem;
  max: number;
  failing: boolean;
  slow: boolean;
  onFilter: () => void;
}) {
  const pct = (p: Percentile) => {
    const t = latencyText(p);
    return t === null ? <Missing /> : t;
  };
  const rate = (r: number | null, n: number) => {
    const t = rateText(r);
    return t === null ? <Missing /> : <span title={`${formatCount(n)} requests`}>{t}</span>;
  };
  return (
    <tr
      data-route={item.route}
      data-service={item.service}
      data-method={item.method}
      className="transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover"
    >
      <th scope="row" className="max-w-[26rem] py-2 pr-3 pl-4 text-left font-normal sm:pl-5">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="w-12 shrink-0 font-mono text-axis text-fg-2">{item.method}</span>
          <span className="font-mono break-all text-fg">{item.route}</span>
          {failing ? <Pill tone="critical">Most 5xx</Pill> : null}
          {slow ? <Pill tone="warning">Slowest p95</Pill> : null}
        </span>
        <span className="mt-1 flex flex-wrap items-center gap-2 pl-12 text-caption font-normal text-fg-3">
          {serviceName(item.service)}
          {item.source === "span" ? <TracesBadge /> : null}
          {item.lossy ? <ApproxBadge /> : null}
          {item.infrastructure_requests > 0 ? (
            <span title="CORS preflights, health and probe routes and known health checkers, not counted in requests">
              {formatCount(item.infrastructure_requests)} infrastructure
            </span>
          ) : null}
        </span>
      </th>
      <td className="px-3">
        <span className="flex items-center gap-2">
          <span className="w-16 shrink-0 text-right font-mono text-fg">
            {formatCount(item.requests)}
          </span>
          <span aria-hidden className="h-1.5 min-w-8 flex-1 overflow-hidden rounded-bar bg-hover">
            <span
              className="block h-full rounded-bar bg-series-1"
              // the row's share of the busiest route on this page: data, so it
              // cannot be a class
              style={{ width: `${(item.requests / max) * 100}%` }}
            />
          </span>
        </span>
      </td>
      <Cell className={(item.rate_4xx ?? 0) >= 0.05 ? "text-warning" : undefined}>
        {rate(item.rate_4xx, item.errors_4xx)}
      </Cell>
      <Cell className={(item.rate_5xx ?? 0) > 0 ? "text-critical" : undefined}>
        {rate(item.rate_5xx, item.errors_5xx)}
      </Cell>
      <Cell>{pct(item.p50)}</Cell>
      <Cell className={slow ? "text-warning" : "text-fg"}>{pct(item.p95)}</Cell>
      <Cell>{pct(item.p99)}</Cell>
      <td className="py-2 pr-4 pl-2 text-right sm:pr-5">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Filter to ${item.method} ${item.route} of ${serviceName(item.service)}`}
          title="Filter to this route"
          onClick={onFilter}
        >
          <Filter aria-hidden />
        </Button>
      </td>
    </tr>
  );
}
