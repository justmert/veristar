// the project overview: what is happening now, what changed and what needs
// the builder. the sites are chips that filter the web parts of the page;
// eight figures (web, api, on-chain and the integrity grade) with their
// change, the selected one drawing the chart; beside the chart the few
// things to act on, which while setup is open are the next steps; then the
// top pages, sources and routes (countries without an api), each opening
// its full page.

import { useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight, Check } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { ApiFilters } from "@/api/endpoints";
import { onchainQuery } from "@/api/onchain";
import {
  apiOverviewQuery,
  apiRoutesQuery,
  apiTimeseriesQuery,
  ingestMonthQuery,
  integrityQuery,
  sitesQuery,
} from "@/api/queries";
import type { ApiOverview, IntegrityGrade, Site, TenantStatus } from "@/api/schemas";
import { type WebDimension, type WebSummary, webBreakdownQuery, webSummaryQuery } from "@/api/web";
import { BreakdownList, type TimeBucket, TimeChart } from "@/components/charts";
import { GradeRing } from "@/components/grade-ring";
import { PageHeader } from "@/components/page-header";
import { Panel } from "@/components/panel";
import { RangeControl } from "@/components/range-control";
import { SetupBar, type SetupStep } from "@/components/setup-progress";
import { StatCard, type StatChange } from "@/components/stat-card";
import { ErrorState, Skeleton } from "@/components/states";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  formatBucket,
  latencyText,
  percentileChange,
  plotPercentile,
  rateChange,
  relativeChange,
  tickFormatter,
  toStatChange,
} from "@/lib/api-metrics";
import {
  countOf,
  formatChange,
  formatCompact,
  formatCount,
  formatDate,
  formatDateRange,
  formatPercent,
  formatTick,
  utcDay,
} from "@/lib/format";
import type { DayRange } from "@/lib/global";
import { siblingUrl } from "@/lib/hosts";
import { shortKey, verifiedActivity } from "@/lib/onchain";
import { apiRangeOfDays, type OverviewPreset, readOverviewRange } from "@/lib/overview";
import { combineFirstEvent, type FirstEvent, firstEvent, siteSetupPath } from "@/lib/site-setup";
import {
  dayCount,
  formatMetric,
  keyLabel,
  metricValue,
  periodLabel,
  WEB_MAX_DAYS,
  WEB_PRESETS,
  type WebMetric,
  webPath,
  webTrend,
} from "@/lib/web-dashboard";
import { canAddSites } from "@/pages/project/sites";
import { useProject } from "@/pages/project-layout";
import { OverviewSkeleton, PAGE_HEADERS } from "./page-frame";
import { LiveCount, SiteChips } from "./site-chips";
import { METRIC_LABEL, WebTrendChart } from "./web-chart";

const NO_FILTERS: ApiFilters = { service: null, route: null, method: null };
const CADENCE = "Web totals refresh every hour. API counts refresh every few minutes.";

// a project that is not approved, is suspended or was removed shows no setup
// step: no event can arrive. the status notice above the page says why.
const STOPPED: ReadonlySet<TenantStatus> = new Set(["rejected", "suspended", "removed"]);

function noSiteTitle(status: TenantStatus, termsCurrent: boolean): string {
  if (!termsCurrent) return "An owner must accept the new terms before you can add sites";
  switch (status) {
    case "suspended":
      return "You cannot add sites while the project is suspended";
    case "removed":
      return "This project was removed";
    default:
      return "No sites yet";
  }
}

type Figure = WebMetric | "requests" | "5xx" | "p95" | "operations";

