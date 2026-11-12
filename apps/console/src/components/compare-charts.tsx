// daily charts of the operator comparison: one line per project, every chart
// on the same days (contract 8.4 and 8.6 align every series with `days`), so
// the charts stack and read across. a project keeps its colour slot on every
// chart of the page. every chart has a legend and a table view, so no value
// depends on hovering or on colour (two of the five slots are under 3:1 on
// the light card).

import { CartesianGrid, Line, LineChart, ReferenceLine, Tooltip, XAxis, YAxis } from "recharts";
import { ChartCard, Legend, type Series } from "@/components/api-charts";
import { OPEN_BUCKET_CEILING } from "@/lib/api-metrics";
import { formatDay, formatShortDay } from "@/lib/global";

const HEIGHT = 200;
const MARGIN = { top: 8, right: 8, bottom: 0, left: 0 };
const AXIS = { fontSize: 12, fill: "var(--muted-foreground)" };

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
          r={4}
          fill={color}
          stroke="var(--card)"
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
        fill="var(--card)"
        stroke={color}
        strokeWidth={2}
      />
    );
  };
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
  kind,
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
  // counts get whole ticks; a share keeps its axis at 0 to 100% while every
  // value is 0, instead of an axis scaled to nothing
  kind: "count" | "share" | "measure";
}) {
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
  const text = (i: number, d: number) => {
    const v = value(i, d);
    return v === null ? missingText : format(v);
  };
  return (
    <ChartCard
      title={title}
      description={
        anyOpen
          ? `${description} Rings on the 10 s line are above 10 s, the last bucket bound.`
          : description
      }
      legend={<Legend series={series} />}
      dim={false}
      table={
        <table className="w-full text-left text-sm">
          <thead className="sticky top-0 bg-muted text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-3 py-2 font-medium">
                Day (UTC)
              </th>
              {series.map((s) => (
                <th key={s.key} scope="col" className="px-3 py-2 text-right font-medium">
                  {s.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {days.map((day, d) => (
              <tr key={day}>
                <th scope="row" className="px-3 py-1.5 font-normal whitespace-nowrap">
                  {formatDay(day)}
                </th>
                {series.map((s, i) => (
                  <td key={s.key} className="tabular px-3 py-1.5 text-right whitespace-nowrap">
                    {text(i, d)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      }
    >
      <LineChart responsive width="100%" height={HEIGHT} data={rows} margin={MARGIN} title={title}>
        <CartesianGrid vertical={false} stroke="var(--chart-grid)" />
        <XAxis
          dataKey="day"
          tickFormatter={formatShortDay}
          tick={AXIS}
          tickLine={false}
          axisLine={{ stroke: "var(--border)" }}
          minTickGap={24}
        />
        <YAxis
          tickFormatter={axisFormat}
          tick={AXIS}
          tickLine={false}
          axisLine={false}
          width={52}
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
        {anyOpen ? (
          <ReferenceLine
            y={OPEN_BUCKET_CEILING}
            stroke="var(--muted-foreground)"
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
            const d = typeof label === "string" ? days.indexOf(label) : -1;
            if (!active || d < 0) return null;
            return (
              <div className="min-w-40 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
                <p className="mb-1.5 text-muted-foreground">{formatDay(days[d] ?? "")}</p>
                <ul className="space-y-1">
                  {series.map((s, i) => (
                    <li key={s.key} className="flex items-center gap-2">
                      <svg aria-hidden width="12" height="4" className="shrink-0">
                        <line x1="0" x2="12" y1="2" y2="2" stroke={s.color} strokeWidth="2" />
                      </svg>
                      <span className="tabular font-semibold">{text(i, d)}</span>
                      <span className="truncate text-muted-foreground">{s.label}</span>
                    </li>
                  ))}
                </ul>
              </div>
            );
          }}
        />
        {series.map((s) => (
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
