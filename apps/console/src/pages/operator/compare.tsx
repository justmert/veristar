import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { apiCompareQuery, webCompareQuery } from "@/api/queries";
import type {
  ApiCompare,
  ApiCompareProject,
  ApiNumbers,
  Breakdown,
  TenantRef,
  WebCompare,
  WebDimension,
} from "@/api/schemas";
import type { Series } from "@/components/api-charts";
import { CompareChart, type CompareValue } from "@/components/compare-charts";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatRate, formatSeconds, OPEN_BUCKET } from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";
import {
  COMPARE_COLORS,
  compareSearch,
  countryName,
  type DayRange,
  formatDuration,
  formatPct,
  formatShare,
  readCompare,
} from "@/lib/global";
import { API_NOTES, SourceBadge } from "./global-api";
import {
  AGGREGATE_NOTE,
  Missing,
  Notes,
  QUARANTINE_NOTE,
  RangeControl,
  VISITORS_NOTE,
} from "./global-shared";

const DIMENSIONS: { dim: WebDimension; label: string }[] = [
  { dim: "referrer", label: "Referrers" },
  { dim: "country", label: "Countries" },
  { dim: "device", label: "Devices" },
  { dim: "browser", label: "Browsers" },
  { dim: "os", label: "Operating systems" },
];

function seriesOf(tenants: TenantRef[]): Series[] {
  return tenants.map((t, i) => ({
    key: t.id,
    label: t.name,
    color: COMPARE_COLORS[i] ?? "var(--chart-none)",
  }));
}

const compact = (v: number) =>
  new Intl.NumberFormat("en", { notation: v >= 10_000 ? "compact" : "standard" }).format(v);
const pctAxis = (v: number) => `${Math.round(v * 1000) / 10}%`;

// the side-by-side view of up to five projects (contract 8.4 and 8.6). with
// one project it is that project's aggregate drill-down.
export function ComparePage() {
  const [params, setParams] = useSearchParams();
  const { tenants, range } = readCompare(params);
  const setRange = (r: DayRange) => setParams(compareSearch(tenants, r), { replace: true });
  const web = useQuery({ ...webCompareQuery(tenants, range), enabled: tenants.length > 0 });
  const apiq = useQuery({ ...apiCompareQuery(tenants, range), enabled: tenants.length > 0 });

  if (tenants.length === 0) {
    return (
      <section className="space-y-3">
        <h2 className="text-xl font-semibold tracking-tight">Compare projects</h2>
        <p className="text-muted-foreground">
          No project is selected. Select up to five projects in{" "}
          <Link className="underline underline-offset-2" to="/operator/web">
            Global web
          </Link>{" "}
          or{" "}
          <Link className="underline underline-offset-2" to="/operator/api">
            Global API
          </Link>{" "}
          and choose Compare.
        </p>
      </section>
    );
  }

  const resolved = web.data ? { from: web.data.from, to: web.data.to } : null;
  const refs = web.data?.projects.map((p) => p.tenant) ?? apiq.data?.projects.map((p) => p.tenant);

  return (
    <section className="space-y-6">
      <header className="space-y-3">
        <Button asChild variant="ghost" size="sm" className="-ml-2">
          <Link to="/operator/web">
            <ArrowLeft aria-hidden />
            Global web
          </Link>
        </Button>
        <h2 className="text-xl font-semibold tracking-tight">
          {tenants.length === 1 ? "Project aggregates" : "Compare projects"}
        </h2>
        {refs ? (
          <ul aria-label="Compared projects" className="flex flex-wrap gap-2">
            {refs.map((t, i) => (
              <li
                key={t.id}
                className="inline-flex items-center gap-2 rounded-full border bg-card px-3 py-1 text-sm"
              >
                <svg aria-hidden width="10" height="10" className="shrink-0">
                  <rect width="10" height="10" rx="2" fill={COMPARE_COLORS[i]} />
                </svg>
                <span className="font-medium">{t.name}</span>
                <StatusPill status={t.status} />
              </li>
            ))}
          </ul>
        ) : null}
      </header>

      <div className="rounded-xl border bg-card p-4 sm:p-5">
        <RangeControl resolved={resolved} onChange={setRange} />
      </div>

      <section aria-labelledby="compare-web" className="space-y-4">
        <h3 id="compare-web" className="text-lg font-semibold tracking-tight">
          Web
        </h3>
        {web.isPending ? <Loading label="Loading web numbers" /> : null}
        {web.error ? <ErrorPanel error={web.error} onRetry={() => web.refetch()} /> : null}
        {web.data ? <WebSection data={web.data} /> : null}
      </section>

      <section aria-labelledby="compare-api" className="space-y-4">
        <h3 id="compare-api" className="text-lg font-semibold tracking-tight">
          API
        </h3>
        {apiq.isPending ? <Loading label="Loading API numbers" /> : null}
        {apiq.error ? <ErrorPanel error={apiq.error} onRetry={() => apiq.refetch()} /> : null}
        {apiq.data ? <ApiSection data={apiq.data} /> : null}
      </section>

      <Notes
        items={[
          VISITORS_NOTE,
          QUARANTINE_NOTE,
          "A day not read by the aggregation yet (for example before a backfill) is a gap in the web charts and shows as not read in the tables; a day read without traffic shows 0.",
          ...API_NOTES.filter((n) => n !== AGGREGATE_NOTE),
          AGGREGATE_NOTE,
        ]}
      >
        <DbIpAttribution className="mt-3" />
      </Notes>
    </section>
  );
}

