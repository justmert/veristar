// daily charts of the operator comparison (system v2 F-Compare): one line per
// project, every chart on the same days (contract 8.4 and 8.6 align every
// series with `days`), so the charts stack and read across. a project keeps
// its series slot on every chart of the page and in every table, in the
// order the comparison names them. the dash pattern repeats the slot, so no
// line depends on its colour alone, and every chart has a table view.

import { cn } from "@veristar/brand/cn";
import { durationMs } from "@veristar/brand/motion";
import { useReducedMotion } from "motion/react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceArea,
  ReferenceLine,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { ChartCard, type Series } from "@/components/api-charts";
import { useRevealOnce } from "@/components/motion";
import { OPEN_BUCKET_CEILING } from "@/lib/api-metrics";
import { formatDate, formatTick } from "@/lib/format";

// the fixed slots: colour token, its class for html swatches, and the dash
// pattern that tells the lines apart without colour
export const SERIES_COLOR = [
  "var(--vs-series-1)",
  "var(--vs-series-2)",
  "var(--vs-series-3)",
  "var(--vs-series-4)",
  "var(--vs-series-5)",
];
export const SERIES_BG = [
  "bg-series-1",
  "bg-series-2",
  "bg-series-3",
  "bg-series-4",
  "bg-series-5",
];
export const SERIES_DASH = ["", "6 3", "1 3", "8 3 2 3", "3 3"];

// a line sample in the slot's colour and dash, for legends and table rows
export function SeriesLine({ slot, className }: { slot: number; className?: string }) {
  return (
    <svg aria-hidden width="16" height="8" className={cn("shrink-0", className)}>
      <line
        x1="1"
        x2="15"
        y1="4"
        y2="4"
        stroke={SERIES_COLOR[slot] ?? "var(--vs-fg-3)"}
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeDasharray={SERIES_DASH[slot] || undefined}
      />
    </svg>
  );
}

const HEIGHT = 180;
const MARGIN = { top: 8, right: 20, bottom: 0, left: 0 };
const AXIS = { fontSize: 11, fill: "var(--vs-fg-3)", fontFamily: "var(--vs-font-mono)" };

// a value of one project on one day: a number, null for no value (a gap),
// or "open" for a percentile above the 10 s bucket bound
export type CompareValue = number | null | "open";

type Row = Record<string, number | string | null> & { day: string };

function plotted(v: CompareValue): number | null {
  if (v === "open") return OPEN_BUCKET_CEILING;
  return v;
}

type DotProps = { cx?: number; cy?: number; payload?: Row; index?: number };

// a percentile above 10 s sits on the 10 s line as a ring, read as
// "somewhere above", never as a measured 10 s
function openDot(key: string, color: string) {
  return function OpenDot({ cx, cy, payload, index }: DotProps) {
    if (cx === undefined || cy === undefined || !payload) return <g key={`${key}-${index}`} />;
    // a value with no value on either side draws no line at all, so it
    // gets a filled marker instead of disappearing
    if (payload[`${key}:open`] !== 1) {
      if (payload[`${key}:lone`] !== 1) return <g key={`${key}-${index}`} />;
      return (
        <circle
          key={`${key}-${index}`}
          cx={cx}
          cy={cy}
          r={3}
          fill={color}
          stroke="var(--vs-raised)"
          strokeWidth={2}
        />
      );
    }
    return (
      <circle
        key={`${key}-${index}`}
        cx={cx}
        cy={cy}
        r={4}
        fill="var(--vs-raised)"
        stroke={color}
        strokeWidth={2}
      />
    );
  };
}

// runs of days without a value for any project: not read by the
// aggregation yet, drawn as a shaded band that says so
function gapRuns(rows: Row[], series: Series[]): { from: string; to: string }[] {
  const runs: { from: string; to: string }[] = [];
  let start: string | null = null;
  let last: string | null = null;
  for (const r of rows) {
    const empty = series.length > 0 && series.every((s) => r[s.key] === null);
    if (empty) {
      start ??= r.day;
      last = r.day;
    } else if (start && last) {
      runs.push({ from: start, to: last });
      start = null;
    }
  }
  if (start && last) runs.push({ from: start, to: last });
  return runs;
}