export function OverviewPage() {
  const { tenant, termsCurrent } = useProject();
  const stopped = STOPPED.has(tenant.status);
  const canAdd = canAddSites(tenant.status, termsCurrent);
  const sites = useQuery(sitesQuery(tenant.id));
  const [params, setParams] = useSearchParams();
  const { preset, days } = readOverviewRange(params, new Date());
  const live = sites.data?.filter((s) => s.status !== "removed") ?? [];
  const siteParam = params.get("site");
  const site = live.find((s) => s.id === siteParam)?.id ?? null;
  const summary = useQuery({
    ...webSummaryQuery(tenant.id, { ...days, site, band: "primary" }),
    enabled: live.length > 0,
  });

  const setParam = (k: string, v: string | null) => {
    const next = new URLSearchParams(params);
    if (v === null) next.delete(k);
    else next.set(k, v);
    setParams(next, { replace: true });
  };

  const header = (
    <PageHeader
      {...PAGE_HEADERS.overview}
      freshness={summary.data?.read_at ? { at: summary.data.read_at, cadence: CADENCE } : null}
      items={[`${formatDateRange(days.from, days.to)}, UTC`]}
      secondary={
        <RangeControl<OverviewPreset>
          value={preset}
          presets={WEB_PRESETS}
          onPreset={(p) => {
            const next = new URLSearchParams(params);
            next.delete("from");
            next.delete("to");
            if (p === "30") next.delete("range");
            else next.set("range", p);
            setParams(next, { replace: true });
          }}
          custom={preset === "custom" ? days : null}
          onCustom={(r) => {
            const next = new URLSearchParams(params);
            next.set("range", "custom");
            next.set("from", r.from);
            next.set("to", r.to);
            setParams(next, { replace: true });
          }}
          maxDay={utcDay()}
          maxDays={WEB_MAX_DAYS}
          range={days}
          words={false}
        />
      }
    />
  );

  let body: ReactNode;
  if (sites.isPending) body = <OverviewSkeleton />;
  else if (sites.error)
    body = (
      <ErrorState
        title="The sites did not load"
        error={sites.error}
        onRetry={() => sites.refetch()}
      />
    );
  else
    body = (
      <>
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          {live.length > 0 ? (
            <SiteChips
              tenantId={tenant.id}
              sites={live}
              value={site}
              onChange={(s) => setParam("site", s)}
              canAdd={canAdd && !stopped}
            />
          ) : (
            <span />
          )}
          {live.length > 0 && !stopped ? <LiveCount tenantId={tenant.id} site={site} /> : null}
        </div>
        <Dashboard
          tenantId={tenant.id}
          sites={live}
          site={site}
          days={days}
          summary={summary.data ?? null}
          summaryState={
            live.length === 0
              ? "data"
              : summary.isPending
                ? "loading"
                : summary.error
                  ? "error"
                  : "data"
          }
          summaryError={summary.error}
          onRetry={() => summary.refetch()}
          stopped={stopped}
          canAdd={canAdd}
          noSite={noSiteTitle(tenant.status, termsCurrent)}
        />
      </>
    );

  return (
    <div className="flex flex-col gap-4">
      {header}
      {body}
    </div>
  );
}

// ---- the figures, the chart and what needs attention

const WEB_FIGURES: { metric: WebMetric; definition: string }[] = [
  { metric: "pageviews", definition: "Pages loaded in a browser." },
  { metric: "visitors", definition: "Distinct visitors per UTC day, added up over the range." },
  { metric: "bounce_rate", definition: "Visits with one page view and no custom event." },
];

function usesApi(o: ApiOverview): boolean {
  return (
    o.current.summary.requests > 0 ||
    o.previous.summary.requests > 0 ||
    o.current.sources.length > 0
  );
}

// the range of equal length right before a range of days
export function previousDays(r: DayRange): DayRange {
  const n = dayCount(r);
  const start = Date.parse(`${r.from}T00:00:00Z`);
  return {
    from: utcDay(new Date(start - n * 86_400_000)),
    to: utcDay(new Date(start - 86_400_000)),
  };
}

function changeOf(r: number, period: string): StatChange {
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period,
  };
}

