// the project overview answers two questions above the fold: how the
// project is doing (web and api numbers against the previous period, one
// chart) and what to do next (the one setup step that is open). every other
// answer is one line that links to its own page.

import { useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight, Check, Clock, Globe } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { ApiFilters } from "@/api/endpoints";
import { onchainQuery } from "@/api/onchain";
import { apiOverviewQuery, ingestMonthQuery, integrityQuery, sitesQuery } from "@/api/queries";
import type { ApiOverview, Site, TenantStatus } from "@/api/schemas";
import { type WebSummary, webSummaryQuery } from "@/api/web";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { PageHeader } from "@/components/page-header";
import { RangeControl } from "@/components/range-control";
import { StatCard } from "@/components/stat-card";
import { EmptyState, ErrorState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import {
  latencyText,
  percentileChange,
  rateChange,
  relativeChange,
  toStatChange,
} from "@/lib/api-metrics";
import { countOf, formatCount, formatPercent, utcDay } from "@/lib/format";
import { type DayRange, lastDays } from "@/lib/global";
import { verifiedActivity } from "@/lib/onchain";
import { apiRangeOfDays, type OverviewPreset, readOverviewRange } from "@/lib/overview";
import {
  combineFirstEvent,
  firstEvent,
  nextSetupStep,
  type SetupNext,
  siteSetupPath,
} from "@/lib/site-setup";
import {
  dayCount,
  formatMetric,
  metricValue,
  periodLabel,
  WEB_MAX_DAYS,
  WEB_PRESETS,
  type WebMetric,
  webPath,
  webTrend,
} from "@/lib/web-dashboard";
import { AddSiteButton, canAddSites } from "@/pages/project/sites";
import { useProject } from "@/pages/project-layout";
import { OverviewSkeleton, PAGE_HEADERS } from "./page-frame";
import { METRIC_LABEL, WebTrendChart } from "./web-chart";

const NO_FILTERS: ApiFilters = { service: null, route: null, method: null };
const CADENCE = "Web totals refresh every hour. API counts refresh every few minutes.";

// a project that is not approved, is suspended or was removed shows no setup
// step: no event can arrive. the status notice above the page says why.
const STOPPED: ReadonlySet<TenantStatus> = new Set(["rejected", "suspended", "removed"]);

function noSiteText(status: TenantStatus, isOwner: boolean, termsCurrent: boolean): string {
  if (!termsCurrent) return "An owner must accept the new terms before you can add sites.";
  switch (status) {
    case "rejected":
      return isOwner
        ? "Sites that you add now wait until operators approve the project. Change the answers above and submit them again."
        : "Sites that you add now wait until operators approve the project. An owner can change the answers and submit them again.";
    case "suspended":
      return "You cannot add sites while the project is suspended.";
    case "removed":
      return "This project was removed. It cannot take new sites.";
    default:
      return "Add the website that you want to measure. You get a script tag for its pages.";
  }
}

export function OverviewPage() {
  const { tenant, termsCurrent, isOwner } = useProject();
  const stopped = STOPPED.has(tenant.status);
  const canAdd = canAddSites(tenant.status, termsCurrent);
  const sites = useQuery(sitesQuery(tenant.id));
  const [params, setParams] = useSearchParams();
  const { preset, days } = readOverviewRange(params, new Date());
  const live = sites.data?.filter((s) => s.status !== "removed") ?? [];
  const summary = useQuery({
    ...webSummaryQuery(tenant.id, { ...days, site: null, band: "primary" }),
    enabled: live.length > 0,
  });

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
  else if (live.length === 0)
    // a project without a site can still declare on-chain ids: the
    // summaries stay, only the web numbers wait for a site
    body = (
      <>
        {!stopped && canAdd ? (
          <SetupChecklist tenantId={tenant.id} sites={live} days={days} canAdd={canAdd} />
        ) : (
          <EmptyState
            icon={<Globe aria-hidden strokeWidth={1.75} />}
            title="No sites yet"
            // the resubmit form above holds the page's main action
            action={canAdd ? <AddSiteButton tenantId={tenant.id} variant="outline" /> : undefined}
          >
            {noSiteText(tenant.status, isOwner, termsCurrent)}
          </EmptyState>
        )}
        <Summaries tenantId={tenant.id} sites={live} days={days} />
      </>
    );
  else
    body = (
      <>
        {stopped ? null : (
          <SetupChecklist tenantId={tenant.id} sites={live} days={days} canAdd={canAdd} />
        )}
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
        />
        <Numbers
          tenantId={tenant.id}
          days={days}
          summary={summary.data ?? null}
          summaryState={summary.isPending ? "loading" : summary.error ? "error" : "data"}
          summaryError={summary.error}
          onRetry={() => summary.refetch()}
          only={live.length === 1 ? live[0] : undefined}
        />
        <Summaries tenantId={tenant.id} sites={live} days={days} />
      </>
    );

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.overview}
        freshness={summary.data?.read_at ? { at: summary.data.read_at, cadence: CADENCE } : null}
      />
      {body}
      <DbIpAttribution />
    </div>
  );
}
// ---- the setup checklist and its next site step: one line, one action