export function CompareChart({
  title,
  description,
  days,
  series,
  value,
  format,
  axisFormat,
  missingText = "-",
  gapLabel,
  kind,
  markers = [],
}: {
  title: string;
  description: string;
  days: string[];
  series: Series[];
  // value of series index s on day index d
  value: (s: number, d: number) => CompareValue;
  format: (v: number | "open") => string;
  axisFormat: (v: number) => string;
  missingText?: string;
  // a day without a value for every project is labelled in the plot
  gapLabel?: string;
  // counts get whole ticks; a share keeps its axis at 0 to 100% while every
  // value is 0, instead of an axis scaled to nothing
  kind: "count" | "share" | "measure";
  // days to annotate, such as a project's award date (contract 19.5), in
  // the project's series slot; a day outside the chart is left out
  markers?: ChartMarker[];
}) {
  const { ref, seen } = useRevealOnce<HTMLDivElement>();
  // the lines draw in once; people who ask for reduced motion get them drawn
  const still = useReducedMotion() ?? false;
  let anyOpen = false;
  const rows: Row[] = days.map((day, d) => {
    const row: Row = { day };
    series.forEach((s, i) => {
      const v = value(i, d);
      if (v === "open") anyOpen = true;
      row[s.key] = plotted(v);
      row[`${s.key}:open`] = v === "open" ? 1 : 0;
    });
    return row;
  });
  rows.forEach((row, d) => {
    for (const s of series) {
      const has = (r: Row | undefined) => r !== undefined && r[s.key] !== null;
      row[`${s.key}:lone`] = has(row) && !has(rows[d - 1]) && !has(rows[d + 1]) ? 1 : 0;
    }
  });
  const gaps = gapLabel ? gapRuns(rows, series) : [];
  const shown = markers.filter((m) => days.includes(m.day));
  const text = (i: number, d: number) => {
    const v = value(i, d);
    return v === null ? missingText : format(v);
  };
  const first = days[0];
  const middle = days[Math.floor((days.length - 1) / 2)];
  const last = days[days.length - 1];
  return (
    <ChartCard
      title={title}
      description={
        anyOpen
          ? `${description} Rings on the 10 s line are above 10 s, the last bucket bound.`
          : description
      }
      legend={
        series.length > 1 || shown.length > 0 ? (
          <div className="flex flex-col gap-1">
            {series.length > 1 ? <CompareLegend series={series} /> : null}
            {shown.length > 0 ? <MarkerLegend markers={shown} /> : null}
          </div>
        ) : undefined
      }
      dim={false}
      table={
        <table className="w-full text-left text-small tabular">
          <thead>
            <tr>
              <th
                scope="col"
                className="sticky top-0 border-b border-line bg-raised px-3 py-2 text-caption font-medium text-fg-3"
              >
                Day (UTC)
              </th>
              {series.map((s, i) => (
                <th
                  key={s.key}
                  scope="col"
                  className="sticky top-0 border-b border-line bg-raised px-3 py-2 text-right text-caption font-medium text-fg-3"
                >
                  <span className="inline-flex items-center gap-1">
                    <SeriesLine slot={i} />
                    {s.label}
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {days.map((day, d) => (
              <tr key={day} className="hover:bg-hover">
                <th
                  scope="row"
                  className="border-b border-line-subtle px-3 py-1 font-normal whitespace-nowrap text-fg-2"
                >
                  {formatDate(day)}
                </th>
                {series.map((s, i) => (
                  <td
                    key={s.key}
                    className="border-b border-line-subtle px-3 py-1 text-right font-mono whitespace-nowrap text-fg"
                  >
                    {text(i, d)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      }
    >
      <div ref={ref} className="min-h-45">
        {seen ? (
          <LineChart
            responsive
            width="100%"
            height={HEIGHT}
            data={rows}
            margin={MARGIN}
            title={title}
          >
            <CartesianGrid vertical={false} stroke="var(--vs-grid)" />
            <XAxis
              dataKey="day"
              ticks={[first, middle, last].filter(
                (x, i, a): x is string => !!x && a.indexOf(x) === i,
              )}
              interval={0}
              tickFormatter={(d: string) => formatTick(d, "day")}
              tick={{ ...AXIS, fontFamily: "var(--vs-font-sans)" }}
              tickLine={false}
              axisLine={{ stroke: "var(--vs-axis)" }}
            />
            <YAxis
              orientation="left"
              tickFormatter={axisFormat}
              tick={AXIS}
              tickLine={false}
              axisLine={false}
              width={52}
              tickCount={4}
              allowDecimals={kind !== "count"}
              domain={[
                0,
                anyOpen
                  ? OPEN_BUCKET_CEILING
                  : kind === "share"
                    ? (max: number) => (max > 0 ? Math.min(1, max * 1.1) : 1)
                    : (max: number) => (max > 0 ? max : 1),
              ]}
            />
            {gaps.map((g) =>
              g.from === g.to ? (
                <ReferenceLine
                  key={g.from}
                  x={g.from}
                  stroke="var(--vs-line-strong)"
                  strokeDasharray="3 3"
                  label={{
                    value: gapLabel,
                    position: "insideTop",
                    fontSize: 10,
                    fill: "var(--vs-fg-3)",
                  }}
                />
              ) : (
                <ReferenceArea
                  key={g.from}
                  x1={g.from}
                  x2={g.to}
                  fill="var(--vs-hover)"
                  fillOpacity={1}
                  stroke="none"
                  label={{
                    value: gapLabel,
                    position: "insideTop",
                    fontSize: 10,
                    fill: "var(--vs-fg-3)",
                  }}
                />
              ),
            )}
            {shown.map((m) => (
              <ReferenceLine
                key={`${m.slot}-${m.day}`}
                x={m.day}
                stroke={SERIES_COLOR[m.slot] ?? "var(--vs-fg-3)"}
                strokeDasharray="2 2"
                label={{
                  value: m.label,
                  position: "insideTopRight",
                  fontSize: 10,
                  fill: "var(--vs-fg-2)",
                }}
              />
            ))}
            {anyOpen ? (
              <ReferenceLine
                y={OPEN_BUCKET_CEILING}
                stroke="var(--vs-line-strong)"
                label={{
                  value: ">10 s above",
                  position: "insideTopLeft",
                  fontSize: 11,
                  fill: "var(--vs-fg-3)",
                }}
              />
            ) : null}
            <Tooltip
              cursor={{ stroke: "var(--vs-line-strong)", strokeWidth: 1 }}
              isAnimationActive={false}
              content={({ active, label }) => {
                const d = typeof label === "string" ? days.indexOf(label) : -1;
                if (!active || d < 0) return null;
                return (
                  <div className="min-w-44 rounded-md border border-line-strong bg-overlay px-3 py-2 text-caption shadow-overlay">
                    <p className="mb-1 text-fg-3">{formatDate(days[d] ?? "")} (UTC)</p>
                    <ul className="flex flex-col gap-1">
                      {series.map((s, i) => (
                        <li key={s.key} className="flex items-center gap-2">
                          <SeriesLine slot={i} />
                          <span className="truncate text-fg-2">{s.label}</span>
                          <span className="ml-auto pl-3 font-mono font-medium text-fg">
                            {text(i, d)}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                );
              }}
            />
            {series.map((s, i) => {
              const color = SERIES_COLOR[i] ?? "var(--vs-fg-3)";
              return (
                <Line
                  key={s.key}
                  dataKey={s.key}
                  name={s.label}
                  stroke={color}
                  strokeWidth={2}
                  strokeLinejoin="round"
                  strokeLinecap="round"
                  strokeDasharray={SERIES_DASH[i] || undefined}
                  dot={openDot(s.key, color)}
                  activeDot={{ r: 4, fill: color, stroke: "var(--vs-raised)", strokeWidth: 2 }}
                  isAnimationActive={!still}
                  animationDuration={durationMs.page}
                  connectNulls={false}
                  type="linear"
                />
              );
            })}
          </LineChart>
        ) : null}
      </div>
    </ChartCard>
  );
}

// a day a chart marks, such as an award date, in a project's series slot
export type ChartMarker = { day: string; slot: number; label: string };

// the marked days in words, so the annotation also reads without the plot
export function MarkerLegend({ markers }: { markers: ChartMarker[] }) {
  return (
    <ul className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-fg-2">
      {markers.map((m) => (
        <li
          key={`${m.slot}-${m.day}`}
          data-marker={m.day}
          className="inline-flex items-center gap-1"
        >
          <svg aria-hidden width="8" height="12" className="shrink-0">
            <line
              x1="4"
              x2="4"
              y1="0"
              y2="12"
              stroke={SERIES_COLOR[m.slot] ?? "var(--vs-fg-3)"}
              strokeWidth="2"
              strokeDasharray="2 2"
            />
          </svg>
          {m.label}, {formatDate(m.day)}
        </li>
      ))}
    </ul>
  );
}

export function CompareLegend({ series }: { series: Series[] }) {
  return (
    <ul className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-fg-2">
      {series.map((s, i) => (
        <li key={s.key} className="inline-flex min-w-0 items-center gap-1">
          <SeriesLine slot={i} />
          <span className="max-w-40 truncate">{s.label}</span>
        </li>
      ))}
    </ul>
  );
}