function Dashboard({
  tenantId,
  sites,
  site,
  days,
  summary,
  summaryState,
  summaryError,
  onRetry,
  stopped,
  canAdd,
  noSite,
}: {
  tenantId: string;
  sites: Site[];
  site: string | null;
  days: DayRange;
  summary: WebSummary | null;
  summaryState: "loading" | "error" | "data";
  summaryError: unknown;
  onRetry: () => void;
  stopped: boolean;
  canAdd: boolean;
  noSite: string;
}) {
  const [figure, setFigure] = useState<Figure>("pageviews");
  const api = useQuery(apiOverviewQuery(tenantId, apiRangeOfDays(days), NO_FILTERS));
  const chain = useQuery(onchainQuery(tenantId, days));
  const chainBefore = useQuery(onchainQuery(tenantId, previousDays(days)));
  const grade = useQuery(integrityQuery("tenant", tenantId, days));
  const single = days.from === days.to;
  const period = periodLabel(dayCount(days));
  const hasSite = sites.length > 0;
  const noWeb = !hasSite || (summary !== null && summary.current.totals.pageviews === 0);

  const apiUsed = api.data ? usesApi(api.data) : null;
  const activity = chain.data ? verifiedActivity(chain.data) : null;
  const before = chainBefore.data ? verifiedActivity(chainBefore.data) : null;
  const chainUsed = activity ? activity.verified > 0 : null;
  const unproven = chain.data?.ids.find((i) => i.verification.status !== "verified") ?? null;
  // operations against the same days before, only where both are measured
  const opsRatio =
    activity && before && !activity.gap && !before.gap && before.operations > 0
      ? (activity.operations - before.operations) / before.operations
      : null;

  const webChartable = (m: WebMetric) => !single || m === "pageviews" || m === "visitors";
  const selectable = (f: Figure): boolean => {
    if (f === "requests" || f === "5xx" || f === "p95") return apiUsed === true;
    if (f === "operations") return chainUsed === true && !activity?.gap;
    return hasSite && !noWeb && webChartable(f);
  };
  const shown: Figure = selectable(figure) ? figure : "pageviews";
  const pick = (f: Figure) =>
    selectable(f)
      ? { selected: shown === f, onSelect: () => setFigure(f) }
      : { selected: undefined, onSelect: undefined };

  const cur = api.data?.current.summary;
  const prev = api.data?.previous.summary;
  const apiChange = (k: "requests" | "5xx" | "p95"): StatChange | null => {
    if (!cur || !prev || !apiUsed) return null;
    if (k === "requests")
      return toStatChange(relativeChange(cur.requests, prev.requests), null, period);
    if (k === "5xx") return toStatChange(rateChange(cur.rate_5xx, prev.rate_5xx), true, period);
    return toStatChange(percentileChange(cur.p95, prev.p95), true, period);
  };
  const apiState = api.isPending ? "loading" : api.error ? "error" : "data";
  const onchainPath = `/projects/${tenantId}/onchain`;

  const g = grade.data;
  const graded = hasSite && g !== undefined && g.graded_days > 0 ? g : null;

  return (
    <>
      {/* web, api and on-chain figures in three groups with a wider gap between
          them on a wide screen, as one row of tiles that wraps on a narrow one */}
      <section
        aria-label="Figures"
        className="grid grid-cols-[repeat(auto-fit,minmax(min(8rem,100%),1fr))] gap-2 xl:grid-cols-[repeat(3,minmax(0,1fr))_10px_repeat(3,minmax(0,1fr))_10px_repeat(2,minmax(0,1fr))]"
      >
        {WEB_FIGURES.map((c) => {
          const v = summary ? metricValue(summary.current.totals, c.metric) : null;
          return (
            <StatCard
              key={c.metric}
              data-metric={c.metric}
              series={1}
              label={METRIC_LABEL[c.metric]}
              definition={c.definition}
              value={noWeb ? null : v}
              format={(n) => formatMetric(c.metric, n)}
              missingReason={hasSite ? "No visits in this range" : "No site yet"}
              state={summaryState}
              change={
                summary && !noWeb
                  ? webTrend(
                      c.metric,
                      v,
                      metricValue(summary.previous.totals, c.metric),
                      summary.current.range.days,
                    )
                  : null
              }
              {...pick(c.metric)}
            />
          );
        })}
        <span aria-hidden className="hidden xl:block" />
        <StatCard
          data-tile="API requests"
          series={3}
          label="API requests"
          definition="Requests that your services answered. Health checks and preflights are not included."
          value={apiUsed ? (cur?.requests ?? null) : null}
          missingReason="No API connected"
          state={apiState}
          change={apiChange("requests")}
          action={
            apiUsed === false && !stopped ? (
              <Link to={`/projects/${tenantId}/connect-api`}>Connect API</Link>
            ) : undefined
          }
          {...pick("requests")}
        />
        <StatCard
          data-tile="API 5xx rate"
          series={3}
          label="API 5xx rate"
          definition="Share of requests answered with a 5xx status."
          value={apiUsed ? (cur?.rate_5xx ?? null) : null}
          format={formatPercent}
          missingReason={apiUsed ? "No API requests in this range" : "No API connected"}
          state={apiState}
          change={apiChange("5xx")}
          {...pick("5xx")}
        />
        <StatCard
          data-tile="API p95"
          series={3}
          label="API p95 latency"
          definition="95 of 100 requests were faster than this."
          value={apiUsed && cur ? latencyText(cur.p95) : null}
          missingReason={apiUsed ? "No API requests in this range" : "No API connected"}
          state={apiState}
          change={apiChange("p95")}
          {...pick("p95")}
        />
        <span aria-hidden className="hidden xl:block" />
        <StatCard
          data-tile="On-chain ops"
          series="success"
          label="On-chain ops"
          definition="Operations of your proven accounts and contracts, days before today."
          value={chainUsed && activity && !activity.gap ? activity.operations : null}
          missingReason={activity?.gap ?? "No proven account or contract"}
          state={chain.isPending ? "loading" : chain.error ? "error" : "data"}
          change={opsRatio === null ? null : changeOf(opsRatio, period)}
          action={
            chainUsed === false && !stopped ? (
              <Link to={onchainPath}>{unproven ? "Prove control" : "Add an account"}</Link>
            ) : undefined
          }
          {...pick("operations")}
        />
        <StatCard
          data-tile="Integrity grade"
          series="neutral"
          label="Integrity grade"
          definition="How likely the project's web traffic is real, graded per site and day."
          value={graded?.grade ?? null}
          missingReason="Not graded"
          state={grade.isPending && hasSite ? "loading" : "data"}
          lead={<GradeRing row={graded} size="xs" />}
          footer={
            <Link
              to={`/projects/${tenantId}/scf`}
              className="w-fit rounded-sm text-caption text-fg-2 hover:text-fg focus-ring"
              data-grade-days=""
            >
              {graded ? countOf(graded.graded_days, "site day") : "not graded"}
            </Link>
          }
        />
      </section>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <div className="min-w-0">
          {shown === "requests" || shown === "5xx" || shown === "p95" ? (
            <ApiChart tenantId={tenantId} days={days} figure={shown} />
          ) : shown === "operations" && activity ? (
            <ChainChart daily={activity.daily} />
          ) : !hasSite ? (
            <WaitPanel title={noSite} />
          ) : summaryState === "error" ? (
            <ErrorState title="The numbers did not load" error={summaryError} onRetry={onRetry} />
          ) : summary && noWeb ? (
            <WaitPanel title="Waiting for the first page view" />
          ) : summary ? (
            <WebTrendChart
              tenantId={tenantId}
              q={{ ...days, site, band: "primary" }}
              summary={summary}
              metric={shown as WebMetric}
            />
          ) : (
            <Skeleton.Chart />
          )}
        </div>
        <Attention
          tenantId={tenantId}
          sites={sites}
          days={days}
          stopped={stopped}
          canAdd={canAdd}
          apiUsed={apiUsed}
          chainUsed={chainUsed}
          unproven={unproven ? unproven.strkey : null}
          grade={graded}
          opsRatio={opsRatio}
        />
      </div>

      {hasSite && !noWeb ? (
        <TopLists tenantId={tenantId} days={days} site={site} apiUsed={apiUsed === true} />
      ) : null}
    </>
  );
}

