// the project's web numbers: the seven web metrics for a range of utc days
// against the previous period, one chart, the breakdowns, the visitors right
// now and what the totals leave out, for the whole project or one site.
// totals, days and the referrer, country and environment breakdowns come
// from the daily totals operators read too; pages, events, utm values, hours
// and the last minutes are read live.

import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Globe } from "lucide-react";
import { type ReactNode, useCallback, useState, useSyncExternalStore } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { sitesQuery } from "@/api/queries";
import type { Site, TenantStatus } from "@/api/schemas";
import {
  type WebDimension,
  type WebQuery,
  type WebSummary,
  webBreakdownQuery,
  webKeys,
  webRealtimeQuery,
  webSummaryQuery,
} from "@/api/web";
import { BreakdownList } from "@/components/charts";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { Notice } from "@/components/notice";
import { Panel } from "@/components/panel";
import { PageHeader } from "@/components/page-header";
import { RangeControl } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { Sparkline, StatCard, StatGrid } from "@/components/stat-card";
import { EmptyState, ErrorState, Missing } from "@/components/states";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { formatCount, formatPercent, formatTimeUtc } from "@/lib/format";
import { rejectionRows, sumRejected, sumUnclassified } from "@/lib/outcomes";
import {
  DIMENSION_LABEL,
  formatMetric,
  ingestWindow,
  isSingleDay,
  keyLabel,
  metricValue,
  readWebState,
  sparkOf,
  sumOutcomes,
  UTM_DIMENSIONS,
  validateWebDays,
  WEB_MAX_DAYS,
  WEB_PRESETS,
  type WebMetric,
  type WebState,
  webDays,
  webQuery,
  webTrend,
  writeWebState,
} from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { Movers } from "./movers";
import { PAGE_HEADERS, WebSkeleton } from "./page-frame";
import { SelectFilter } from "./select-filter";
import { METRIC_LABEL, WebTrendChart } from "./web-chart";

const METRICS: { metric: WebMetric; definition: string }[] = [
  { metric: "pageviews", definition: "Pages loaded in a browser." },
  {
    metric: "visitors",
    definition: "Distinct visitors per UTC day, added up over the range.",
  },
  { metric: "visits", definition: "A visit ends after 30 minutes without activity." },
  { metric: "bounce_rate", definition: "Visits with one page view and no custom event." },
  {
    metric: "avg_visit_duration",
    definition: "Average time from the first to the last page view of a visit.",
  },
];

// why a range has no visits, by project status
const EMPTY_REASON: Partial<Record<TenantStatus, string>> = {
  active:
    "No site sent a page view in this range. Choose a longer range, or check that the tracker is on your pages.",
  pending: "Sites receive traffic after an operator approves the project.",
  rejected: "Sites receive traffic after an operator approves the project.",
  suspended: "New events are refused while the project is suspended.",
};

const CADENCE = "Totals refresh every hour. Pages, events and hours are read live.";

