// the project's web dashboard (contract 18): the seven web metrics of the
// rfp for any range of utc days against the previous period, breakdowns,
// the hours of one day and the visitors right now, for the whole project or
// one site. totals, days and the referrer, country and environment
// breakdowns come from the daily aggregates operators read too; pages,
// events, utm values, hours and the last minutes come from the analytics
// engine, read as the member.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade, stagger } from "@veristar/brand/motion";
import { ChevronDown, Globe } from "lucide-react";
import { motion } from "motion/react";
import {
  type FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { sitesQuery } from "@/api/queries";
import type { Site } from "@/api/schemas";
import {
  type WebBreakdown,
  type WebDimension,
  type WebQuery,
  type WebSummary,
  webBreakdownQuery,
  webHourlyQuery,
  webKeys,
  webRealtimeQuery,
  webSummaryQuery,
} from "@/api/web";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { NativeSelect, TextField } from "@/components/form-field";
import { IntegrityReportView } from "@/components/integrity";
import { StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { ProjectOnchainSection } from "@/components/onchain";
import { PageHeader, Panel } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { StatTile } from "@/components/stat-tile";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { type ChartBucket, TimeChart } from "@/components/web-charts";
import { errorText } from "@/lib/errors";
import { formatCount, formatDateTimeUtc } from "@/lib/format";
import { formatDayRange } from "@/lib/global";
import {
  DIMENSION_LABEL,
  formatMetric,
  formatShare,
  higherIsBetter,
  isSingleDay,
  keyLabel,
  lastDaysOf,
  metricValue,
  pointValue,
  readWebState,
  shareOf,
  UTM_DIMENSIONS,
  validateWebDays,
  WEB_PRESETS,
  type WebMetric,
  type WebPreset,
  type WebState,
  webDays,
  webQuery,
  webTrend,
  writeWebState,
} from "@/lib/web-dashboard";
import { useProject } from "./project-layout";

const INTEGRITY_MAX_DAYS = 366;

const METRIC_TILES: { metric: WebMetric; label: string; hint: string }[] = [
  { metric: "pageviews", label: "Page views", hint: "Pages loaded in a browser" },
  {
    metric: "visitors",
    label: "Unique visitors",
    hint: "Distinct visitors per UTC day, added up over the range; no visitor is followed from one day to the next",
  },
  { metric: "visits", label: "Visits", hint: "A visit ends after 30 minutes without activity" },
  {
    metric: "bounce_rate",
    label: "Bounce rate",
    hint: "Visits with one page view and no custom event",
  },
  {
    metric: "avg_visit_duration",
    label: "Visit duration",
    hint: "Average time from the first to the last page view of a visit",
  },
];

const tickDay = new Intl.DateTimeFormat("en", { month: "short", day: "numeric", timeZone: "UTC" });
const labelDay = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  year: "numeric",
  timeZone: "UTC",
});
const tickHour = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});