// the chart's place before the first page view: the empty plot keeps its axis
function WaitPanel({ title }: { title: string }) {
  return (
    <TimeChart
      title="Page views"
      description="per day, UTC"
      buckets={[]}
      series={[]}
      state="empty"
      height={240}
      emptyTitle={title}
      emptyAction={null}
      className="h-full"
    />
  );
}

// ---- the chart of an api figure, per hour or per day

const API_LABEL = { requests: "API requests", "5xx": "API 5xx rate", p95: "API p95 latency" };

function ApiChart({
  tenantId,
  days,
  figure,
}: {
  tenantId: string;
  days: DayRange;
  figure: "requests" | "5xx" | "p95";
}) {
  const ts = useQuery(apiTimeseriesQuery(tenantId, apiRangeOfDays(days), NO_FILTERS));
  const points = ts.data?.points ?? [];
  const range = ts.data?.range;
  const now = Date.now();
  const buckets: TimeBucket[] = points.map((p) => ({
    at: p.at,
    tick: range ? tickFormatter(range)(p.at) : p.at,
    label: range ? formatBucket(p.at, range) : p.at,
    partial: range ? Date.parse(p.at) + range.bucket_seconds * 1000 > now : false,
  }));
  const values = points.map((p) =>
    figure === "requests" ? p.requests : figure === "5xx" ? p.rate_5xx : plotPercentile(p.p95),
  );
  const fmt = (n: number) =>
    figure === "requests"
      ? formatCount(n)
      : figure === "5xx"
        ? formatPercent(n)
        : (latencyText(n) ?? "");
  const total = points.reduce((a, p) => a + p.requests, 0);
  return (
    <TimeChart
      key={figure}
      title={API_LABEL[figure]}
      description={range?.resolution === "day" ? "per day, UTC" : "per hour, UTC"}
      kind={figure === "requests" ? "bar" : "line"}
      buckets={buckets}
      series={[{ key: figure, label: API_LABEL[figure], tone: "series-3", values }]}
      format={fmt}
      axisFormat={figure === "requests" ? formatCompact : fmt}
      higherIsBetter={figure === "requests"}
      state={ts.isPending ? "loading" : ts.error ? "error" : total === 0 ? "empty" : "data"}
      error={ts.error}
      onRetry={() => ts.refetch()}
      emptyTitle="No API requests in this range"
    />
  );
}