// ---- web

function WebSection({ data }: { data: WebCompare }) {
  const series = seriesOf(data.projects.map((p) => p.tenant));
  const point = (s: number, d: number) => data.projects[s]?.series[d];
  const count = (k: "pageviews" | "visitors" | "visits") => (s: number, d: number) =>
    point(s, d)?.[k] ?? null;
  const fmtCount = (v: number | "open") => (v === "open" ? OPEN_BUCKET : formatCount(v));
  return (
    <>
      <WebTotalsTable data={data} />
      <div className="grid gap-4 lg:grid-cols-2">
        <CompareChart
          kind="count"
          title="Page views"
          description="Page views of the primary band per UTC day."
          days={data.days}
          series={series}
          value={count("pageviews")}
          format={fmtCount}
          axisFormat={compact}
          missingText="not read"
        />
        <CompareChart
          kind="count"
          title="Visitors"
          description="Visitors per UTC day, unique within the day and site."
          days={data.days}
          series={series}
          value={count("visitors")}
          format={fmtCount}
          axisFormat={compact}
          missingText="not read"
        />
        <CompareChart
          kind="share"
          title="Bounce rate"
          description="Visits with one page view and no event, over all visits. Gaps are days without visits."
          days={data.days}
          series={series}
          value={(s, d) => point(s, d)?.bounce_rate ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatShare(v) ?? "-"))}
          axisFormat={pctAxis}
        />
        <CompareChart
          kind="count"
          title="Average visit duration"
          description="Time between the first and last page view of a visit, averaged per day."
          days={data.days}
          series={series}
          value={(s, d) => point(s, d)?.avg_visit_duration ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatDuration(v) ?? "-"))}
          axisFormat={(v) => formatDuration(v) ?? ""}
        />
      </div>
      {DIMENSIONS.map((d) => (
        <BreakdownRow
          key={d.dim}
          label={d.label}
          dim={d.dim}
          projects={data.projects.map((p) => ({ tenant: p.tenant, b: p.breakdowns[d.dim] }))}
        />
      ))}
    </>
  );
}

function WebTotalsTable({ data }: { data: WebCompare }) {
  const rows: {
    label: string;
    value: (t: WebCompare["projects"][number]["totals"]) => string | null;
  }[] = [
    { label: "Page views", value: (t) => formatCount(t.pageviews) },
    { label: "Visitors", value: (t) => formatCount(t.visitors) },
    { label: "Visits", value: (t) => formatCount(t.visits) },
    { label: "Bounce rate", value: (t) => formatShare(t.bounce_rate) },
    { label: "Average visit duration", value: (t) => formatDuration(t.avg_visit_duration) },
    { label: "Quarantined page views", value: (t) => formatCount(t.quarantine_pageviews) },
    { label: "Quarantined share", value: (t) => formatShare(t.quarantine_share) },
  ];
  return (
    <TotalsTable
      label="Web totals"
      tenants={data.projects.map((p) => p.tenant)}
      rows={rows.map((r) => ({
        label: r.label,
        cells: data.projects.map((p) => r.value(p.totals)),
      }))}
    />
  );
}

