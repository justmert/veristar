import { useQueries, useQuery } from "@tanstack/react-query";
import { ArrowRight, ChevronRight, ExternalLink, Globe } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { type ApiFilters, webDashboardUrl } from "@/api/endpoints";
import { apiOverviewQuery, ingestMonthQuery, sitesQuery } from "@/api/queries";
import type { ApiOverview, Ingest, Site } from "@/api/schemas";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { PageHeader } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { StatTile, type Trend } from "@/components/stat-tile";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  readWebRange,
  useWebTraffic,
  WEB_RANGES,
  type WebRange,
  WebTraffic,
} from "@/components/web-charts";
import { formatPercentile, percentileChange, relativeChange } from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";
import { useProject } from "./project-layout";
import { AddSiteButton, firstEvent, SitesTable, setupDone, snippetPath } from "./sites";

export type HourSlot = { at: string; accepted: number; quarantined: number };

// one slot per utc hour from `from` up to `to`; the route omits empty hours,
// which are zero, not unknown
export function hourSlots(ingest: Ingest): HourSlot[] {
  const byAt = new Map(ingest.series.map((p) => [Date.parse(p.at), p.counts]));
  const out: HourSlot[] = [];
  const end = Date.parse(ingest.to);
  for (let t = Date.parse(ingest.from); t < end; t += 3600_000) {
    const c = byAt.get(t) ?? {};
    out.push({
      at: new Date(t).toISOString().replace(/\.\d{3}Z$/, "Z"),
      accepted: c.accepted ?? 0,
      quarantined: c.quarantined ?? 0,
    });
  }
  return out;
}

const NO_FILTERS: ApiFilters = { service: null, route: null, method: null };

// the project overview answers first: which site still needs setting up, or
// how the project is doing, then the numbers and the sites
export function OverviewPage() {
  const { tenant, termsCurrent } = useProject();
  const canChange = termsCurrent && tenant.status !== "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const [params, setParams] = useSearchParams();
  const range = readWebRange(params.get("range"));

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const live = sites.data.filter((s) => s.status !== "removed");
  const only = live.length === 1 ? live[0] : undefined;

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Overview"
        actions={
          live.length > 0 ? (
            <>
              <Segmented
                label="Range"
                value={range}
                options={WEB_RANGES}
                onChange={(r) => {
                  const next = new URLSearchParams(params);
                  next.set("range", r);
                  setParams(next, { replace: true });
                }}
                hint={range === "24h" ? "per hour" : "per day"}
              />
              {only ? (
                <Button asChild className="max-lg:h-(--vs-control-touch)">
                  <a href={webDashboardUrl(only.id, "primary")}>
                    Open web dashboard
                    <ExternalLink aria-hidden />
                  </a>
                </Button>
              ) : null}
            </>
          ) : null
        }
      />

      {live.length === 0 ? (
        <StateBlock
          kind="empty"
          icon={<Globe aria-hidden strokeWidth={1.75} />}
          title="No sites yet"
          action={canChange ? <AddSiteButton tenantId={tenant.id} variant="default" /> : undefined}
        >
          Add the website you want to measure. You get a script tag to paste into its pages.
        </StateBlock>
      ) : (
        <ProjectTraffic sites={live} range={range} canChange={canChange} tenantId={tenant.id} />
      )}

      <div className="flex flex-col gap-1">
        {live.length > 0 ? (
          <p className="text-caption font-normal text-fg-3">
            Counts are per UTC {range === "24h" ? "hour" : "day"} and update every few minutes.
          </p>
        ) : null}
        <DbIpAttribution />
      </div>
    </div>
  );
}