export function WebPage() {
  const { tenant } = useProject();
  const [params, setParams] = useSearchParams();
  const state = readWebState(params);
  const set = (next: Partial<WebState>) =>
    setParams(writeWebState({ ...state, ...next }), { replace: true });
  const sites = useQuery(sitesQuery(tenant.id));
  const provisioning = useProvisioningPending(tenant.id);
  const now = new Date();

  const listed = sites.data?.filter((s) => s.status !== "removed") ?? [];
  const selected = state.site ? listed.find((s) => s.id === state.site) : undefined;
  // a site in the address that is deleted or not one of this project's: the
  // numbers are those of all sites, and a notice says so
  const unknownSite = sites.data !== undefined && state.site !== null && !selected;
  const q = webQuery(unknownSite ? { ...state, site: null } : state, now);
  const summary = useQuery({
    ...webSummaryQuery(tenant.id, q),
    enabled: listed.length > 0,
  });

  const header = (
    <PageHeader
      {...PAGE_HEADERS.web}
      freshness={summary.data?.read_at ? { at: summary.data.read_at, cadence: CADENCE } : null}
    />
  );

  if (sites.isPending)
    return (
      <Page header={header}>
        <WebSkeleton />
      </Page>
    );
  if (sites.error)
    return (
      <Page header={header}>
        <ErrorState
          title="The sites did not load"
          error={sites.error}
          onRetry={() => sites.refetch()}
        />
      </Page>
    );
  if (listed.length === 0)
    return (
      <Page header={header}>
        <EmptyState
          icon={<Globe aria-hidden strokeWidth={1.75} />}
          title="No sites yet"
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}/sites`}>Add site</Link>
            </Button>
          }
        >
          The numbers show here after a site sends its first page view.
        </EmptyState>
      </Page>
    );

  const scope = selected ? [selected] : listed;
  // a range without one counted page view gets one page-level
  // empty state that says why, not a row of dashes and empty panels. the
  // not counted section stays: it says why events did not count
  const totals = summary.data?.current.totals;
  const empty =
    state.band === "primary" &&
    totals !== undefined &&
    totals.pageviews === 0 &&
    !summary.isPlaceholderData;
  const utm = scope.some((s) => s.collect_utm);
  const days = webDays(state, now);
  // one control filters by site, and choosing All sites is the one way to
  // clear it: no chip and no second Clear, so the row keeps its height.
  // the options show the site name; the domain joins only where two sites
  // share a name
  const names = new Map<string, number>();
  for (const x of listed) names.set(x.name, (names.get(x.name) ?? 0) + 1);
  const siteFilter = (
    <SelectFilter
      label="Site"
      value={selected?.id ?? ""}
      onChange={(v) => set({ site: v || null })}
    >
      <option value="">All sites</option>
      {listed.map((x) => (
        <option key={x.id} value={x.id} title={x.domain}>
          {(names.get(x.name) ?? 0) > 1 ? `${x.name} (${x.domain})` : x.name}
        </option>
      ))}
    </SelectFilter>
  );

  return (
    <Page header={header}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
          <RangeControl
            value={state.preset}
            presets={WEB_PRESETS}
            onPreset={(preset) => set({ preset })}
            custom={state.preset === "custom" ? state.custom : null}
            onCustom={(custom) => set({ preset: "custom", custom })}
            range={days}
            maxDays={WEB_MAX_DAYS}
            validate={(r) => validateWebDays(r, new Date())}
          />
          {listed.length > 1 ? siteFilter : null}
        </div>

        {provisioning ? (
          <Notice tone="info" title="Live figures are being set up">
            Pages, events, hours and visitors right now show when your access is ready. The daily
            totals show now.
          </Notice>
        ) : null}
        {unknownSite ? (
          <Notice
            tone="warning"
            title="That site is not part of this project"
            actions={
              <Button variant="outline" size="sm" onClick={() => set({ site: null })}>
                Show all sites
              </Button>
            }
          >
            The address names a deleted site or a site of another project. The numbers show all
            sites.
          </Notice>
        ) : null}
        {state.band === "quarantine" ? (
          <Notice
            tone="info"
            title="You see quarantined traffic"
            actions={
              <Button variant="outline" size="sm" onClick={() => set({ band: "primary" })}>
                Show counted traffic
              </Button>
            }
          >
            The totals leave this traffic out. It is likely automated.
          </Notice>
        ) : null}
      </div>

      {state.band === "primary" && !(empty && tenant.status !== "active") ? (
        <Realtime tenantId={tenant.id} site={q.site} />
      ) : null}

      {summary.isPending ? (
        <WebSkeleton />
      ) : summary.error ? (
        <ErrorState
          title="The web numbers did not load"
          error={summary.error}
          onRetry={() => summary.refetch()}
        />
      ) : empty ? (
        <EmptyState
          icon={<Globe aria-hidden strokeWidth={1.75} />}
          title="No visits in this range"
          action={
            tenant.status === "active" ? (
              <Button asChild variant="outline">
                <Link to={`/projects/${tenant.id}/sites`}>Check the site setup</Link>
              </Button>
            ) : undefined
          }
        >
          {EMPTY_REASON[tenant.status] ?? EMPTY_REASON.active}
        </EmptyState>
      ) : (
        <Dashboard
          tenantId={tenant.id}
          state={state}
          set={set}
          q={q}
          summary={summary.data}
          stale={summary.isPlaceholderData}
          sites={listed}
          utm={utm}
        />
      )}

      <NotCounted
        sites={scope}
        q={q}
        summary={summary.data ?? null}
        band={state.band}
        onBand={(band) => set({ band })}
      />

      <DbIpAttribution />
    </Page>
  );
}

function Page({ header, children }: { header: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-8">
      {header}
      {children}
    </div>
  );
}
// whether a live read of the project waits for its analytics access (409
// provisioning_pending). the query client keeps asking; this only says why
// a section still loads.
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

// ---- right now: the last 5 minutes, asked for every 30 seconds

function Realtime({ tenantId, site }: { tenantId: string; site: string | null }) {
  const rt = useQuery(webRealtimeQuery(tenantId, site));
  const minutes = rt.data?.minutes ?? [];
  const max = Math.max(1, ...minutes.map((m) => m.pageviews));
  return (
    <section
      aria-label="Right now"
      className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-lg border border-line bg-raised px-4 py-3 sm:px-5"
    >
      <div className="flex min-h-8 items-center gap-3">
        <StatusDot
          tone={rt.data && rt.data.visitors > 0 ? "success" : "neutral"}
          live={!!rt.data}
        />
        {rt.isPending ? (
          <Bone className="h-6 w-48" />
        ) : rt.error ? (
          <span className="text-small text-fg-2">Visitors right now did not load.</span>
        ) : (
          <span className="text-body text-fg-2">
            <span className="text-title text-fg tabular" data-testid="web-active">
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
            aria-label={`Page views per minute, last 30 minutes: ${minutes.map((m) => m.pageviews).join(", ")}`}
            className="flex h-8 items-end gap-px"
          >
            {minutes.map((m) => (
              <span
                key={m.at}
                className={cn("flex-1 rounded-t-bar", m.pageviews > 0 ? "bg-series-1" : "bg-line")}
                // the height is data; react writes it through the cssom
                style={{ height: `${Math.max(6, (m.pageviews / max) * 100)}%` }}
              />
            ))}
          </div>
          <figcaption className="text-caption text-fg-2">
            Page views per minute, last 30 minutes
          </figcaption>
        </figure>
      ) : null}
      {rt.data ? (
        <time dateTime={rt.data.at} className="text-caption text-fg-2">
          Checked {formatTimeUtc(rt.data.at, { seconds: true })}, every 30 s
        </time>
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
  summary,
  stale,
  sites,
  utm,
}: {
  tenantId: string;
  state: WebState;
  set: (s: Partial<WebState>) => void;
  q: WebQuery;
  summary: WebSummary;
  stale: boolean;
  sites: Site[];
  utm: boolean;
}) {
  const single = isSingleDay(q);
  const cur = summary.current;
  const prev = summary.previous;
  // a single day is drawn per hour, which only page views and visitors have
  const chartable = (m: WebMetric) => !single || m === "pageviews" || m === "visitors";
  const metric: WebMetric = chartable(state.metric) ? state.metric : "pageviews";

  return (
    <div
      // the last range's numbers stay while the next loads, dimmed and busy
      className={cn(
        "flex flex-col gap-8 transition-opacity duration-(--vs-duration-change) ease-standard",
        stale && "opacity-60",
      )}
      aria-busy={stale || undefined}
      data-testid="web-dashboard"
    >
      <Movers
        items={METRICS.map((t) => ({
          key: t.metric,
          label: METRIC_LABEL[t.metric],
          change: webTrend(
            t.metric,
            metricValue(cur.totals, t.metric),
            metricValue(prev.totals, t.metric),
            cur.range.days,
          ),
        }))}
      />
      <StatGrid aria-label="Totals">
        {METRICS.map((t) => {
          const v = metricValue(cur.totals, t.metric);
          const spark = sparkOf(cur.series, t.metric);
          return (
            <StatCard
              key={t.metric}
              data-metric={t.metric}
              label={METRIC_LABEL[t.metric]}
              definition={t.definition}
              value={v}
              format={(n) => formatMetric(t.metric, n)}
              missingReason="No visits in this range"
              change={webTrend(t.metric, v, metricValue(prev.totals, t.metric), cur.range.days)}
              spark={
                spark ? (
                  <Sparkline values={spark} previous={prev.series.map((p) => p[t.metric] ?? 0)} />
                ) : null
              }
              selected={chartable(t.metric) ? metric === t.metric : undefined}
              onSelect={chartable(t.metric) ? () => set({ metric: t.metric }) : undefined}
              // a rate or an average with no visit behind it is no card
              hideWhenEmpty
            />
          );
        })}
      </StatGrid>

      <WebTrendChart tenantId={tenantId} q={q} summary={summary} metric={metric} />

      <Breakdowns
        tenantId={tenantId}
        q={q}
        visitors={cur.totals.visitors}
        sites={sites}
        utm={utm}
      />
    </div>
  );
}

// ---- breakdowns

type Group = { title: string; options: WebDimension[]; wide?: boolean };

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
    // events count events, not visitors, so they take their own full row
    { title: "Custom events", options: ["event"], wide: true },
  ];
  const names = q.site ? null : Object.fromEntries(sites.map((s) => [s.id, s.name]));
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {groups.map((g) => (
        <BreakdownPanel
          key={g.title}
          tenantId={tenantId}
          q={q}
          group={g}
          visitors={visitors}
          siteNames={names}
        />
      ))}
    </div>
  );
}

const LIMIT = 10;
const MORE = 100;

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
  const [limit, setLimit] = useState(LIMIT);
  // a utm choice that went away with the site selection falls back
  const active = group.options.includes(dim) ? dim : first;
  const b = useQuery(webBreakdownQuery(tenantId, q, active, limit));
  const data = b.data;
  const events = data?.unit === "events";
  const headingId = `breakdown-${group.title.toLowerCase().replace(/\s+/g, "-")}`;
  return (
    <section
      aria-labelledby={headingId}
      className={cn(
        "flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5",
        group.wide && "lg:col-span-2",
      )}
    >
      {/* the row is as tall as the segmented control, so titles of panels
          side by side sit on one line with or without it */}
      <div className="flex min-h-(--vs-control-touch) flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <h2 id={headingId} className="text-title text-fg">
          {group.title}
        </h2>
        {group.options.length > 1 ? (
          <Segmented<WebDimension>
            label={`${group.title} by`}
            value={active}
            options={group.options.map((o) => ({
              value: o,
              label: SHORT[o] ?? DIMENSION_LABEL[o],
            }))}
            onChange={(v) => {
              setDim(v);
              setLimit(LIMIT);
            }}
            className="max-w-full overflow-x-auto"
          />
        ) : null}
      </div>
      <div
        className={cn(
          "transition-opacity duration-(--vs-duration-change) ease-standard",
          b.isPlaceholderData && "opacity-60",
        )}
        aria-busy={b.isPlaceholderData || undefined}
      >
        <BreakdownList
          label={DIMENSION_LABEL[active]}
          state={b.isPending ? "loading" : b.error ? "error" : undefined}
          error={b.error}
          onRetry={() => b.refetch()}
          items={(data?.items ?? []).map((it) => {
            const text = keyLabel(active, it.key);
            const site = siteNames && it.site_id ? siteNames[it.site_id] : undefined;
            return {
              key: `${it.site_id ?? ""}|${it.key}`,
              text: site ? `${text}, ${site}` : text,
              label: (
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate">{text}</span>
                  {site ? (
                    <span className="shrink-0 truncate text-caption text-fg-2">{site}</span>
                  ) : null}
                </span>
              ),
              value: it.value,
            };
          })}
          total={events ? null : visitors}
          shareDefinition="The share of the range's visitors. A visitor can count in several rows, so the shares can add up to more than 100%."
          valueLabel={events ? "Events" : "Visitors"}
          emptyTitle={
            active === "event"
              ? "No custom events in this range"
              : `No ${DIMENSION_LABEL[active].toLowerCase()} in this range`
          }
          onShowMore={
            data && data.total_keys > data.items.length && limit < MORE
              ? () => setLimit(MORE)
              : undefined
          }
          showMoreLabel={
            (data?.total_keys ?? 0) <= MORE
              ? `Show all ${formatCount(data?.total_keys ?? 0)}`
              : `Show the top ${MORE}`
          }
          showMorePending={b.isFetching && b.isPlaceholderData}
        />
      </div>
    </section>
  );
}

// ---- not counted: what the totals leave out, and why

const OUTCOMES: { key: string; label: string; definition: string }[] = [
  { key: "accepted", label: "Counted", definition: "Events the totals include." },
  {
    key: "quarantined",
    label: "Quarantined",
    definition: "Many visitors from one network, or very many events from one visitor.",
  },
  { key: "bot", label: "Bots", definition: "The browser is a known bot." },
  { key: "rejected", label: "Rejected", definition: "Refused at the edge. The reasons are below." },
  { key: "dlq", label: "Not stored", definition: "Accepted, but the write to storage failed." },
];

// a term with its definition in a tooltip: dotted underline, focusable
const DEFINED =
  "rounded-sm underline decoration-line-strong decoration-dotted underline-offset-4 focus-ring";

const OTHER_OUTCOME = {
  key: "other",
  label: "Other",
  definition: "An outcome that this page does not name yet.",
};

function NotCounted({
  sites,
  q,
  summary,
  band,
  onBand,
}: {
  sites: Site[];
  q: WebQuery;
  summary: WebSummary | null;
  band: "primary" | "quarantine";
  onBand: (b: "primary" | "quarantine") => void;
}) {
  const span = ingestWindow(q, new Date());
  const answers = useQueries({
    queries: sites.map((s) => ({
      queryKey: ["ingest", s.id, span?.from ?? "", span?.to ?? ""] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        span
          ? api.ingest(s.id, span, signal)
          : Promise.reject(new Error("the range has not started")),
      enabled: span !== null,
      // rows are folded every few minutes; asking more often shows nothing new
      staleTime: 2 * 60_000,
    })),
  });
  const pending = answers.some((a) => a.isPending);
  const failed = answers.find((a) => a.error);
  const totals = sumOutcomes(answers.map((a) => a.data));
  const other = sumUnclassified(totals);
  const value = (k: string) =>
    k === "rejected" ? sumRejected(totals) : k === "other" ? other : (totals[k] ?? 0);
  const reasons = rejectionRows(totals);
  const share = summary?.quarantine_share ?? null;
  const quarantined = summary
    ? band === "primary"
      ? summary.other_band.pageviews
      : summary.current.totals.pageviews
    : null;
  // an outcome nothing reached is left out; a range where nothing reached
  // any outcome draws neither list
  const rows = (other > 0 ? [...OUTCOMES, OTHER_OUTCOME] : OUTCOMES).filter(
    (o) => pending || value(o.key) > 0,
  );

  // the reasons stand beside the outcomes only when there are any; alone,
  // the outcomes take the width
  const whyShown = span !== null && !failed && (pending || reasons.length > 0);

  return (
    <section aria-labelledby="not-counted" className="flex flex-col gap-4">
      <h2 id="not-counted" className="text-section text-fg">
        Not counted
      </h2>

      <div className={cn("grid items-start gap-4", whyShown && "lg:grid-cols-2")}>
        {/* one panel: the quarantined page views, then every event by outcome */}
        <Panel
          title="Every event, by outcome"
          actions={
            <Button
              variant="outline"
              size="sm"
              className="max-lg:h-(--vs-control-touch)"
              onClick={() => onBand(band === "primary" ? "quarantine" : "primary")}
            >
              {band === "primary" ? "Show quarantined traffic" : "Show counted traffic"}
            </Button>
          }
          bodyClassName="flex flex-col"
        >
          <p
            className="border-b border-line-subtle pb-3 text-body text-fg-2"
            data-testid="quarantined-pageviews"
          >
            {quarantined === null ? (
              <Missing reason="The totals did not load" />
            ) : (
              <span className="text-fg tabular">{formatCount(quarantined)}</span>
            )}{" "}
            quarantined page views
            {share !== null && share > 0 ? `, ${formatPercent(share)} of all page views` : ""}
          </p>
          {span === null ? (
            <EmptyState compact title="The range has not started">
              Outcome counts show from the first day of the range. Choose an earlier range.
            </EmptyState>
          ) : failed ? (
            <ErrorState
              compact
              title="The outcome counts did not load"
              error={failed.error}
              onRetry={() => {
                for (const a of answers) if (a.error) a.refetch();
              }}
            />
          ) : rows.length === 0 ? null : (
            <dl aria-label="Outcomes" className="flex flex-col">
              {rows.map((o) => (
                <div
                  key={o.key}
                  data-outcome={o.key}
                  className="flex h-(--vs-control-sm) items-center justify-between gap-3 border-b border-line-subtle last:border-b-0 max-lg:h-(--vs-control-touch)"
                >
                  <dt className="text-body text-fg-2">
                    <Tooltip content={o.definition}>
                      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
                      <span tabIndex={0} className={DEFINED}>
                        {o.label}
                      </span>
                    </Tooltip>
                    <span className="sr-only">. {o.definition}</span>
                  </dt>
                  <dd className="text-small text-fg tabular">
                    {pending ? <Bone className="h-3 w-12" /> : formatCount(value(o.key))}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </Panel>
        {!whyShown ? null : (
          <Panel title="Why events were rejected">
            <BreakdownList
              label="Reasons"
              state={pending ? "loading" : undefined}
              items={reasons.map((r) => ({
                key: r.key,
                label: r.label,
                text: r.label,
                value: r.count,
              }))}
              valueLabel="Events"
              emptyTitle="No rejected events in this range"
            />
          </Panel>
        )}
      </div>
    </section>
  );
}