// the range totals: one column per project, so the numbers sit side by side
function TotalsTable({
  label,
  tenants,
  rows,
}: {
  label: string;
  tenants: TenantRef[];
  rows: { label: string; cells: (string | null)[] }[];
}) {
  return (
    <section
      aria-label={label}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable region needs focus (wcag 2.1.1)
      tabIndex={0}
      className="relative overflow-x-auto rounded-xl border bg-card"
    >
      <table className="w-full text-left text-sm">
        <thead className="border-b bg-muted/50 text-xs text-muted-foreground">
          <tr>
            <th scope="col" className="px-4 py-2 font-medium sm:px-5">
              <span className="sr-only">Metric</span>
            </th>
            {tenants.map((t, i) => (
              <th key={t.id} scope="col" className="px-3 py-2 text-right font-medium">
                <span className="inline-flex items-center gap-1.5">
                  <svg aria-hidden width="8" height="8" className="shrink-0">
                    <rect width="8" height="8" rx="2" fill={COMPARE_COLORS[i]} />
                  </svg>
                  {t.name}
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y">
          {rows.map((r) => (
            <tr key={r.label} data-metric={r.label}>
              <th scope="row" className="px-4 py-2 font-normal whitespace-nowrap sm:px-5">
                {r.label}
              </th>
              {r.cells.map((c, i) => (
                <td key={i} className="tabular px-3 py-2 text-right whitespace-nowrap">
                  {c === null ? <Missing /> : c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function keyText(dim: WebDimension, key: string): string {
  return dim === "country" ? countryName(key) : key;
}

// one dimension across the projects: each project's top keys with a bar
// scaled to that project's largest value. values are umami's y, distinct
// visitors of a day whose page views carry the key, summed over the days.
function BreakdownRow({
  label,
  dim,
  projects,
}: {
  label: string;
  dim: WebDimension;
  projects: { tenant: TenantRef; b: Breakdown }[];
}) {
  return (
    <section aria-label={label} className="rounded-xl border bg-card p-4 sm:p-5">
      <h4 className="font-semibold">{label}</h4>
      <p className="mt-0.5 text-sm text-muted-foreground">
        Top values over the range, counted in daily visitors.
        {dim === "referrer"
          ? " Only other sites count; visits from a project's own pages do not."
          : ""}
      </p>
      <div className="mt-3 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {projects.map(({ tenant, b }, i) => {
          const max = Math.max(1, ...b.items.map((x) => x.value));
          return (
            <div key={tenant.id} className="min-w-0">
              <p className="flex items-center gap-1.5 text-sm font-medium">
                <svg aria-hidden width="8" height="8" className="shrink-0">
                  <rect width="8" height="8" rx="2" fill={COMPARE_COLORS[i]} />
                </svg>
                <span className="truncate">{tenant.name}</span>
              </p>
              {b.items.length === 0 ? (
                <p className="mt-1 text-sm text-muted-foreground">None in this range.</p>
              ) : (
                <ul className="mt-2 space-y-1.5" aria-label={`${label} of ${tenant.name}`}>
                  {b.items.map((x) => (
                    <li key={x.key} className="text-sm">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="min-w-0 truncate" title={keyText(dim, x.key)}>
                          {keyText(dim, x.key)}
                        </span>
                        <span className="tabular shrink-0 font-medium">{formatCount(x.value)}</span>
                      </div>
                      <div className="mt-0.5 h-1.5 rounded-full bg-muted">
                        <div
                          className="h-full rounded-full"
                          // widths and colours are data; react writes them through the cssom
                          style={{
                            width: `${(x.value / max) * 100}%`,
                            background: COMPARE_COLORS[i],
                          }}
                        />
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              {b.total_keys > b.items.length ? (
                <p className="mt-1.5 text-xs text-muted-foreground">
                  {formatCount(b.total_keys - b.items.length)} more
                </p>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}

// ---- api

function pctValue(seconds: number | null, over: boolean): CompareValue {
  if (over) return "open";
  return seconds;
}

function ApiSection({ data }: { data: ApiCompare }) {
  const series = seriesOf(data.projects.map((p) => p.tenant));
  const point = (s: number, d: number) => data.projects[s]?.series[d];
  const rate = (k: "rate_4xx" | "rate_5xx") => (s: number, d: number) => point(s, d)?.[k] ?? null;
  return (
    <>
      <ApiTotalsTable data={data} />
      <div className="grid gap-4 lg:grid-cols-2">
        <CompareChart
          kind="count"
          title="Requests"
          description="Requests per UTC day over every service."
          days={data.days}
          series={series}
          value={(s, d) => point(s, d)?.requests ?? null}
          format={(v) => (v === "open" ? OPEN_BUCKET : formatCount(v))}
          axisFormat={compact}
        />
        <CompareChart
          kind="measure"
          title="p95 latency"
          description="95th percentile of request duration per day, from summed histograms. Gaps are days without requests."
          days={data.days}
          series={series}
          value={(s, d) => {
            const p = point(s, d);
            return p ? pctValue(p.p95_seconds, p.p95_over_10s) : null;
          }}
          format={(v) => (v === "open" ? OPEN_BUCKET : formatSeconds(v))}
          axisFormat={formatSeconds}
        />
        <CompareChart
          kind="share"
          title="4xx rate"
          description="Share of requests answered with a 4xx status. Gaps are days without requests."
          days={data.days}
          series={series}
          value={rate("rate_4xx")}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatRate(v) ?? "-"))}
          axisFormat={pctAxis}
        />
        <CompareChart
          kind="share"
          title="5xx rate"
          description="Share of requests answered with a 5xx status. Gaps are days without requests."
          days={data.days}
          series={series}
          value={rate("rate_5xx")}
          format={(v) => (v === "open" ? OPEN_BUCKET : (formatRate(v) ?? "-"))}
          axisFormat={pctAxis}
        />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {data.projects.map((p, i) => (
          <ServicesCard key={p.tenant.id} project={p} color={COMPARE_COLORS[i] ?? ""} />
        ))}
      </div>
    </>
  );
}

function pctText(n: ApiNumbers, k: "p50" | "p95" | "p99"): string | null {
  return formatPct(n[`${k}_seconds`], n[`${k}_over_10s`]);
}

function ApiTotalsTable({ data }: { data: ApiCompare }) {
  const rows: { label: string; value: (t: ApiCompareProject["totals"]) => string | null }[] = [
    { label: "Requests", value: (t) => formatCount(t.requests) },
    { label: "4xx rate", value: (t) => formatRate(t.rate_4xx) },
    { label: "5xx rate", value: (t) => formatRate(t.rate_5xx) },
    { label: "p50", value: (t) => pctText(t, "p50") },
    { label: "p95", value: (t) => pctText(t, "p95") },
    { label: "p99", value: (t) => pctText(t, "p99") },
    { label: "Services", value: (t) => formatCount(t.services) },
    { label: "Routes", value: (t) => formatCount(t.routes) },
  ];
  return (
    <TotalsTable
      label="API totals"
      tenants={data.projects.map((p) => p.tenant)}
      rows={rows.map((r) => ({
        label: r.label,
        cells: data.projects.map((p) => r.value(p.totals)),
      }))}
    />
  );
}

function ServicesCard({ project, color }: { project: ApiCompareProject; color: string }) {
  const t = project.totals;
  return (
    <section
      aria-label={`Services of ${project.tenant.name}`}
      className="min-w-0 rounded-xl border bg-card p-4"
    >
      <p className="flex items-center gap-1.5 font-medium">
        <svg aria-hidden width="8" height="8" className="shrink-0">
          <rect width="8" height="8" rx="2" fill={color} />
        </svg>
        <span className="truncate">{project.tenant.name}</span>
      </p>
      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <SourceBadge source={t.source} spanServices={t.span_services} services={t.services} />
        {t.lossy ? <span>some latency buckets are approximate</span> : null}
      </div>
      {project.services.length === 0 ? (
        <p className="mt-2 text-sm text-muted-foreground">No requests in this range.</p>
      ) : (
        <ul className="mt-2 divide-y text-sm">
          {project.services.map((s) => (
            <li key={s.service} className="py-1.5">
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate font-mono text-xs">
                  {s.service === "" ? "(no service name)" : s.service}
                </span>
                <span className="tabular shrink-0 font-medium">{formatCount(s.requests)}</span>
              </div>
              <p className="text-xs text-muted-foreground">
                {formatCount(s.routes)} {s.routes === 1 ? "route" : "routes"}, 5xx{" "}
                {formatRate(s.rate_5xx) ?? "-"}, p95 {pctText(s, "p95") ?? "-"}
                {s.source === "span" ? ", from traces" : ""}
                {s.lossy ? ", approximate" : ""}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