const STEP_TEXT: Record<SetupNext["step"], { text: string; action: string }> = {
  install: {
    text: "has no page view yet. Add the tracker to its pages.",
    action: "Continue setup",
  },
  refused: { text: "sends events, but they are refused.", action: "See why" },
  verify: { text: "counts page views. Verify its domain.", action: "Verify domain" },
};

// the setup step of the first site that is not set up, read from the edge
// counts and, while those have no event, the web totals. loading is true
// while an answer is missing, so no step jumps.
function useNextStep(sites: Site[], tenantId: string) {
  const range = lastDays(30, new Date());
  const active = sites.filter((s) => s.status === "active" && !s.in_review);
  const ingest = useQueries({ queries: active.map((s) => ingestMonthQuery(s.id)) });
  const fromEdge = active.map((_, i) => {
    const d = ingest[i]?.data;
    return d ? firstEvent(d) : null;
  });
  // the edge counts lag by minutes, the web totals by an hour: either one
  // with a counted event completes the step (lib/site-setup.ts). the web
  // totals of a site are asked only when the edge has none.
  const unsure = active.filter((_, i) => fromEdge[i] !== null && fromEdge[i] !== "received");
  const web = useQueries({
    queries: unsure.map((s) => ({
      ...webSummaryQuery(tenantId, { ...range, site: s.id, band: "primary" as const }),
      placeholderData: undefined,
    })),
  });
  const step = nextSetupStep(active, (s) => {
    const i = active.indexOf(s);
    const j = unsure.indexOf(s);
    if (j < 0) return fromEdge[i] ?? null;
    const w = web[j];
    if (w?.isPending) return null;
    const views = w?.data?.current.totals.pageviews;
    return combineFirstEvent(fromEdge[i] ?? null, views === undefined ? null : views > 0);
  });
  const loading = ingest.some((q) => q.isPending) || web.some((q) => q.isPending);
  return { active, step, loading };
}

type CheckState = "done" | "current" | "todo" | "waiting";