// ---- the chart of on-chain operations per day

function ChainChart({ daily }: { daily: { day: string; value: number | null }[] }) {
  const today = utcDay();
  const buckets: TimeBucket[] = daily.map((d) => ({
    at: `${d.day}T00:00:00Z`,
    tick: formatTick(d.day, "day"),
    label: formatDate(d.day),
    partial: d.day === today,
  }));
  return (
    <TimeChart
      title="On-chain operations"
      description="per day, UTC"
      buckets={buckets}
      series={[
        {
          key: "operations",
          label: "Operations",
          tone: "series-2",
          values: daily.map((d) => d.value),
        },
      ]}
      axisFormat={formatCompact}
      state={daily.some((d) => (d.value ?? 0) > 0) ? "data" : "empty"}
      emptyTitle="No operations in this range"
    />
  );
}

// ---- needs attention, or next steps while setup is open

type Item = {
  key: string;
  tone: "warning" | "critical" | "neutral";
  text: ReactNode;
  actions: ReactNode;
};

// the first event of every active site: the edge counts of 30 days, and
// while those have none, the web totals of the range
function useFirstEvents(sites: Site[], tenantId: string, days: DayRange) {
  const active = sites.filter((s) => s.status === "active" && !s.in_review);
  const ingest = useQueries({ queries: active.map((s) => ingestMonthQuery(s.id)) });
  const fromEdge = active.map((_, i) => {
    const d = ingest[i]?.data;
    return d ? firstEvent(d) : null;
  });
  // the edge counts lag by minutes, the web totals by an hour: either one
  // with a counted event completes the step (lib/site-setup.ts)
  const unsure = active.filter((_, i) => fromEdge[i] !== null && fromEdge[i] !== "received");
  const web = useQueries({
    queries: unsure.map((s) => ({
      ...webSummaryQuery(tenantId, { ...days, site: s.id, band: "primary" as const }),
      placeholderData: undefined,
    })),
  });
  const events = new Map<string, FirstEvent | null>();
  active.forEach((s, i) => {
    const j = unsure.indexOf(s);
    const w = j < 0 ? undefined : web[j];
    if (j < 0) events.set(s.id, fromEdge[i] ?? null);
    else if (w?.isPending) events.set(s.id, null);
    else {
      const views = w?.data?.current.totals.pageviews;
      events.set(
        s.id,
        combineFirstEvent(fromEdge[i] ?? null, views === undefined ? null : views > 0),
      );
    }
  });
  const loading = ingest.some((q) => q.isPending) || web.some((q) => q.isPending);
  return { active, events, loading };
}

