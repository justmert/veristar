// the project's web numbers: the five web figures for a range of utc days
// against the previous period, one chart, the breakdowns, the custom events,
// the visitors right now and where every event went, for the whole project
// or one site. counted or quarantined traffic is a page-wide switch in the
// header, beside the range: it decides what every part of the page reads.
// totals, days and the referrer, country and environment breakdowns come
// from the daily totals operators read too; pages, events, utm values, hours
// and the last minutes are read live.

import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { type ReactNode, useCallback, useState, useSyncExternalStore } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { sitesQuery } from "@/api/queries";
import type { Site, TenantStatus } from "@/api/schemas";
import {
  type WebBand,
  type WebDimension,
  type WebQuery,
  type WebSummary,
  webBreakdownQuery,
  webKeys,
  webSummaryQuery,
} from "@/api/web";
import { BreakdownList, TimeChart } from "@/components/charts";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Panel } from "@/components/panel";
import { RangeControl } from "@/components/range-control";
import { Segmented, SegmentedDot } from "@/components/segmented";
import { ChangeLine, StatCard, type StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { ZoomBlock, type ZoomGroup, type ZoomPart } from "@/components/zoom-block";
import { formatCount, formatDateRange } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { rejectionRows, sumUnclassified } from "@/lib/outcomes";
import { siteSetupPath } from "@/lib/site-setup";
import {
  DIMENSION_LABEL,
  formatMetric,
  ingestWindow,
  isSingleDay,
  keyLabel,
  metricValue,
  readWebState,
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
import { PAGE_HEADERS, WebSkeleton } from "./page-frame";
import { LiveCount, SiteChips, useSiteHealth } from "./site-chips";
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

// why a range has no visits, by project status: the title of the first step
const EMPTY_TITLE: Partial<Record<TenantStatus, string>> = {
  active: "Waiting for the first page view",
  pending: "Sites receive traffic after an operator approves the project",
  rejected: "Sites receive traffic after an operator approves the project",
  suspended: "New events are refused while the project is suspended",
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
  const days = webDays(state, now);

  const header = (
    <PageHeader
      {...PAGE_HEADERS.web}
      freshness={summary.data?.read_at ? { at: summary.data.read_at, cadence: CADENCE } : null}
      items={days.from && days.to ? [`${formatDateRange(days.from, days.to)}, UTC`] : []}
      secondary={
        listed.length > 0 ? (
          <>
            <Segmented<WebBand>
              label="Traffic"
              value={state.band}
              options={[
                { value: "primary", label: "Counted" },
                {
                  value: "quarantine",
                  label: "Quarantined",
                  marker: <SegmentedDot tone="warning" />,
                },
              ]}
              onChange={(band) => set({ band })}
            />
            <RangeControl
              value={state.preset}
              presets={WEB_PRESETS}
              onPreset={(preset) => set({ preset })}
              custom={state.preset === "custom" ? state.custom : null}
              onCustom={(custom) => set({ preset: "custom", custom })}
              range={days}
              maxDays={WEB_MAX_DAYS}
              validate={(r) => validateWebDays(r, new Date())}
              words={false}
            />
          </>
        ) : null
      }
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
          title="No sites yet"
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}/sites`}>Add site</Link>
            </Button>
          }
        />
      </Page>
    );

  const scope = selected ? [selected] : listed;
  // a range without one counted page view is the first step of the page,
  // not a row of dashes and empty panels
  const totals = summary.data?.current.totals;
  const empty =
    state.band === "primary" &&
    totals !== undefined &&
    totals.pageviews === 0 &&
    !summary.isPlaceholderData;
  const utm = scope.some((s) => s.collect_utm);

  return (
    <Page header={header}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <SiteChips
          tenantId={tenant.id}
          sites={listed}
          value={selected?.id ?? null}
          onChange={(site) => set({ site })}
        />
        {state.band === "primary" && !(empty && tenant.status !== "active") ? (
          <LiveCount tenantId={tenant.id} site={q.site} minutes />
        ) : null}
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
          The address names a deleted site or a site of another project. The numbers show all sites.
        </Notice>
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
        <FirstStep sites={scope} status={tenant.status} />
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

      {empty ? null : <WhereEventsWent sites={scope} q={q} />}
    </Page>
  );
}

function Page({ header, children }: { header: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
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

// ---- no page view yet: the figures wait, and the chart's place holds the
// one step that brings the first page view

function FirstStep({ sites, status }: { sites: Site[]; status: TenantStatus }) {
  const health = useSiteHealth(sites);
  const docs = siblingUrl(window.location.hostname, "docs");
  // the first site whose tracker sent nothing is the one to set up
  const next = sites.find((s) => health.get(s.id)?.tone !== "success") ?? sites[0];
  const active = status === "active";
  return (
    <div className="flex flex-col gap-4" data-testid="web-first-step">
      <FigureRow>
        {METRICS.map((t) => (
          <StatCard
            key={t.metric}
            data-metric={t.metric}
            label={METRIC_LABEL[t.metric]}
            definition={t.definition}
            value={null}
            missingReason="No page view yet"
          />
        ))}
      </FigureRow>
      <TimeChart
        title="Page views"
        description="per day, UTC"
        buckets={[]}
        series={[]}
        state="empty"
        height={300}
        emptyTitle={EMPTY_TITLE[status] ?? (EMPTY_TITLE.active as string)}
        emptyAction={
          active && next ? (
            <>
              <Button asChild>
                <Link to={siteSetupPath(next)}>Install the tracker</Link>
              </Button>
              {docs ? (
                <Button asChild variant="ghost">
                  <a href={docs}>Docs</a>
                </Button>
              ) : null}
            </>
          ) : null
        }
      />
    </div>
  );
}

function FigureRow({ children }: { children: ReactNode }) {
  return (
    <section aria-label="Totals" className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
      {children}
    </section>
  );
}

// ---- totals, the chart and the breakdowns

// quarantined traffic that grows is bad news, so its colour turns
function bandChange(c: StatChange | null, band: WebBand): StatChange | null {
  if (!c || band === "primary" || c.good === null) return c;
  return { ...c, good: !c.good };
}

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
        "flex flex-col gap-4 transition-opacity duration-(--vs-duration-change) ease-standard",
        stale && "opacity-60",
      )}
      aria-busy={stale || undefined}
      data-testid="web-dashboard"
      data-band={q.band}
    >
      <FigureRow>
        {METRICS.map((t) => {
          const v = metricValue(cur.totals, t.metric);
          return (
            <StatCard
              key={t.metric}
              data-metric={t.metric}
              label={METRIC_LABEL[t.metric]}
              definition={t.definition}
              value={v}
              format={(n) => formatMetric(t.metric, n)}
              missingReason="No visits in this range"
              change={bandChange(
                webTrend(t.metric, v, metricValue(prev.totals, t.metric), cur.range.days),
                q.band,
              )}
              selected={chartable(t.metric) ? metric === t.metric : undefined}
              onSelect={chartable(t.metric) ? () => set({ metric: t.metric }) : undefined}
            />
          );
        })}
      </FigureRow>

      <WebTrendChart tenantId={tenantId} q={q} summary={summary} metric={metric} />

      <Breakdowns
        tenantId={tenantId}
        q={q}
        visitors={cur.totals.visitors}
        sites={sites}
        utm={utm}
      />

      <CustomEvents tenantId={tenantId} q={q} summary={summary} />
    </div>
  );
}

// ---- breakdowns: four equal panels, each with its own switch

type Group = { title: string; options: WebDimension[] };

const SHORT: Partial<Record<WebDimension, string>> = {
  page: "Pages",
  entry_page: "Entry",
  exit_page: "Exit",
  referrer: "Referrers",
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
  ];
  // the site of a row shows only when two sites share the list
  const names =
    q.site || sites.length < 2 ? null : Object.fromEntries(sites.map((s) => [s.id, s.name]));
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
const MORE = 50;

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
  return (
    <Panel
      title={group.title}
      data-breakdown={group.title}
      actions={
        group.options.length > 1 ? (
          <Segmented<WebDimension>
            label={`${group.title} by`}
            value={active}
            options={group.options.map((o) => ({
              value: o,
              label: SHORT[o] ?? o,
            }))}
            onChange={(v) => {
              setDim(v);
              setLimit(LIMIT);
            }}
            className="max-w-full overflow-x-auto"
          />
        ) : null
      }
    >
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
                    <span className="shrink-0 truncate text-caption text-fg-3">{site}</span>
                  ) : null}
                </span>
              ),
              value: it.value,
            };
          })}
          total={visitors}
          tone={q.band === "primary" ? "series-1" : "warning"}
          shareDefinition="The share of the range's visitors. A visitor can count in several rows, so the shares can add up to more than 100%."
          valueLabel="Visitors"
          emptyTitle={`No ${DIMENSION_LABEL[active].toLowerCase()} in this range`}
          onShowMore={
            data && data.total_keys > data.items.length && limit < MORE
              ? () => setLimit(MORE)
              : undefined
          }
          showMoreLabel={
            (data?.total_keys ?? 0) <= MORE
              ? `Show all ${formatCount(data?.total_keys ?? 0)}`
              : `Show the top ${MORE} of ${formatCount(data?.total_keys ?? 0)}`
          }
          showMorePending={b.isFetching && b.isPlaceholderData}
        />
      </div>
    </Panel>
  );
}

// ---- custom events: one card each, with its count and its change against
// the previous period. with no event in the range the panel does not exist

const EVENTS_SHOWN = 4;
const EVENTS_MORE = 50;

function CustomEvents({
  tenantId,
  q,
  summary,
}: {
  tenantId: string;
  q: WebQuery;
  summary: WebSummary;
}) {
  const [limit, setLimit] = useState(EVENTS_SHOWN);
  const cur = useQuery(webBreakdownQuery(tenantId, q, "event", limit));
  const p = summary.previous.range;
  const prev = useQuery(
    webBreakdownQuery(tenantId, { ...q, from: p.from, to: p.to }, "event", EVENTS_MORE),
  );
  if (cur.isPending) return null;
  if (cur.error)
    return (
      <ErrorState
        compact
        title="The custom events did not load"
        error={cur.error}
        onRetry={() => cur.refetch()}
      />
    );
  const items = cur.data.items;
  if (items.length === 0) return null;
  const before = new Map(
    (prev.data?.items ?? []).map((i) => [`${i.site_id ?? ""}|${i.key}`, i.value]),
  );
  const more = cur.data.total_keys > items.length && limit < EVENTS_MORE;
  return (
    <section
      aria-labelledby="custom-events-h"
      data-testid="custom-events"
      className="flex min-w-0 flex-col gap-3 rounded-2xl border border-line bg-raised px-5 py-4"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2
          id="custom-events-h"
          className="flex items-baseline gap-2.5 text-body font-semibold text-fg"
        >
          Custom events
          <span className="text-[12.5px] font-normal text-fg-3 tabular">
            {formatCount(cur.data.total_keys)} {cur.data.total_keys === 1 ? "event" : "events"}
          </span>
        </h2>
        {more ? (
          <Button variant="ghost" size="sm" onClick={() => setLimit(EVENTS_MORE)}>
            Show all
          </Button>
        ) : null}
      </div>
      <ul
        aria-label="Custom events"
        className="grid gap-2.5"
        style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(13rem, 100%), 1fr))" }}
      >
        {items.map((it) => {
          const k = `${it.site_id ?? ""}|${it.key}`;
          const change = prev.data
            ? bandChange(
                webTrend("pageviews", it.value, before.get(k) ?? 0, summary.current.range.days),
                q.band,
              )
            : null;
          return (
            <li
              key={k}
              data-event={it.key}
              className="flex min-w-0 flex-col gap-1 rounded-xl border border-line px-4 py-3"
            >
              <code className="truncate font-mono text-small text-fg-2" title={it.key}>
                {it.key}
              </code>
              <span className="flex items-baseline gap-2">
                <span className="text-[22px] leading-7 font-semibold text-fg tabular">
                  {formatCount(it.value)}
                </span>
                {change ? <ChangeLine change={change} /> : null}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// ---- where events went: every event by outcome, the uncounted slice
// opened into each reason

const GROUPS: ZoomGroup[] = [
  {
    label: "Quarantined",
    tone: "warning",
    definition: "Many visitors from one network, or very many events from one visitor.",
  },
  { label: "Bots", tone: "neutral", definition: "The browser is a known bot." },
  {
    label: "Rejected",
    tone: "critical",
    definition: "Refused at the edge, for the reasons below.",
  },
  { label: "Not stored", tone: "muted", definition: "Accepted, but the write to storage failed." },
];

function WhereEventsWent({ sites, q }: { sites: Site[]; q: WebQuery }) {
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
  if (span === null) return null;
  const failed = answers.find((a) => a.error);
  if (failed)
    return (
      <ErrorState
        compact
        title="Where events went did not load"
        error={failed.error}
        onRetry={() => {
          for (const a of answers) if (a.error) a.refetch();
        }}
      />
    );
  if (answers.some((a) => a.isPending))
    return (
      <div
        aria-busy="true"
        className="flex flex-col gap-3 rounded-2xl border border-line bg-raised px-5 py-4"
      >
        <span className="sr-only">Loading where events went</span>
        <Bone className="h-4 w-40" />
        <Bone className="h-8 w-64" />
        <Bone className="h-3 w-full" />
      </div>
    );
  const totals = sumOutcomes(answers.map((a) => a.data));
  // the edge reached nothing in the range: no block of zeros
  if (Object.values(totals).every((v) => v === 0)) return null;
  const other = sumUnclassified(totals);
  const parts: ZoomPart[] = [
    {
      key: "quarantined",
      label: "Quarantined",
      count: totals.quarantined ?? 0,
      group: "Quarantined",
      definition: GROUPS[0]?.definition,
    },
    {
      key: "bot",
      label: "Bots",
      count: totals.bot ?? 0,
      group: "Bots",
      definition: GROUPS[1]?.definition,
    },
    ...rejectionRows(totals).map((r, i) => ({
      key: r.key,
      label: r.label,
      count: r.count,
      group: "Rejected",
      shade: i,
    })),
    { key: "dlq", label: "Not stored", count: totals.dlq ?? 0, group: "Not stored" },
    ...(other > 0
      ? [{ key: "other", label: "Other", count: other, group: "Not stored", shade: 1 }]
      : []),
  ];
  return (
    <ZoomBlock
      title="Where events went"
      unit="events"
      main={{
        label: "Counted",
        count: totals.accepted ?? 0,
        definition: "Events the totals include.",
      }}
      groups={GROUPS}
      parts={parts}
      caption="Not counted, by reason"
      cleanText="Every event in this range was counted"
    />
  );
}