// the project's setup as one checklist at the top of the overview: a site,
// its tracker and domain, then the api or an on-chain account. it goes away
// when every step is done. a step is current only when the one before it is
// done, so the card offers one action at a time.
function SetupChecklist({
  tenantId,
  sites,
  days,
  canAdd,
}: {
  tenantId: string;
  sites: Site[];
  days: DayRange;
  canAdd: boolean;
}) {
  const next = useNextStep(sites, tenantId);
  const api = useQuery(apiOverviewQuery(tenantId, apiRangeOfDays(days), NO_FILTERS));
  const chain = useQuery(onchainQuery(tenantId, days));
  const hasSite = sites.length > 0;
  const siteDone = hasSite && next.active.length > 0 && !next.loading && next.step === null;
  const dataDone =
    (api.data ? usesApi(api.data) : false) || (chain.data ? chain.data.totals.ids > 0 : false);
  const dataKnown = !api.isPending && !chain.isPending;
  // a project with a site waits for every answer, so a set-up project
  // never shows the card for a moment
  if (hasSite && (next.loading || !dataKnown)) return null;
  if (siteDone && dataDone) return null;

  const states: CheckState[] = [
    hasSite ? "done" : "current",
    !hasSite ? "todo" : next.active.length === 0 ? "waiting" : siteDone ? "done" : "current",
    dataDone ? "done" : hasSite && (siteDone || next.active.length === 0) ? "current" : "todo",
  ];
  const done = states.filter((x) => x === "done").length;
  const step = next.step;
  const t = step ? STEP_TEXT[step.step] : null;
  return (
    <section
      aria-labelledby="setup-h"
      data-setup
      className="flex animate-rise-in flex-col gap-3 rounded-lg border border-line bg-raised p-5 max-sm:p-4"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h2 id="setup-h" className="text-title text-fg">
          Set up the project
        </h2>
        <span className="text-caption text-fg-2">{done} of 3 done</span>
      </div>
      <ol className="flex flex-col divide-y divide-line-subtle">
        <CheckItem
          n={1}
          state={states[0] as CheckState}
          title="Add a site"
          text="Name the website that you want to measure."
          action={
            states[0] === "current" && canAdd ? (
              <AddSiteButton tenantId={tenantId} variant="default" />
            ) : null
          }
        />
        <CheckItem
          n={2}
          state={states[1] as CheckState}
          title="Install the tracker and verify the domain"
          text={
            states[1] === "waiting"
              ? "Operators review the site first. Then install its tracker."
              : "Add the tag to the pages of each site, then prove that you own the domain."
          }
        >
          {states[1] === "current" && step && t ? (
            <section
              aria-label="Next step"
              data-step={step.step}
              className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3"
            >
              <p className="min-w-0 flex-1 text-small text-fg-2">
                <span className="font-medium text-fg">{step.site.name}</span> {t.text}
              </p>
              <Link
                to={siteSetupPath(step.site)}
                className="group inline-flex w-fit items-center gap-1 rounded-sm text-body font-medium text-fg hover:underline focus-ring max-lg:min-h-(--vs-control-touch)"
              >
                {t.action}
                <ArrowRight
                  aria-hidden
                  className="size-4 transition-transform duration-(--vs-duration-press) group-hover:translate-x-0.5"
                />
              </Link>
            </section>
          ) : null}
        </CheckItem>
        <CheckItem
          n={3}
          state={states[2] as CheckState}
          title="Connect your API or an on-chain account"
          text="Send API metrics from your services, or add a Stellar account or contract."
          action={
            states[2] === "current" ? (
              <span className="flex flex-wrap gap-x-6 gap-y-2">
                <PageLink to={`/projects/${tenantId}/connect-api`}>Connect API</PageLink>
                <PageLink to={`/projects/${tenantId}/onchain`}>Add an account</PageLink>
              </span>
            ) : null
          }
        />
      </ol>
    </section>
  );
}

function CheckItem({
  n,
  state,
  title,
  text,
  action,
  children,
}: {
  n: number;
  state: CheckState;
  title: string;
  text: string;
  action?: ReactNode;
  children?: ReactNode;
}) {
  const word =
    state === "done"
      ? "Done"
      : state === "current"
        ? "Now"
        : state === "waiting"
          ? "Waiting"
          : "Later";
  return (
    <li
      data-check={n}
      data-state={state}
      className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0"
    >
      <span
        aria-hidden
        className={cn(
          "mt-px flex size-6 items-center justify-center rounded-full border text-caption tabular",
          state === "done"
            ? "border-success-line text-success"
            : state === "current"
              ? "border-fg text-fg"
              : "border-line text-fg-3",
        )}
      >
        {state === "done" ? (
          <Check className="size-3.5" />
        ) : state === "waiting" ? (
          <Clock className="size-3.5" />
        ) : (
          n
        )}
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <p className={cn("text-body", state === "done" ? "text-fg-2" : "text-fg")}>
          <span className="sr-only">{word}: </span>
          {title}
        </p>
        {state === "done" ? null : <p className="text-small text-fg-2">{text}</p>}
      </div>
      {state === "current" && (action || children) ? (
        <div className="col-start-2 flex min-w-0 flex-col gap-2">
          {children}
          {action}
        </div>
      ) : null}
    </li>
  );
}

// ---- the numbers: web from the web totals, api from the api counts

const WEB_CARDS: { metric: WebMetric; definition: string }[] = [
  { metric: "pageviews", definition: "Pages loaded in a browser." },
  { metric: "visitors", definition: "Distinct visitors per UTC day, added up over the range." },
  { metric: "bounce_rate", definition: "Visits with one page view and no custom event." },
];