function ProjectTraffic({
  sites,
  range,
  canChange,
  tenantId,
}: {
  sites: Site[];
  range: WebRange;
  canChange: boolean;
  tenantId: string;
}) {
  const data = useWebTraffic(sites, range);
  // setup progress reads the last 30 days whatever the range shows
  const month = useQueries({ queries: sites.map((s) => ingestMonthQuery(s.id)) });
  const api = useQuery(apiOverviewQuery(tenantId, { range }, NO_FILTERS));
  const waiting = sites
    .map((s, i) => ({ site: s, answer: month[i]?.data }))
    .find((x) => x.answer && firstEvent(x.answer) !== "received");

  return (
    <>
      {waiting ? <SetupBanner site={waiting.site} answer={waiting.answer} /> : null}
      <WebTraffic
        data={data}
        range={range}
        label={range === "24h" ? "Last 24 hours" : "Last 30 days"}
        extra={
          api.data && usesApi(api.data) ? (
            <ApiTiles overview={api.data} range={range} tenantId={tenantId} />
          ) : null
        }
      />
      <SitesTable
        sites={sites}
        actions={canChange ? <AddSiteButton tenantId={tenantId} /> : null}
      />
    </>
  );
}

// the one thing to do next: a site whose tag has not sent a counted event
function SetupBanner({ site, answer }: { site: Site; answer: Ingest | undefined }) {
  const event = firstEvent(answer);
  const done = setupDone(site, event);
  return (
    <Link
      to={snippetPath(site)}
      className="group flex animate-rise-in flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-line bg-raised px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:border-line-strong hover:bg-hover focus-ring"
    >
      <span className="flex size-5 items-center justify-center">
        <StatusDot tone={event === "refused" ? "warning" : "accent"} live className="size-2" />
      </span>
      <span className="min-w-0 flex-1 text-body text-fg-2">
        <span className="font-medium text-fg">{site.name}</span>{" "}
        {event === "refused"
          ? "is sending events, but they are refused."
          : "is waiting for its first event."}{" "}
        Setup is {done} of 3 done.
      </span>
      <span className="flex items-center gap-1 text-body font-medium text-fg">
        Continue setup
        <ChevronRight
          aria-hidden
          className="size-4 transition-transform duration-(--vs-duration-press) group-hover:translate-x-0.5"
        />
      </span>
    </Link>
  );
}

function usesApi(o: ApiOverview): boolean {
  return (
    o.current.summary.requests > 0 ||
    o.previous.summary.requests > 0 ||
    o.current.sources.length > 0
  );
}

const PERIOD: Record<WebRange, string> = {
  "24h": "previous 24 hours",
  "30d": "previous 30 days",
};

function toTrend(
  c: { text: string; direction: "up" | "down" | "same" | null },
  range: WebRange,
  higherIsBetter: boolean,
): Trend | null {
  if (c.direction === null) return null;
  return {
    text: c.text.replace(/^[+-]/, ""),
    direction: c.direction === "same" ? "flat" : c.direction,
    good: c.direction === "same" ? null : (c.direction === "up") === higherIsBetter,
    period: PERIOD[range],
  };
}

// the api numbers of the same range; they do not drive the web chart, the
// api traffic page has their charts
function ApiTiles({
  overview,
  range,
  tenantId,
}: {
  overview: ApiOverview;
  range: WebRange;
  tenantId: string;
}) {
  const cur = overview.current.summary;
  const prev = overview.previous.summary;
  const link = (
    <Link
      to={`/projects/${tenantId}/api/traffic?range=${range}`}
      className="mt-1 inline-flex w-fit items-center gap-1 rounded-sm text-caption text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
    >
      API traffic
      <ArrowRight aria-hidden className="size-3" />
    </Link>
  );
  const p95 = formatPercentile(cur.p95);
  return (
    <>
      <StatTile
        label="API requests"
        value={formatCount(cur.requests)}
        numeric={cur.requests}
        format={formatCount}
        state={cur.requests === 0 ? "empty" : "data"}
        emptyText="No requests in this range"
        trend={toTrend(relativeChange(cur.requests, prev.requests), range, true)}
        footer={link}
      />
      <StatTile
        label="API p95"
        hint="95% of requests finished within this time"
        value={p95 ?? "n/a"}
        state={p95 === null ? "empty" : "data"}
        emptyText="No requests in this range"
        trend={toTrend(percentileChange(cur.p95, prev.p95), range, false)}
        footer={link}
      />
    </>
  );
}