function Attention({
  tenantId,
  sites,
  days,
  stopped,
  canAdd,
  apiUsed,
  chainUsed,
  unproven,
  grade,
  opsRatio,
}: {
  tenantId: string;
  sites: Site[];
  days: DayRange;
  stopped: boolean;
  canAdd: boolean;
  apiUsed: boolean | null;
  chainUsed: boolean | null;
  unproven: string | null;
  grade: { grade: IntegrityGrade | null; days_b: number; days_c: number } | null;
  opsRatio: number | null;
}) {
  const first = useFirstEvents(sites, tenantId, days);
  const failing = useQuery({
    ...apiRoutesQuery(tenantId, apiRangeOfDays(days), NO_FILTERS, {
      sort: "rate_5xx",
      order: "desc",
      limit: 3,
      cursor: null,
    }),
    enabled: apiUsed === true,
  });
  const docs = siblingUrl(window.location.hostname, "docs");
  const p = `/projects/${tenantId}`;

  // setup: a site, its tracker sending, its domain verified
  const hasSite = sites.length > 0;
  const waiting = first.active.filter((s) => first.events.get(s.id) !== "received");
  const unverified = first.active.filter((s) => s.verification !== "verified");
  const steps: SetupStep[] = [
    { key: "site", label: "Add a site", state: hasSite ? "done" : "current" },
    {
      key: "tracker",
      label: "Install the tracker",
      state: !hasSite ? "todo" : waiting.length > 0 ? "current" : "done",
    },
    {
      key: "domain",
      label: "Verify the domain",
      state: !hasSite ? "todo" : unverified.length > 0 ? "current" : "done",
    },
  ];
  const setupOpen = !stopped && steps.some((s) => s.state !== "done");
  const done = steps.filter((s) => s.state === "done").length;

  const items: Item[] = [];
  if (!stopped) {
    if (!hasSite && canAdd)
      items.push({
        key: "site",
        tone: "warning",
        text: "No site yet",
        actions: (
          <Button asChild size="sm">
            <Link to={`${p}/sites`}>Add site</Link>
          </Button>
        ),
      });
    for (const s of first.active) {
      const ev = first.events.get(s.id);
      if (ev === null || ev === undefined || ev === "received") continue;
      items.push({
        key: `event-${s.id}`,
        tone: ev === "refused" ? "critical" : "warning",
        text: ev === "refused" ? `${s.name} sends refused events` : `${s.name} has no event yet`,
        actions: (
          <>
            <Button asChild size="sm">
              <Link to={siteSetupPath(s)}>
                {ev === "refused" ? "See why events are refused" : "Continue setup"}
              </Link>
            </Button>
            {docs ? (
              <Button asChild size="sm" variant="ghost">
                <a href={docs}>Docs</a>
              </Button>
            ) : null}
          </>
        ),
      });
    }
    for (const s of unverified) {
      if (first.events.get(s.id) !== "received") continue;
      items.push({
        key: `domain-${s.id}`,
        tone: "warning",
        text: `${s.domain} is not verified`,
        actions: (
          <Button asChild size="sm" variant="outline">
            <Link to={siteSetupPath(s)}>Verify domain</Link>
          </Button>
        ),
      });
    }
  }
  for (const r of failing.data?.items ?? []) {
    if (!r.rate_5xx) continue;
    const q = new URLSearchParams({ route: r.route, method: r.method, service: r.service });
    items.push({
      key: `route-${r.service}-${r.method}-${r.route}`,
      tone: "critical",
      text: `${r.method} ${r.route} fails ${formatPercent(r.rate_5xx)} of the time`,
      actions: (
        <Button asChild size="sm" variant="outline">
          <Link to={`${p}/api?${q}`}>Open the route</Link>
        </Button>
      ),
    });
  }
  if (grade?.grade && grade.grade !== "A")
    items.push({
      key: "grade",
      tone: "warning",
      text: `Grade lowered to ${grade.grade} on ${countOf(grade.days_b + grade.days_c, "day")}`,
      actions: (
        <Button asChild size="sm" variant="outline">
          <Link to={`${p}/scf`}>See why</Link>
        </Button>
      ),
    });
  if (opsRatio !== null && opsRatio < -0.0005)
    items.push({
      key: "ops",
      tone: "warning",
      text: `On-chain operations down ${formatChange(opsRatio).replace(/^[-−]/, "")}`,
      actions: (
        <Button asChild size="sm" variant="outline">
          <Link to={`${p}/onchain`}>Open On-chain</Link>
        </Button>
      ),
    });
  if (!stopped) {
    if (unproven)
      items.push({
        key: "unproven",
        tone: "neutral",
        text: `${shortKey(unproven)} is not proven yet`,
        actions: (
          <Button asChild size="sm" variant="outline">
            <Link to={`${p}/onchain`}>Prove control</Link>
          </Button>
        ),
      });
    if (apiUsed === false)
      items.push({
        key: "api",
        tone: "neutral",
        text: "No API connected",
        actions: (
          <Button asChild size="sm" variant="outline">
            <Link to={`${p}/connect-api`}>Connect API</Link>
          </Button>
        ),
      });
    if (chainUsed === false && !unproven)
      items.push({
        key: "chain",
        tone: "neutral",
        text: "No Stellar account or contract",
        actions: (
          <Button asChild size="sm" variant="outline">
            <Link to={`${p}/onchain`}>Add an account</Link>
          </Button>
        ),
      });
  }

  const nextSteps = setupOpen || items.some((i) => i.tone === "neutral");
  const title = nextSteps ? "Next steps" : "Needs attention";
  return (
    <Panel
      data-setup={setupOpen ? "" : undefined}
      title={title}
      actions={
        setupOpen ? (
          <span className="text-caption text-fg-3 tabular">
            setup {done} of {steps.length}
          </span>
        ) : items.length ? (
          <span className="text-caption text-fg-3 tabular">{formatCount(items.length)}</span>
        ) : null
      }
      className="h-full"
    >
      {setupOpen ? <SetupBar steps={steps} className="mb-1" /> : null}
      {first.loading && hasSite ? (
        <div className="pt-3">
          <Skeleton.Table rows={2} columns={1} />
        </div>
      ) : items.length === 0 ? (
        <p className="flex items-center gap-2 pt-1 text-small text-fg-2">
          <Check aria-hidden strokeWidth={2.6} className="size-3.5 text-success" />
          Nothing needs you now
        </p>
      ) : (
        <ul aria-label={title} className="flex flex-col">
          {items.map((it) => (
            <li
              key={it.key}
              data-attention={it.key}
              className="grid grid-cols-[10px_minmax(0,1fr)] items-start gap-3 border-t border-line-subtle py-3 first:border-t-0"
            >
              <StatusDot tone={it.tone} className="mt-1.5" />
              <span className="flex min-w-0 flex-col gap-2">
                <span className="text-body text-fg">{it.text}</span>
                <span className="flex flex-wrap items-center gap-2">{it.actions}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

// ---- the top lists, each opening its full page

function TopLists({
  tenantId,
  days,
  site,
  apiUsed,
}: {
  tenantId: string;
  days: DayRange;
  site: string | null;
  apiUsed: boolean;
}) {
  const web = webPath(tenantId, site);
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <WebTop title="Top pages" tenantId={tenantId} days={days} site={site} dim="page" to={web} />
      <WebTop
        title="Top sources"
        tenantId={tenantId}
        days={days}
        site={site}
        dim="referrer"
        to={web}
      />
      {apiUsed ? (
        <BusiestRoutes tenantId={tenantId} days={days} />
      ) : (
        <WebTop
          title="Countries"
          tenantId={tenantId}
          days={days}
          site={site}
          dim="country"
          to={web}
        />
      )}
    </div>
  );
}

function TopPanel({
  title,
  to,
  link,
  children,
}: {
  title: string;
  to: string;
  link: string;
  children: ReactNode;
}) {
  return (
    <Panel
      title={title}
      data-summary={title}
      actions={
        <Link
          to={to}
          aria-label={`${title}, open ${link}`}
          className="inline-flex items-center gap-1 rounded-sm text-caption text-fg-2 hover:text-fg focus-ring"
        >
          {link}
          <ArrowRight aria-hidden className="size-3" />
        </Link>
      }
    >
      {children}
    </Panel>
  );
}

const TOP = 5;

// a key with a quieter tag after it (the site of a page, the service of a
// route). the key always reads first; the tag wraps onto a hidden second
// line when the row has no room for it, so it never squeezes the key
function KeyWithTag({ text, tag }: { text: string; tag?: string | undefined }) {
  return (
    <span className="flex h-[1lh] min-w-0 flex-wrap items-baseline gap-x-2 overflow-hidden">
      <span className="min-w-0 max-w-full truncate">{text}</span>
      {tag ? <span className="text-caption text-fg-3 whitespace-nowrap">{tag}</span> : null}
    </span>
  );
}

function WebTop({
  title,
  tenantId,
  days,
  site,
  dim,
  to,
}: {
  title: string;
  tenantId: string;
  days: DayRange;
  site: string | null;
  dim: WebDimension;
  to: string;
}) {
  const b = useQuery(webBreakdownQuery(tenantId, { ...days, site, band: "primary" }, dim, TOP));
  const sites = b.data?.sites ?? [];
  const names =
    site || sites.length < 2 ? null : Object.fromEntries(sites.map((s) => [s.id, s.name]));
  return (
    <TopPanel title={title} to={to} link="Web">
      <BreakdownList
        label={title}
        state={b.isPending ? "loading" : b.error ? "error" : undefined}
        error={b.error}
        onRetry={() => b.refetch()}
        items={(b.data?.items ?? []).map((it) => {
          const text = keyLabel(dim, it.key);
          const siteName = names && it.site_id ? names[it.site_id] : undefined;
          return {
            key: `${it.site_id ?? ""}|${it.key}`,
            text: siteName ? `${text}, ${siteName}` : text,
            label: <KeyWithTag text={text} tag={siteName} />,
            value: it.value,
          };
        })}
        header={false}
        valueLabel="Visitors"
        emptyTitle="Nothing in this range"
      />
    </TopPanel>
  );
}

function BusiestRoutes({ tenantId, days }: { tenantId: string; days: DayRange }) {
  const r = useQuery(
    apiRoutesQuery(tenantId, apiRangeOfDays(days), NO_FILTERS, {
      sort: "requests",
      order: "desc",
      limit: TOP,
      cursor: null,
    }),
  );
  const services = new Set((r.data?.items ?? []).map((i) => i.service));
  return (
    <TopPanel title="Busiest routes" to={`/projects/${tenantId}/api`} link="API">
      <BreakdownList
        label="Busiest routes"
        state={r.isPending ? "loading" : r.error ? "error" : undefined}
        error={r.error}
        onRetry={() => r.refetch()}
        items={(r.data?.items ?? []).map((it) => {
          const text = `${it.method} ${it.route}`;
          return {
            key: `${it.service}|${it.method}|${it.route}`,
            text,
            label: <KeyWithTag text={text} tag={services.size > 1 ? it.service : undefined} />,
            value: it.requests,
            aside:
              it.rate_5xx === null ? null : (
                <span
                  className={cn("tabular", it.rate_5xx > 0 ? "text-critical" : "text-fg-3")}
                  data-rate-5xx=""
                >
                  {formatPercent(it.rate_5xx)}
                  <span className="sr-only"> 5xx</span>
                </span>
              ),
          };
        })}
        asideLabel="5xx"
        header={false}
        tone="series-3"
        valueLabel="Requests"
        format={formatCompact}
        emptyTitle="No API requests in this range"
      />
    </TopPanel>
  );
}