function Numbers({
  tenantId,
  days,
  summary,
  summaryState,
  summaryError,
  onRetry,
  only,
}: {
  tenantId: string;
  days: DayRange;
  summary: WebSummary | null;
  summaryState: "loading" | "error" | "data";
  summaryError: unknown;
  onRetry: () => void;
  only: Site | undefined;
}) {
  const [metric, setMetric] = useState<WebMetric>("pageviews");
  const api = useQuery(apiOverviewQuery(tenantId, apiRangeOfDays(days), NO_FILTERS));
  const single = days.from === days.to;
  const chartable = (m: WebMetric) => !single || m === "pageviews" || m === "visitors";
  const shown: WebMetric = chartable(metric) ? metric : "pageviews";

  return (
    <div className="flex flex-col gap-4">
      {/* the numbers name their pages in their own header, not in a loose
          row of links under the cards */}
      <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
        <h2 className="text-caption text-fg-2">Web and API</h2>
        <span className="flex flex-wrap gap-x-6 gap-y-1">
          <PageLink to={webPath(tenantId, only?.id)}>Web</PageLink>
          <PageLink to={`/projects/${tenantId}/api`}>API</PageLink>
        </span>
      </div>
      <section
        aria-label="Numbers"
        className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6"
      >
        {WEB_CARDS.map((c) => {
          const v = summary ? metricValue(summary.current.totals, c.metric) : null;
          return (
            <StatCard
              key={c.metric}
              data-metric={c.metric}
              label={METRIC_LABEL[c.metric]}
              definition={c.definition}
              value={v}
              format={(n) => formatMetric(c.metric, n)}
              missingReason="No visits in this range"
              state={summaryState}
              change={
                summary
                  ? webTrend(
                      c.metric,
                      v,
                      metricValue(summary.previous.totals, c.metric),
                      summary.current.range.days,
                    )
                  : null
              }
              selected={chartable(c.metric) ? shown === c.metric : undefined}
              onSelect={chartable(c.metric) ? () => setMetric(c.metric) : undefined}
            />
          );
        })}
        <ApiCards
          tenantId={tenantId}
          overview={api.data ?? null}
          state={api.isPending ? "loading" : api.error ? "error" : "data"}
          period={periodLabel(dayCount(days))}
        />
      </section>

      {summaryState === "error" ? (
        <ErrorState title="The numbers did not load" error={summaryError} onRetry={onRetry} />
      ) : summary ? (
        <WebTrendChart
          tenantId={tenantId}
          q={{ ...days, site: null, band: "primary" }}
          summary={summary}
          metric={shown}
        />
      ) : (
        <Skeleton.Chart />
      )}
    </div>
  );
}

function usesApi(o: ApiOverview): boolean {
  return (
    o.current.summary.requests > 0 ||
    o.previous.summary.requests > 0 ||
    o.current.sources.length > 0
  );
}

function ApiCards({
  tenantId,
  overview,
  state,
  period,
}: {
  tenantId: string;
  overview: ApiOverview | null;
  state: "loading" | "error" | "data";
  period: string;
}) {
  if (overview && !usesApi(overview))
    return (
      <div
        data-api="none"
        className="flex flex-col justify-between gap-2 rounded-lg border border-dashed border-line-strong p-4 md:col-span-3"
      >
        <span className="text-caption text-fg-2">API</span>
        <p className="text-body text-fg-2">No API requests in this range.</p>
        <PageLink to={`/projects/${tenantId}/connect-api`}>Connect API</PageLink>
      </div>
    );
  const cur = overview?.current.summary;
  const prev = overview?.previous.summary;
  const change = (c: Parameters<typeof toStatChange>[0], upIsBad: boolean | null) =>
    cur && prev ? toStatChange(c, upIsBad, period) : null;
  return (
    <>
      <StatCard
        data-tile="API requests"
        label="API requests"
        definition="Requests that your services answered. Health checks and preflights are not included."
        value={cur ? cur.requests : null}
        state={state}
        change={cur && prev ? change(relativeChange(cur.requests, prev.requests), null) : null}
      />
      <StatCard
        data-tile="API 5xx rate"
        label="API 5xx rate"
        definition="Share of requests answered with a 5xx status."
        value={cur ? cur.rate_5xx : null}
        format={formatPercent}
        missingReason="No API requests in this range"
        state={state}
        change={cur && prev ? change(rateChange(cur.rate_5xx, prev.rate_5xx), true) : null}
      />
      <StatCard
        data-tile="API p95"
        label="API p95 latency"
        definition="95 of 100 requests were faster than this."
        value={cur ? latencyText(cur.p95) : null}
        missingReason="No API requests in this range"
        state={state}
        change={cur && prev ? change(percentileChange(cur.p95, prev.p95), true) : null}
      />
    </>
  );
}

function PageLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      to={to}
      className="group inline-flex w-fit items-center gap-1 rounded-sm text-small font-medium text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch)"
    >
      {children}
      <ArrowRight
        aria-hidden
        className="size-3.5 transition-transform duration-(--vs-duration-press) group-hover:translate-x-0.5"
      />
    </Link>
  );
}

// ---- one line each for sites, the grade and on-chain, linking to their pages

function Summaries({ tenantId, sites, days }: { tenantId: string; sites: Site[]; days: DayRange }) {
  const grade = useQuery(integrityQuery("tenant", tenantId, days));
  const chain = useQuery(onchainQuery(tenantId, days));
  // verified ids only, as On-chain and SCF review count them
  const activity = chain.data ? verifiedActivity(chain.data) : null;
  const unverified = sites.filter((s) => s.status === "active" && s.verification !== "verified");
  const inReview = sites.filter((s) => s.in_review || s.status === "pending");
  return (
    <section aria-label="Project" className="grid gap-3 md:grid-cols-3">
      <SummaryCard title="Sites" to={`/projects/${tenantId}/sites`} link="Sites">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="text-title text-fg tabular">{formatCount(sites.length)}</span>
          <span className="text-small text-fg-2">
            {sites.length === 0
              ? "No site added"
              : inReview.length > 0
                ? `${formatCount(inReview.length)} in review`
                : unverified.length > 0
                  ? `${countOf(unverified.length, "domain")} not verified`
                  : "All domains verified"}
          </span>
        </span>
      </SummaryCard>
      <SummaryCard title="Integrity grade" to={`/projects/${tenantId}/scf`} link="SCF review">
        {grade.isPending ? (
          <Bone className="h-6 w-32" />
        ) : grade.error ? (
          <span className="text-small text-fg-2">The grade did not load.</span>
        ) : (
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge kind="grade" grade={grade.data.grade} />
            <span className="text-small text-fg-2">
              {sites.length === 0
                ? "No site to grade"
                : grade.data.graded_days === 0
                  ? "Too few visits for a grade"
                  : `Graded on ${countOf(grade.data.graded_days, "site day")}`}
            </span>
          </span>
        )}
      </SummaryCard>
      <SummaryCard title="On-chain" to={`/projects/${tenantId}/onchain`} link="On-chain">
        {chain.isPending ? (
          <Bone className="h-6 w-32" />
        ) : chain.error ? (
          <span className="text-small text-fg-2">On-chain activity did not load.</span>
        ) : chain.data.totals.ids === 0 ? (
          <span className="text-small text-fg-2">No accounts or contracts declared</span>
        ) : activity?.gap ? (
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-mono text-title text-fg tabular">
              <Missing reason={activity.gap} />
            </span>
            <span className="text-small text-fg-2">
              operations, {formatCount(activity.verified)} of {formatCount(chain.data.totals.ids)}{" "}
              verified
            </span>
          </span>
        ) : (
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="text-title text-fg tabular">
              {formatCount(activity?.operations ?? 0)}
            </span>
            <span className="text-small text-fg-2">
              operations of verified ids, {formatCount(activity?.verified ?? 0)} of{" "}
              {formatCount(chain.data.totals.ids)} verified
            </span>
          </span>
        )}
      </SummaryCard>
    </section>
  );
}

function SummaryCard({
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
    <section
      aria-label={title}
      data-summary={title}
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4"
    >
      <h2 className="text-caption text-fg-2">{title}</h2>
      <div className="flex min-h-8 items-center">{children}</div>
      <PageLink to={to}>{link}</PageLink>
    </section>
  );
}
