// charts of the api traffic page, on recharts. colours are css variables
// (index.css), so dark mode switches with the rest of the console; react
// sets every style through the cssom, which the csp's style-src allows.
// every chart has a table view, so no value depends on hovering or on colour.

import { type ReactNode, useId } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { ApiPoint, ApiRange, ApiStatus, StatusClass } from "@/api/schemas";
import {
  formatBucket,
  formatPercentile,
  formatRate,
  formatSeconds,
  OPEN_BUCKET,
  OPEN_BUCKET_CEILING,
  plotPercentile,
  tickFormatter,
} from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";

const HEIGHT = 220;
const MARGIN = { top: 8, right: 8, bottom: 0, left: 0 };
const AXIS = { fontSize: 12, fill: "var(--muted-foreground)" };

export type Series = { key: string; label: string; color: string };

type Row = Record<string, number | string | null> & { at: string };

// the readout of every series at one bucket: value first, name second, a
// short stroke of the series colour as the key
function Readout({
  active,
  label,
  rows,
  range,
}: {
  active?: boolean;
  label?: string | number;
  rows: { label: string; color: string; value: string }[];
  range: ApiRange;
}) {
  if (!active || typeof label !== "string") return null;
  return (
    <div className="min-w-40 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
      <p className="mb-1.5 text-muted-foreground">{formatBucket(label, range)}</p>
      <ul className="space-y-1">
        {rows.map((r) => (
          <li key={r.label} className="flex items-center gap-2">
            <svg aria-hidden width="12" height="4" className="shrink-0">
              <line x1="0" x2="12" y1="2" y2="2" stroke={r.color} strokeWidth="2" />
            </svg>
            <span className="tabular font-semibold">{r.value}</span>
            <span className="text-muted-foreground">{r.label}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function Legend({ series, shape = "line" }: { series: Series[]; shape?: "line" | "box" }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {series.map((s) => (
        <li key={s.key} className="inline-flex items-center gap-1.5">
          {shape === "line" ? (
            <svg aria-hidden width="14" height="4">
              <line x1="0" x2="14" y1="2" y2="2" stroke={s.color} strokeWidth="2" />
            </svg>
          ) : (
            <svg aria-hidden width="10" height="10">
              <rect width="10" height="10" rx="2" fill={s.color} />
            </svg>
          )}
          {s.label}
        </li>
      ))}
    </ul>
  );
}

// a chart card: title, optional legend, the plot and its table
export function ChartCard({
  title,
  description,
  legend,
  table,
  dim,
  children,
}: {
  title: string;
  description: ReactNode;
  legend?: ReactNode;
  table: ReactNode;
  dim: boolean;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="min-w-0 rounded-xl border bg-card p-4 sm:p-5">
      <h3 id={id} className="font-semibold">
        {title}
      </h3>
      <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>
      {legend ? <div className="mt-3">{legend}</div> : null}
      <div className={`mt-3 transition-opacity ${dim ? "opacity-60" : ""}`}>{children}</div>
      <details className="mt-3 text-sm">
        <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
          Show as table
        </summary>
        <div className="relative mt-2 max-h-80 overflow-auto rounded-lg border">{table}</div>
      </details>
    </section>
  );
}

function SeriesTable({
  range,
  points,
  columns,
}: {
  range: ApiRange;
  points: ApiPoint[];
  columns: { label: string; value: (p: ApiPoint) => string }[];
}) {
  return (
    <table className="w-full text-left text-sm">
      <thead className="sticky top-0 bg-muted text-xs text-muted-foreground">
        <tr>
          <th scope="col" className="px-3 py-2 font-medium">
            Time
          </th>
          {columns.map((c) => (
            <th key={c.label} scope="col" className="px-3 py-2 text-right font-medium">
              {c.label}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="divide-y">
        {points.map((p) => (
          <tr key={p.at}>
            <th scope="row" className="px-3 py-1.5 font-normal whitespace-nowrap">
              {formatBucket(p.at, range)}
            </th>
            {columns.map((c) => (
              <td key={c.label} className="tabular px-3 py-1.5 text-right whitespace-nowrap">
                {c.value(p)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const dash = (v: string | null) => v ?? "-";

function compact(n: number): string {
  return new Intl.NumberFormat("en", { notation: n >= 10_000 ? "compact" : "standard" }).format(n);
}

export function VolumeChart({
  range,
  points,
  dim,
}: {
  range: ApiRange;
  points: ApiPoint[];
  dim: boolean;
}) {
  const tick = tickFormatter(range);
  const byAt = new Map(points.map((p) => [p.at, p]));
  return (
    <ChartCard
      title="Requests"
      description="Every request your services handled in each bucket."
      dim={dim}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[{ label: "Requests", value: (p) => formatCount(p.requests) }]}
        />
      }
    >
      <BarChart
        responsive
        width="100%"
        height={HEIGHT}
        data={points}
        margin={MARGIN}
        barCategoryGap="12%"
        title="Requests per bucket"
      >
        <CartesianGrid vertical={false} stroke="var(--chart-grid)" />
        <XAxis
          dataKey="at"
          tickFormatter={tick}
          tick={AXIS}
          tickLine={false}
          axisLine={{ stroke: "var(--border)" }}
          minTickGap={24}
        />
        <YAxis
          tickFormatter={compact}
          tick={AXIS}
          tickLine={false}
          axisLine={false}
          allowDecimals={false}
          width={44}
        />
        <Tooltip
          cursor={{ fill: "var(--muted)" }}
          isAnimationActive={false}
          content={({ active, label }) => {
            const p = typeof label === "string" ? byAt.get(label) : undefined;
            return (
              <Readout
                active={active && p !== undefined}
                label={label}
                range={range}
                rows={
                  p
                    ? [
                        {
                          label: "requests",
                          color: "var(--chart-1)",
                          value: formatCount(p.requests),
                        },
                      ]
                    : []
                }
              />
            );
          }}
        />
        <Bar
          dataKey="requests"
          name="Requests"
          fill="var(--chart-1)"
          radius={[4, 4, 0, 0]}
          maxBarSize={24}
          isAnimationActive={false}
        />
      </BarChart>
    </ChartCard>
  );
}

const ERROR_SERIES: Series[] = [
  { key: "rate4xx", label: "4xx rate", color: "var(--chart-4xx)" },
  { key: "rate5xx", label: "5xx rate", color: "var(--chart-5xx)" },
];

export function ErrorChart({
  range,
  points,
  dim,
}: {
  range: ApiRange;
  points: ApiPoint[];
  dim: boolean;
}) {
  const tick = tickFormatter(range);
  // percent on the axis; a bucket without requests has no rate and leaves a gap
  const rows: Row[] = points.map((p) => ({
    at: p.at,
    rate4xx: p.rate_4xx === null ? null : p.rate_4xx * 100,
    rate5xx: p.rate_5xx === null ? null : p.rate_5xx * 100,
  }));
  const byAt = new Map(points.map((p) => [p.at, p]));
  return (
    <ChartCard
      title="Error rates"
      description="Share of requests answered with a 4xx or a 5xx status. Gaps are buckets without requests."
      legend={<Legend series={ERROR_SERIES} />}
      dim={dim}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[
            { label: "4xx", value: (p) => formatCount(p.errors_4xx) },
            { label: "4xx rate", value: (p) => dash(formatRate(p.rate_4xx)) },
            { label: "5xx", value: (p) => formatCount(p.errors_5xx) },
            { label: "5xx rate", value: (p) => dash(formatRate(p.rate_5xx)) },
          ]}
        />
      }
    >
      <LineChart
        responsive
        width="100%"
        height={HEIGHT}
        data={rows}
        margin={MARGIN}
        title="Error rates"
      >
        <CartesianGrid vertical={false} stroke="var(--chart-grid)" />
        <XAxis
          dataKey="at"
          tickFormatter={tick}
          tick={AXIS}
          tickLine={false}
          axisLine={{ stroke: "var(--border)" }}
          minTickGap={24}
        />
        <YAxis
          tickFormatter={(v: number) => `${v}%`}
          tick={AXIS}
          tickLine={false}
          axisLine={false}
          width={44}
          domain={[0, "auto"]}
        />
        <Tooltip
          cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
          isAnimationActive={false}
          content={({ active, label }) => {
            const p = typeof label === "string" ? byAt.get(label) : undefined;
            return (
              <Readout
                active={active && p !== undefined}
                label={label}
                range={range}
                rows={
                  p
                    ? [
                        {
                          label: `4xx (${formatCount(p.errors_4xx)})`,
                          color: "var(--chart-4xx)",
                          value: dash(formatRate(p.rate_4xx)),
                        },
                        {
                          label: `5xx (${formatCount(p.errors_5xx)})`,
                          color: "var(--chart-5xx)",
                          value: dash(formatRate(p.rate_5xx)),
                        },
                      ]
                    : []
                }
              />
            );
          }}
        />
        {ERROR_SERIES.map((s) => (
          <Line
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stroke={s.color}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
            isAnimationActive={false}
            connectNulls={false}
            type="linear"
          />
        ))}
      </LineChart>
    </ChartCard>
  );
}

const LATENCY_SERIES: Series[] = [
  { key: "p50", label: "p50", color: "var(--chart-1)" },
  { key: "p95", label: "p95", color: "var(--chart-2)" },
  { key: "p99", label: "p99", color: "var(--chart-3)" },
];

type DotProps = { cx?: number; cy?: number; payload?: Row; index?: number };

// a point in the open bucket sits on the 10 s line as a ring, so it reads as
// "somewhere above", not as a measured 10 s
function openDot(key: string, color: string) {
  return function OpenDot({ cx, cy, payload, index }: DotProps) {
    if (cx === undefined || cy === undefined || !payload || payload[`${key}Open`] !== 1)
      return <g key={`${key}-${index}`} />;
    return (
      <circle
        key={`${key}-${index}`}
        cx={cx}
        cy={cy}
        r={4}
        fill="var(--card)"
        stroke={color}
        strokeWidth={2}
      />
    );
  };
}

export function LatencyChart({
  range,
  points,
  dim,
}: {
  range: ApiRange;
  points: ApiPoint[];
  dim: boolean;
}) {
  const tick = tickFormatter(range);
  const rows: Row[] = points.map((p) => ({
    at: p.at,
    p50: plotPercentile(p.p50),
    p95: plotPercentile(p.p95),
    p99: plotPercentile(p.p99),
    p50Open: p.p50 === OPEN_BUCKET ? 1 : 0,
    p95Open: p.p95 === OPEN_BUCKET ? 1 : 0,
    p99Open: p.p99 === OPEN_BUCKET ? 1 : 0,
  }));
  const anyOpen = points.some(
    (p) => p.p50 === OPEN_BUCKET || p.p95 === OPEN_BUCKET || p.p99 === OPEN_BUCKET,
  );
  const anyLossy = points.some((p) => p.lossy);
  const byAt = new Map(points.map((p) => [p.at, p]));
  return (
    <ChartCard
      title="Latency"
      description={
        <>
          Percentiles of request duration in each bucket, from summed histograms.
          {anyOpen ? " Rings on the 10 s line are above 10 s, the last bucket bound." : ""}
          {anyLossy ? " Some buckets are approximate (marked in the table)." : ""}
        </>
      }
      legend={<Legend series={LATENCY_SERIES} />}
      dim={dim}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[
            ...LATENCY_SERIES.map((s) => ({
              label: s.label,
              value: (p: ApiPoint) => dash(formatPercentile(p[s.key as "p50" | "p95" | "p99"])),
            })),
            { label: "Approximate", value: (p: ApiPoint) => (p.lossy ? "yes" : "no") },
          ]}
        />
      }
    >
      <LineChart
        responsive
        width="100%"
        height={HEIGHT}
        data={rows}
        margin={MARGIN}
        title="Latency"
      >
        <CartesianGrid vertical={false} stroke="var(--chart-grid)" />
        <XAxis
          dataKey="at"
          tickFormatter={tick}
          tick={AXIS}
          tickLine={false}
          axisLine={{ stroke: "var(--border)" }}
          minTickGap={24}
        />
        <YAxis
          tickFormatter={(v: number) => formatSeconds(v)}
          tick={AXIS}
          tickLine={false}
          axisLine={false}
          width={56}
          domain={[0, anyOpen ? OPEN_BUCKET_CEILING : "auto"]}
        />
        {anyOpen ? (
          <ReferenceLine
            y={OPEN_BUCKET_CEILING}
            stroke="var(--muted-foreground)"
            strokeDasharray="0"
            label={{
              value: ">10 s above",
              position: "insideTopLeft",
              fontSize: 11,
              fill: "var(--muted-foreground)",
            }}
          />
        ) : null}
        <Tooltip
          cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
          isAnimationActive={false}
          content={({ active, label }) => {
            const p = typeof label === "string" ? byAt.get(label) : undefined;
            return (
              <Readout
                active={active && p !== undefined}
                label={label}
                range={range}
                rows={
                  p
                    ? LATENCY_SERIES.map((s) => ({
                        label: p.lossy ? `${s.label}, approximate` : s.label,
                        color: s.color,
                        value: dash(formatPercentile(p[s.key as "p50" | "p95" | "p99"])),
                      }))
                    : []
                }
              />
            );
          }}
        />
        {LATENCY_SERIES.map((s) => (
          <Line
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stroke={s.color}
            strokeWidth={2}
            dot={openDot(s.key, s.color)}
            activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
            isAnimationActive={false}
            connectNulls={false}
            type="linear"
          />
        ))}
      </LineChart>
    </ChartCard>
  );
}

export const CLASS_META: Record<StatusClass, { label: string; color: string }> = {
  "1xx": { label: "1xx informational", color: "var(--chart-1xx)" },
  "2xx": { label: "2xx success", color: "var(--chart-1)" },
  "3xx": { label: "3xx redirect", color: "var(--chart-3xx)" },
  "4xx": { label: "4xx client error", color: "var(--chart-4xx)" },
  "5xx": { label: "5xx server error", color: "var(--chart-5xx)" },
  unknown: { label: "No status code", color: "var(--chart-none)" },
};

export function share(n: number, total: number): string {
  if (total === 0 || n === 0) return "0%";
  const pct = (n / total) * 100;
  if (pct < 0.1) return "<0.1%";
  return `${pct.toFixed(1).replace(/\.0$/, "")}%`;
}

// one whole split into its classes: a segmented bar, widths proportional,
// a class without requests draws nothing. names and numbers are in the
// legend list beside it, so the bar itself carries no text.
export function StatusClassBar({ status }: { status: ApiStatus }) {
  const total = status.requests;
  const parts = status.classes.filter((c) => c.requests > 0);
  return (
    <div>
      <div
        role="img"
        aria-label={`Requests by status class: ${status.classes
          .map(
            (c) =>
              `${CLASS_META[c.class].label} ${formatCount(c.requests)}, ${share(c.requests, total)}`,
          )
          .join("; ")}`}
        className="flex h-7 w-full gap-0.5 overflow-hidden"
      >
        {parts.map((c) => (
          <div
            key={c.class}
            title={`${CLASS_META[c.class].label}: ${formatCount(c.requests)} (${share(c.requests, total)})`}
            className="h-full min-w-0.5 rounded"
            // widths are data, so they cannot be classes; react writes them
            // through the cssom
            style={{ flexGrow: c.requests, flexBasis: 0, background: CLASS_META[c.class].color }}
          />
        ))}
      </div>
      <ul className="mt-3 grid gap-x-6 gap-y-1.5 text-sm sm:grid-cols-2">
        {status.classes.map((c) => (
          <li key={c.class} className="flex items-center gap-2">
            <svg aria-hidden width="10" height="10" className="shrink-0">
              <rect width="10" height="10" rx="2" fill={CLASS_META[c.class].color} />
            </svg>
            <span className="min-w-0 flex-1">{CLASS_META[c.class].label}</span>
            <span className="tabular font-medium">{formatCount(c.requests)}</span>
            <span className="tabular w-12 text-right text-muted-foreground">
              {share(c.requests, total)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
