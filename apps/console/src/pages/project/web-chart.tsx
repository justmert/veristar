import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { type WebQuery, type WebSummary, webHourlyQuery } from "@/api/web";
import { type TimeBucket, TimeChart } from "@/components/charts";
import { formatCompact, formatDate, formatDateTimeUtc, formatTick, utcDay } from "@/lib/format";
import {
  formatMetric,
  higherIsBetter,
  isSingleDay,
  pointValue,
  type WebMetric,
} from "@/lib/web-dashboard";

export const METRIC_LABEL: Record<WebMetric, string> = {
  pageviews: "Page views",
  visitors: "Unique visitors",
  visits: "Visits",
  bounce_rate: "Bounce rate",
  avg_visit_duration: "Visit duration",
};

// counts are bars; a rate or an average per day is a line
const LINE: readonly WebMetric[] = ["bounce_rate", "avg_visit_duration"];

// the one chart of the web numbers: per day from the daily totals, or per
// hour for a single day. web and the overview draw the same chart.
export function WebTrendChart({
  tenantId,
  q,
  summary,
  metric,
}: {
  tenantId: string;
  q: WebQuery;
  summary: WebSummary;
  metric: WebMetric;
}) {
  if (isSingleDay(q))
    return (
      <HourChart
        tenantId={tenantId}
        q={q}
        metric={metric === "visitors" ? "visitors" : "pageviews"}
      />
    );
  return <DayChart summary={summary} metric={metric} />;
}

function DayChart({ summary, metric }: { summary: WebSummary; metric: WebMetric }) {
  const today = utcDay();
  const buckets: TimeBucket[] = summary.current.series.map((p) => ({
    at: `${p.day}T00:00:00Z`,
    tick: formatTick(p.day, "day"),
    label: formatDate(p.day),
    // today is still counting, and a day not read yet is not a zero
    partial: p.day === today || !p.aggregated,
  }));
  const label = METRIC_LABEL[metric];
  const state = summary.current.totals.pageviews === 0 ? "empty" : "data";
  const fmt = (n: number) => formatMetric(metric, n);
  return (
    <TimeChart
      // a remount from empty to data gives the reveal observer the plot
      key={state}
      title={`${label} per day`}
      kind={LINE.includes(metric) ? "line" : "bar"}
      buckets={buckets}
      series={[
        {
          key: metric,
          label,
          tone: "series-1",
          values: summary.current.series.map((p) => pointValue(p, metric)),
        },
      ]}
      previous={summary.previous.series.map((p) => pointValue(p, metric))}
      format={fmt}
      axisFormat={LINE.includes(metric) ? fmt : formatCompact}
      higherIsBetter={higherIsBetter(metric)}
      state={state}
      emptyTitle="No page views in this range"
      emptyText="Page views show here within an hour after they happen."
      noCompleteTitle="No complete day yet"
      noCompleteText="Today is still counting. Its value shows here, hatched, as it grows."
    />
  );
}

function HourChart({
  tenantId,
  q,
  metric,
}: {
  tenantId: string;
  q: WebQuery;
  metric: "pageviews" | "visitors";
}) {
  const h = useQuery(webHourlyQuery(tenantId, q.from, q.site, q.band));
  const points = h.data?.points ?? [];
  const buckets: TimeBucket[] = useMemo(() => {
    const now = Date.now();
    return points.map((p) => ({
      at: p.at,
      tick: formatTick(p.at, "hour"),
      label: formatDateTimeUtc(p.at),
      // the running hour; later hours of today have not begun
      partial: Date.parse(p.at) <= now && Date.parse(p.at) + 3_600_000 > now,
    }));
  }, [points]);
  const total = points.reduce((a, p) => a + p.pageviews, 0);
  const state = h.isPending ? "loading" : h.error ? "error" : total === 0 ? "empty" : "data";
  const label = METRIC_LABEL[metric];
  return (
    <TimeChart
      key={state}
      title={`${label === "Unique visitors" ? "Visitors" : label} per hour`}
      buckets={buckets}
      series={[{ key: metric, label, tone: "series-1", values: points.map((p) => p[metric]) }]}
      state={state}
      error={h.error}
      onRetry={() => h.refetch()}
      emptyTitle="No page views on this day"
      emptyText="Page views show here a few minutes after they happen."
    />
  );
}