export function WebPage() {
  const { tenant } = useProject();
  const [params, setParams] = useSearchParams();
  const state = readWebState(params);
  const set = (next: Partial<WebState>) =>
    setParams(writeWebState({ ...state, ...next }), { replace: true });
  const sites = useQuery(sitesQuery(tenant.id));
  const provisioning = useProvisioningPending(tenant.id);
  const now = new Date();

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const listed = sites.data.filter((s) => s.status !== "removed");
  const selected = state.site ? listed.find((s) => s.id === state.site) : undefined;
  // a site in the address that is deleted or not one of this project's: the
  // select shows all sites, so the figures are those of all sites too
  const unknownSite = state.site !== null && !selected;
  const q = webQuery(unknownSite ? { ...state, site: null } : state, now);
  // the integrity report covers at most 366 days (17.2)
  const graded = lastDaysOf(q, INTEGRITY_MAX_DAYS);
  const scopeSites = selected ? [selected] : listed;
  const utm = scopeSites.some((s) => s.collect_utm);

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Web analytics"
        description="Page views, visitors, visits, bounce rate, visit duration, sources, countries and devices of your sites. Days are UTC."
        actions={
          listed.length > 0 ? (
            <>
              <label className="sr-only" htmlFor="web-site">
                Site
              </label>
              <NativeSelect
                id="web-site"
                size="sm"
                value={state.site ?? ""}
                onChange={(e) => set({ site: e.target.value || null })}
              >
                <option value="">All sites</option>
                {listed.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name} ({s.domain})
                  </option>
                ))}
              </NativeSelect>
              <Segmented
                label="Traffic"
                value={state.band}
                options={[
                  { value: "primary", label: "Counted" },
                  { value: "quarantine", label: "Quarantined" },
                ]}
                onChange={(band) => set({ band })}
              />
            </>
          ) : null
        }
      />

      {provisioning ? (
        <Notice tone="info" title="Your access is being set up">
          Your access to the live figures of this project (pages, events, hours and visitors right
          now) is being created. They load on their own once it is ready; the daily totals show
          already.
        </Notice>
      ) : null}

      {unknownSite && listed.length > 0 ? (
        <Notice
          tone="warning"
          title="That site is not part of this project"
          actions={
            <Button variant="outline" size="sm" onClick={() => set({ site: null })}>
              Clear the site
            </Button>
          }
        >
          The address names a site that was deleted or belongs to another project. The figures below
          cover all sites of this project.
        </Notice>
      ) : null}

      {listed.length === 0 ? (
        <StateBlock
          kind="empty"
          icon={<Globe aria-hidden strokeWidth={1.75} />}
          title="No sites yet"
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}`}>Add a site on the overview</Link>
            </Button>
          }
        >
          The dashboard fills once a site sends its first page view.
        </StateBlock>
      ) : (
        <>
          <RangeBar state={state} set={set} now={now} />
          {state.band === "primary" ? <Realtime tenantId={tenant.id} site={q.site} /> : null}
          <Dashboard tenantId={tenant.id} state={state} set={set} q={q} sites={listed} utm={utm} />
          <IntegrityReportView
            scope="tenant"
            tenantId={tenant.id}
            range={graded}
            siteNames={Object.fromEntries(sites.data.map((s) => [s.id, s.name]))}
            intro={
              graded.from === q.from
                ? "The integrity grade of your web traffic over this range, with the reasons behind it. SCF operators see the same report."
                : `The integrity grade of your web traffic over the last ${INTEGRITY_MAX_DAYS} days of this range (${formatDayRange(graded.from, graded.to)}), with the reasons behind it. SCF operators see the same report.`
            }
          />
          <ProjectOnchainSection tenantId={tenant.id} range={{ from: q.from, to: q.to }} />
          <DbIpAttribution />
        </>
      )}
    </div>
  );
}

// whether a web read of the project is waiting for its analytics access
// (409 provisioning_pending, 18.9). the query client keeps asking; this only
// tells people why a panel is still loading.
function useProvisioningPending(tenantId: string): boolean {
  const cache = useQueryClient().getQueryCache();
  const read = useCallback(
    () =>
      cache.findAll({ queryKey: webKeys.all(tenantId) }).some((q) => {
        const e = q.state.error ?? q.state.fetchFailureReason;
        return e instanceof ApiError && e.code === "provisioning_pending";
      }),
    [cache, tenantId],
  );
  const subscribe = useCallback((cb: () => void) => cache.subscribe(cb), [cache]);
  return useSyncExternalStore(subscribe, read, read);
}

// ---- range

function RangeBar({
  state,
  set,
  now,
}: {
  state: WebState;
  set: (s: Partial<WebState>) => void;
  now: Date;
}) {
  const days = webDays(state, now);
  const [custom, setCustom] = useState(state.preset === "custom");
  const [draft, setDraft] = useState(days);
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  useEffect(() => {
    if (state.preset === "custom") setCustom(true);
  }, [state.preset]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateWebDays(draft, new Date());
    setErrors(v);
    if (v.from || v.to) return;
    set({ preset: "custom", custom: draft });
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented<WebPreset>
          label="Range"
          value={custom ? "custom" : state.preset}
          options={WEB_PRESETS}
          onChange={(v) => {
            if (v === "custom") {
              setDraft(days);
              setCustom(true);
              return;
            }
            setCustom(false);
            set({ preset: v });
          }}
        />
        <span className="text-caption text-fg-3" aria-live="polite">
          {formatDayRange(days.from, days.to)}
        </span>
      </div>
      {custom ? (
        <form
          aria-label="Custom range"
          onSubmit={submit}
          noValidate
          className="flex animate-reveal-in flex-wrap items-end gap-3"
        >
          <div className="w-40">
            <TextField
              label="From (UTC day)"
              type="date"
              value={draft.from}
              error={errors.from}
              onChange={(e) => setDraft({ ...draft, from: e.target.value })}
            />
          </div>
          <div className="w-40">
            <TextField
              label="To (UTC day)"
              type="date"
              value={draft.to}
              error={errors.to}
              onChange={(e) => setDraft({ ...draft, to: e.target.value })}
            />
          </div>
          <Button type="submit" variant="outline">
            Apply range
          </Button>
        </form>
      ) : null}
    </div>
  );
}

// ---- right now

function Realtime({ tenantId, site }: { tenantId: string; site: string | null }) {
  const rt = useQuery(webRealtimeQuery(tenantId, site));
  const max = Math.max(1, ...(rt.data?.minutes.map((m) => m.pageviews) ?? [0]));
  return (
    <section
      aria-label="Right now"
      className="flex flex-wrap items-center gap-x-5 gap-y-3 rounded-lg border border-line bg-raised px-4 py-3 sm:px-5"
    >
      <div className="flex items-center gap-3">
        <Pill tone={rt.data && rt.data.visitors > 0 ? "success" : "info"} live={!!rt.data}>
          Right now
        </Pill>
        {rt.isPending ? (
          <Skeleton className="h-6 w-28" />
        ) : rt.error ? (
          <span className="text-small text-fg-2">{errorText(rt.error)}</span>
        ) : (
          <span className="text-body text-fg">
            <span className="text-title tabular-nums" data-testid="web-active">
              {formatCount(rt.data.visitors)}
            </span>{" "}
            {rt.data.visitors === 1 ? "visitor" : "visitors"} in the last 5 minutes
          </span>
        )}
      </div>
      {rt.data ? (
        <figure className="flex min-w-48 flex-1 flex-col gap-1">
          <div
            role="img"
            aria-label={`Page views per minute over the last 30 minutes: ${rt.data.minutes
              .map((m) => m.pageviews)
              .join(", ")}`}
            className="flex h-8 items-end gap-px"
          >
            {rt.data.minutes.map((m) => (
              <span
                key={m.at}
                className={cn("flex-1 rounded-t-bar", m.pageviews > 0 ? "bg-series-1" : "bg-line")}
                // the height is data; react writes it through the cssom
                style={{ height: `${Math.max(6, (m.pageviews / max) * 100)}%` }}
              />
            ))}
          </div>
          <figcaption className="text-caption text-fg-3">
            Page views per minute, last 30 minutes
          </figcaption>
        </figure>
      ) : null}
    </section>
  );
}

// ---- totals, the chart and the breakdowns

function Dashboard({
  tenantId,
  state,
  set,
  q,
  sites,
  utm,
}: {
  tenantId: string;
  state: WebState;
  set: (s: Partial<WebState>) => void;
  q: WebQuery;
  sites: Site[];
  utm: boolean;
}) {
  const summary = useQuery(webSummaryQuery(tenantId, q));
  const single = isSingleDay(q);
  if (summary.isPending) return <Loading label="Loading web analytics" />;
  if (summary.error) return <ErrorPanel error={summary.error} onRetry={() => summary.refetch()} />;
  const s = summary.data;
  const cur = s.current.totals;
  const prev = s.previous.totals;
  const hasData = cur.pageviews > 0;
  // a single day is drawn per hour, which only page views and visitors have
  const metric: WebMetric =
    single && state.metric !== "pageviews" && state.metric !== "visitors"
      ? "pageviews"
      : state.metric;

  // keepPreviousData shows the last range's figures while the next loads;
  // they are dimmed and marked busy so they do not read as the new range
  const stale = summary.isPlaceholderData;

  return (
    <div
      className={cn(
        "flex flex-col gap-5 transition-opacity duration-(--vs-duration-press)",
        stale && "opacity-60",
      )}
      aria-busy={stale || undefined}
      data-testid="web-dashboard"
    >
      <FreshnessNote summary={s} band={state.band} onBand={(band) => set({ band })} />
      <motion.section
        aria-label="Totals"
        variants={stagger}
        initial="hidden"
        animate="show"
        className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5"
      >
        {METRIC_TILES.map((t) => {
          const v = metricValue(cur, t.metric);
          const chartable = !single || t.metric === "pageviews" || t.metric === "visitors";
          return (
            <StaggerItem key={t.metric} className="flex">
              <StatTile
                className="w-full"
                label={t.label}
                hint={t.hint}
                value={formatMetric(t.metric, v)}
                numeric={v ?? undefined}
                format={(n) => formatMetric(t.metric, n)}
                state={v === null || !hasData ? "empty" : "data"}
                emptyText="No page views in this range"
                trend={webTrend(t.metric, v, metricValue(prev, t.metric), s.current.range.days)}
                pressed={chartable ? metric === t.metric : undefined}
                onPress={chartable ? () => set({ metric: t.metric }) : undefined}
              />
            </StaggerItem>
          );
        })}
      </motion.section>

      {single ? (
        <HourChart tenantId={tenantId} q={q} metric={metric} />
      ) : (
        <DayChart summary={s} metric={metric} />
      )}

      <Breakdowns tenantId={tenantId} q={q} visitors={cur.visitors} sites={sites} utm={utm} />
    </div>
  );
}

function FreshnessNote({
  summary,
  band,
  onBand,
}: {
  summary: WebSummary;
  band: "primary" | "quarantine";
  onBand: (b: "primary" | "quarantine") => void;
}) {
  const share = summary.quarantine_share;
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-caption text-fg-3">
      <span>
        {summary.read_at
          ? `Totals updated ${formatDateTimeUtc(summary.read_at)}; they are read every hour.`
          : "No totals for this range yet; they are read every hour."}
      </span>
      {share !== null && share > 0 ? (
        <button
          type="button"
          className="rounded-sm text-fg-2 underline-offset-2 hover:text-fg hover:underline focus-ring"
          onClick={() => onBand(band === "primary" ? "quarantine" : "primary")}
        >
          {formatShare(share)} of page views were quarantined as likely automated.{" "}
          {band === "primary" ? "Show them" : "Show counted traffic"}
        </button>
      ) : null}
    </div>
  );
}

function DayChart({ summary, metric }: { summary: WebSummary; metric: WebMetric }) {
  const today = new Date().toISOString().slice(0, 10);
  const buckets: ChartBucket[] = summary.current.series.map((p) => {
    const d = new Date(`${p.day}T00:00:00Z`);
    return {
      at: d.toISOString(),
      tick: tickDay.format(d),
      label: `${labelDay.format(d)}, UTC`,
      // today is still counting, and a day not read yet is not a zero
      partial: p.day === today || !p.aggregated,
    };
  });
  const label = METRIC_TILES.find((t) => t.metric === metric)?.label ?? metric;
  const values = summary.current.series.map((p) => pointValue(p, metric) ?? 0);
  const previous = summary.previous.series.map((p) => pointValue(p, metric) ?? 0);
  const chartState = summary.current.totals.pageviews === 0 ? "empty" : "data";
  return (
    <TimeChart
      // a remount from empty to data gives the reveal observer the plot
      key={chartState}
      title={`${label} per day`}
      buckets={buckets}
      series={[{ key: metric, label, tone: "series-1", values }]}
      previous={previous}
      previousLabel="Previous period"
      format={(n) => formatMetric(metric, n)}
      higherIsBetter={higherIsBetter(metric)}
      state={chartState}
      emptyTitle="No page views in this range"
      emptyText="Numbers appear here within an hour of the first page view."
    />
  );
}

function HourChart({ tenantId, q, metric }: { tenantId: string; q: WebQuery; metric: WebMetric }) {
  const h = useQuery(webHourlyQuery(tenantId, q.from, q.site, q.band));
  const buckets: ChartBucket[] = useMemo(
    () =>
      (h.data?.points ?? []).map((p) => {
        const d = new Date(p.at);
        return {
          at: p.at,
          tick: tickHour.format(d),
          label: `${tickDay.format(d)}, ${tickHour.format(d)} UTC`,
          partial: d.getTime() + 3_600_000 > Date.now(),
        };
      }),
    [h.data],
  );
  const pv = metric === "pageviews";
  const values = (h.data?.points ?? []).map((p) => (pv ? p.pageviews : p.visitors));
  const total = (h.data?.points ?? []).reduce((a, p) => a + p.pageviews, 0);
  const state = h.isPending ? "loading" : h.error ? "error" : total === 0 ? "empty" : "data";
  return (
    <TimeChart
      // the plot reveals itself once it is in view; a remount on a new state
      // gives the observer the plot element, which loading did not render
      key={state}
      title={`${pv ? "Page views" : "Visitors"} per hour`}
      description="Read live from the analytics engine; the totals above follow within the hour."
      buckets={buckets}
      series={[{ key: metric, label: pv ? "Page views" : "Visitors", tone: "series-1", values }]}
      state={state}
      error={h.error}
      onRetry={() => h.refetch()}
      emptyTitle="No page views on this day"
      emptyText="Page views appear here a few minutes after they happen."
    />
  );
}

// ---- breakdowns

const LIMIT = 10;
const MORE = 100;

type Group = { title: string; options: WebDimension[] };

function Breakdowns({
  tenantId,
  q,
  visitors,
  sites,
  utm,
}: {
  tenantId: string;
  q: WebQuery;
  visitors: number;
  sites: Site[];
  utm: boolean;
}) {
  const groups: Group[] = [
    { title: "Pages", options: ["page", "entry_page", "exit_page"] },
    { title: "Sources", options: ["referrer", ...(utm ? UTM_DIMENSIONS : [])] },
    { title: "Countries", options: ["country"] },
    { title: "Environment", options: ["browser", "os", "device"] },
    { title: "Custom events", options: ["event"] },
  ];
  const names = Object.fromEntries(sites.map((s) => [s.id, s.name]));
  return (
    <motion.div
      variants={stagger}
      initial="hidden"
      animate="show"
      className="grid gap-4 lg:grid-cols-2"
    >
      {groups.map((g) => (
        <StaggerItem key={g.title}>
          <BreakdownPanel
            tenantId={tenantId}
            q={q}
            group={g}
            visitors={visitors}
            siteNames={q.site ? null : names}
          />
        </StaggerItem>
      ))}
    </motion.div>
  );
}

const SHORT: Partial<Record<WebDimension, string>> = {
  page: "Pages",
  entry_page: "Entry",
  exit_page: "Exit",
  referrer: "Referrers",
  utm_source: "Source",
  utm_medium: "Medium",
  utm_campaign: "Campaign",
  utm_content: "Content",
  utm_term: "Term",
  browser: "Browsers",
  os: "OS",
  device: "Devices",
};

function BreakdownPanel({
  tenantId,
  q,
  group,
  visitors,
  siteNames,
}: {
  tenantId: string;
  q: WebQuery;
  group: Group;
  visitors: number;
  siteNames: Record<string, string> | null;
}) {
  const first = group.options[0] ?? "page";
  const [dim, setDim] = useState<WebDimension>(first);
  const [more, setMore] = useState(false);
  // a utm choice that went away with the site selection falls back
  const active = group.options.includes(dim) ? dim : first;
  const b = useQuery(webBreakdownQuery(tenantId, q, active, more ? MORE : LIMIT));
  return (
    <Panel
      title={group.title}
      className="h-full"
      actions={
        group.options.length > 1 ? (
          <Segmented<WebDimension>
            label={`${group.title} by`}
            value={active}
            options={group.options.map((o) => ({
              value: o,
              label: SHORT[o] ?? DIMENSION_LABEL[o],
            }))}
            onChange={(v) => {
              setDim(v);
              setMore(false);
            }}
          />
        ) : null
      }
    >
      {b.isPending ? (
        <div className="flex flex-col gap-2" aria-busy>
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-7 w-full" />
          ))}
        </div>
      ) : b.error ? (
        <ErrorPanel error={b.error} onRetry={() => b.refetch()} />
      ) : (
        <div
          className={cn(
            "transition-opacity duration-(--vs-duration-press)",
            b.isPlaceholderData && "opacity-60",
          )}
          aria-busy={b.isPlaceholderData || undefined}
        >
          <BreakdownList
            data={b.data}
            visitors={visitors}
            siteNames={siteNames}
            onMore={b.data.total_keys > b.data.items.length && !more ? () => setMore(true) : null}
          />
        </div>
      )}
    </Panel>
  );
}

function BreakdownList({
  data,
  visitors,
  siteNames,
  onMore,
}: {
  data: WebBreakdown;
  visitors: number;
  siteNames: Record<string, string> | null;
  onMore: (() => void) | null;
}) {
  if (data.items.length === 0) {
    return (
      <p className="py-6 text-center text-small text-fg-2">
        {data.dimension === "event"
          ? "No custom events in this range."
          : `No ${DIMENSION_LABEL[data.dimension].toLowerCase()} in this range.`}
      </p>
    );
  }
  const top = data.items[0]?.value ?? 1;
  const unit = data.unit === "events" ? "Events" : "Visitors";
  return (
    <div className="flex flex-col gap-2">
      <table className="w-full table-fixed text-small">
        <caption className="sr-only">
          {DIMENSION_LABEL[data.dimension]} by {unit.toLowerCase()}
        </caption>
        <thead>
          <tr className="text-caption text-fg-3">
            <th scope="col" className="pb-1 text-left font-normal">
              {DIMENSION_LABEL[data.dimension]}
            </th>
            <th scope="col" className="w-20 pb-1 text-right font-normal">
              {unit}
            </th>
            {data.unit === "visitors" ? (
              <th scope="col" className="w-16 pb-1 text-right font-normal">
                Share
              </th>
            ) : null}
          </tr>
        </thead>
        <motion.tbody variants={stagger} initial="hidden" animate="show">
          {data.items.map((it) => (
            <motion.tr key={`${it.site_id ?? ""} ${it.key}`} variants={fade} className="group">
              <td className="relative py-1 pr-2">
                <span
                  aria-hidden
                  className="absolute inset-y-0.5 left-0 rounded-bar bg-series-1/15 transition-colors group-hover:bg-series-1/25"
                  // the width is data; react writes it through the cssom
                  style={{ width: `${(it.value / top) * 100}%` }}
                />
                <span className="relative flex min-w-0 items-baseline gap-2 px-1">
                  <span className="truncate text-fg" title={it.key}>
                    {keyLabel(data.dimension, it.key)}
                  </span>
                  {siteNames && it.site_id ? (
                    <span className="shrink-0 truncate text-caption text-fg-3">
                      {siteNames[it.site_id] ?? ""}
                    </span>
                  ) : null}
                </span>
              </td>
              <td className="py-1 text-right tabular-nums text-fg">{formatCount(it.value)}</td>
              {data.unit === "visitors" ? (
                <td className="py-1 text-right tabular-nums text-fg-2">
                  {formatShare(shareOf(it.value, visitors))}
                </td>
              ) : null}
            </motion.tr>
          ))}
        </motion.tbody>
      </table>
      <div className="flex flex-wrap items-center justify-between gap-2 text-caption text-fg-3">
        <span>
          {formatCount(data.items.length)} of {formatCount(data.total_keys)}
          {data.source === "engine" ? " · read live" : " · daily totals"}
        </span>
        {onMore ? (
          <Button variant="ghost" size="sm" onClick={onMore}>
            Show up to {MORE}
            <ChevronDown aria-hidden />
          </Button>
        ) : null}
      </div>
    </div>
  );
}
