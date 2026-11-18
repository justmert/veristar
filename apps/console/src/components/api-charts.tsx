// charts of the api traffic page (C-ChartCard, R2), drawn as html bars and
// an svg line layer so every colour is a token class. the plot answers on
// hover and from the keyboard: a crosshair on the nearest bucket and a
// readout with every series, the previous period and the bucket time.
// every chart also has a table view, so no value depends on hovering or on
// colour.

import { cn } from "@veristar/brand/cn";
import { fade, transition } from "@veristar/brand/motion";
import { Table2 } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useId, useRef, useState } from "react";
import type { ApiPoint, ApiRange, ApiStatus, StatusClass } from "@/api/schemas";
import { useRevealOnce } from "@/components/motion";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type Change,
  formatBucket,
  formatPercentile,
  formatRate,
  formatSeconds,
  OPEN_BUCKET,
  OPEN_BUCKET_CEILING,
  percentileChange,
  plotPercentile,
  rateChange,
  relativeChange,
  tickFormatter,
} from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";

// ---- colours: one role per series, as classes for html and svg

export type Tone = "series-1" | "series-2" | "series-3" | "warning" | "critical" | "prev";

const BG: Record<Tone, string> = {
  "series-1": "bg-series-1",
  "series-2": "bg-series-2",
  "series-3": "bg-series-3",
  warning: "bg-warning",
  critical: "bg-critical",
  prev: "bg-series-prev",
};
const STROKE: Record<Tone, string> = {
  "series-1": "stroke-series-1",
  "series-2": "stroke-series-2",
  "series-3": "stroke-series-3",
  warning: "stroke-warning",
  critical: "stroke-critical",
  prev: "stroke-series-prev",
};
const FILL: Record<Tone, string> = {
  "series-1": "fill-series-1",
  "series-2": "fill-series-2",
  "series-3": "fill-series-3",
  warning: "fill-warning",
  critical: "fill-critical",
  prev: "fill-series-prev",
};
const BORDER: Record<Tone, string> = {
  "series-1": "border-series-1",
  "series-2": "border-series-2",
  "series-3": "border-series-3",
  warning: "border-warning",
  critical: "border-critical",
  prev: "border-series-prev",
};

// a legend entry: `color` (a css variable string, as svg attributes take
// it) for charts drawn elsewhere, `tone` for the charts of this file
export type Series = {
  key: string;
  label: string;
  color?: string;
  tone?: Tone;
  dashed?: boolean;
};

function Swatch({ s, shape }: { s: Series; shape: "line" | "box" }) {
  if (shape === "box")
    return (
      <svg aria-hidden width="10" height="10" className="shrink-0">
        <rect
          width="10"
          height="10"
          rx="2"
          fill={s.color}
          className={s.tone ? FILL[s.tone] : undefined}
        />
      </svg>
    );
  return (
    <svg aria-hidden width="14" height="6" className="shrink-0">
      <line
        x1="0"
        x2="14"
        y1="3"
        y2="3"
        stroke={s.color}
        strokeWidth="2"
        strokeDasharray={s.dashed ? "3 2" : undefined}
        className={s.tone ? STROKE[s.tone] : undefined}
      />
    </svg>
  );
}

export function Legend({ series, shape = "line" }: { series: Series[]; shape?: "line" | "box" }) {
  return (
    <ul className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption font-normal text-fg-2">
      {series.map((s) => (
        <li key={s.key} className="inline-flex items-center gap-1">
          <Swatch s={s} shape={shape} />
          {s.label}
        </li>
      ))}
    </ul>
  );
}

// ---- the card: title and unit line, legend and actions, then the plot or
// its table

export function ChartCard({
  title,
  description,
  legend,
  table,
  dim,
  children,
  actions,
  className,
}: {
  title: string;
  description: ReactNode;
  legend?: ReactNode;
  table: ReactNode;
  dim: boolean;
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  const id = useId();
  const [asTable, setAsTable] = useState(false);
  return (
    <section
      aria-labelledby={id}
      className={cn(
        "flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised px-4 pt-4 pb-3",
        className,
      )}
    >
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-col gap-1">
          <h3 id={id} className="text-title text-fg">
            {title}
          </h3>
          <p className="max-w-prose text-caption font-normal text-fg-3">{description}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {legend}
          {actions}
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label="Show as table"
            title="Show as table"
            aria-pressed={asTable}
            onClick={() => setAsTable((v) => !v)}
            className={asTable ? "border-fg-disabled bg-selected" : undefined}
          >
            <Table2 aria-hidden />
          </Button>
        </div>
      </header>
      <div
        className={cn(
          "min-w-0 transition-opacity duration-(--vs-duration-change) ease-standard",
          dim && "opacity-60",
        )}
      >
        <AnimatePresence mode="wait" initial={false}>
          <motion.div key={asTable ? "table" : "plot"} {...fade}>
            {asTable ? (
              <div className="relative max-h-80 overflow-auto rounded-md border border-line">
                {table}
              </div>
            ) : (
              children
            )}
          </motion.div>
        </AnimatePresence>
      </div>
    </section>
  );
}

// a toggle for the dashed previous-period line, shared by the cards
export function PreviousToggle({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      aria-pressed={on}
      onClick={onToggle}
      className={on ? "border-fg-disabled bg-selected" : undefined}
    >
      <svg aria-hidden width="14" height="6">
        <line
          x1="0"
          x2="14"
          y1="3"
          y2="3"
          strokeWidth="2"
          strokeDasharray="3 2"
          className="stroke-series-prev"
        />
      </svg>
      Previous period
    </Button>
  );
}

// ---- axis

// 3 to 5 round values from 0 up to at least max
export function niceTicks(max: number, integer = false): number[] {
  if (!(max > 0)) return [0, 1];
  const raw = max / 4;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const norm = raw / mag;
  let step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag;
  if (integer && step < 1) step = 1;
  const top = Math.ceil(max / step - 1e-9) * step;
  const out: number[] = [];
  for (let i = 0; i * step <= top + step / 2; i++) out.push(Number((i * step).toPrecision(12)));
  return out;
}

export function compactCount(n: number): string {
  return new Intl.NumberFormat("en", {
    notation: n >= 10_000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(n);
}

// ---- the plot

export type PlotSeries = {
  key: string;
  label: string;
  tone: Tone;
  kind: "bar" | "line";
  values: (number | null)[];
  // the value as the readout says it
  text: (i: number) => string;
  // a value in the open bucket: drawn as a ring on the ceiling
  open?: (i: number) => boolean;
};

export type PlotPrevious = {
  label: string;
  values: (number | null)[];
  text: (i: number) => string;
  delta: (i: number) => { text: string; tone: "good" | "bad" | "neutral" } | null;
};

const H = 100;

// a line breaks at every bucket without a value; a value with no neighbour
// draws no line, so it is returned apart and gets a point of its own
function segments(values: (number | null)[], x: (i: number) => number, y: (v: number) => number) {
  const lines: string[] = [];
  const lone: number[] = [];
  let cur: string[] = [];
  const close = (end: number) => {
    if (cur.length === 1) lone.push(end);
    if (cur.length > 1) lines.push(cur.join(" "));
    cur = [];
  };
  values.forEach((v, i) => {
    if (v === null) {
      close(i - 1);
      return;
    }
    cur.push(`${x(i).toFixed(3)},${y(v).toFixed(3)}`);
  });
  close(values.length - 1);
  return { lines, lone };
}

export function TimePlot({
  label,
  range,
  times,
  series,
  previous,
  yFormat,
  yMax,
  integer = false,
  ceiling,
}: {
  label: string;
  range: ApiRange;
  times: string[];
  series: PlotSeries[];
  previous?: PlotPrevious | null;
  yFormat: (v: number) => string;
  // a fixed top of the scale (the 10 s line of latency)
  yMax?: number;
  integer?: boolean;
  ceiling?: { value: number; label: string } | null;
}) {
  const n = times.length;
  const tipId = useId();
  const [hover, setHover] = useState<number | null>(null);
  const plotRef = useRef<HTMLDivElement>(null);
  const { ref: revealRef, seen } = useRevealOnce<HTMLDivElement>();

  const all = [...series.flatMap((s) => s.values), ...(previous ? previous.values : [])].filter(
    (v): v is number => v !== null,
  );
  const dataMax = all.length ? Math.max(...all) : 0;
  const ticks = niceTicks(yMax !== undefined ? Math.max(yMax, dataMax) : dataMax, integer);
  const top = ticks[ticks.length - 1] || 1;
  const x = (i: number) => ((i + 0.5) / Math.max(1, n)) * 100;
  const y = (v: number) => H - (Math.min(v, top) / top) * H;
  const tick = tickFormatter(range);
  const now = Date.now();
  // the newest bucket can still be filling; it is hatched, so a half bucket
  // never reads as a drop
  const partial = (i: number) => {
    const at = times[i];
    return at !== undefined && Date.parse(at) + range.bucket_seconds * 1000 > now;
  };
  const empty = all.every((v) => v === 0);
  const bars = series.filter((s) => s.kind === "bar");
  const lines = series.filter((s) => s.kind === "line");
  const gap = n <= 90 ? "gap-1" : n <= 240 ? "gap-px" : "";

  function pick(clientX: number) {
    const el = plotRef.current;
    if (!el || n === 0) return;
    const r = el.getBoundingClientRect();
    const i = Math.floor(((clientX - r.left) / Math.max(1, r.width)) * n);
    setHover(Math.min(n - 1, Math.max(0, i)));
  }
  function key(e: React.KeyboardEvent) {
    const cur = hover ?? n - 1;
    const next =
      e.key === "ArrowLeft"
        ? cur - 1
        : e.key === "ArrowRight"
          ? cur + 1
          : e.key === "Home"
            ? 0
            : e.key === "End"
              ? n - 1
              : null;
    if (next === null) return;
    e.preventDefault();
    setHover(Math.min(n - 1, Math.max(0, next)));
  }

  const mid = Math.floor((n - 1) / 2);
  const hv = hover !== null && hover < n ? hover : null;
  const tipLeft = hv !== null && hv > n * 0.6;
  const d = hv !== null && previous ? previous.delta(hv) : null;

  return (
    <div className="grid grid-cols-[2.75rem_minmax(0,1fr)] gap-2">
      <div aria-hidden className="relative h-52">
        {ticks.map((t) => (
          <span
            key={t}
            className="absolute right-0 translate-y-1/2 font-mono text-axis whitespace-nowrap text-fg-3"
            // the label sits on its grid line: geometry, not style
            style={{ bottom: `${(t / top) * 100}%` }}
          >
            {yFormat(t)}
          </span>
        ))}
      </div>
      <div className="flex min-w-0 flex-col">
        <div ref={revealRef} className="relative h-52">
          {ticks.map((t) => (
            <div
              key={t}
              aria-hidden
              className={cn("absolute inset-x-0 h-px", t === 0 ? "bg-axis" : "bg-grid")}
              style={{ bottom: `${(t / top) * 100}%` }}
            />
          ))}
          {ceiling && !empty ? (
            <div
              aria-hidden
              className="absolute inset-x-0 border-t border-dashed border-line-strong"
              style={{ bottom: `${(ceiling.value / top) * 100}%` }}
            >
              <span className="absolute top-0.5 left-1 font-mono text-axis text-fg-3">
                {ceiling.label}
              </span>
            </div>
          ) : null}

          {empty ? (
            <div className="absolute inset-0 flex animate-fade-in flex-col items-center justify-center gap-1 px-4 text-center">
              <span className="text-body font-medium text-fg">No requests in this range</span>
              <span className="text-small text-fg-2">
                Buckets fill in as your services send data.
              </span>
            </div>
          ) : (
            <motion.div
              className="absolute inset-0"
              initial={{ clipPath: "inset(0 100% 0 0)" }}
              animate={seen ? { clipPath: "inset(0 0% 0 0)" } : undefined}
              transition={transition.arrival}
            >
              {bars.length ? (
                <div aria-hidden className={cn("absolute inset-0 flex items-end", gap)}>
                  {times.map((at, i) => (
                    <div
                      key={at}
                      className={cn(
                        "flex h-full min-w-0 flex-1 flex-col justify-end gap-px transition-opacity duration-(--vs-duration-reveal) ease-standard",
                        partial(i) && "hatch",
                        hv !== null && hv !== i && "opacity-50",
                      )}
                    >
                      {[...bars].reverse().map((s) => {
                        const v = s.values[i] ?? 0;
                        if (v <= 0) return null;
                        return (
                          <div
                            key={s.key}
                            className={cn("w-full shrink-0 rounded-t-bar", BG[s.tone])}
                            style={{ height: `${(Math.min(v, top) / top) * 100}%` }}
                          />
                        );
                      })}
                    </div>
                  ))}
                </div>
              ) : null}
              <svg
                aria-hidden
                viewBox={`0 0 100 ${H}`}
                preserveAspectRatio="none"
                className="absolute inset-0 size-full overflow-visible"
              >
                {previous
                  ? segments(previous.values, x, y).lines.map((p) => (
                      <polyline
                        key={p}
                        points={p}
                        fill="none"
                        strokeWidth="1.5"
                        strokeDasharray="4 3"
                        vectorEffect="non-scaling-stroke"
                        className="stroke-series-prev"
                      />
                    ))
                  : null}
                {lines.flatMap((s) =>
                  segments(s.values, x, y).lines.map((p) => (
                    <polyline
                      key={`${s.key}:${p}`}
                      points={p}
                      fill="none"
                      strokeWidth="2"
                      strokeLinejoin="round"
                      strokeLinecap="round"
                      vectorEffect="non-scaling-stroke"
                      className={STROKE[s.tone]}
                    />
                  )),
                )}
              </svg>
              {lines.flatMap((s) => {
                const { lone } = segments(s.values, x, y);
                return s.values.map((v, i) => {
                  if (v === null) return null;
                  const open = s.open?.(i) ?? false;
                  if (!open && !lone.includes(i)) return null;
                  return (
                    <span
                      key={`${s.key}-${times[i]}`}
                      aria-hidden
                      className={cn(
                        "absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2",
                        BORDER[s.tone],
                        open ? "bg-raised" : BG[s.tone],
                      )}
                      style={{ left: `${x(i)}%`, top: `${y(v)}%` }}
                    />
                  );
                });
              })}
            </motion.div>
          )}

          {hv !== null && !empty ? (
            <>
              <div
                aria-hidden
                className="pointer-events-none absolute inset-y-0 w-px bg-line-strong"
                style={{ left: `${x(hv)}%` }}
              />
              {lines.map((s) => {
                const v = s.values[hv];
                if (v === null || v === undefined) return null;
                return (
                  <span
                    key={s.key}
                    aria-hidden
                    className={cn(
                      "pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-raised",
                      BG[s.tone],
                    )}
                    style={{ left: `${x(hv)}%`, top: `${y(v)}%` }}
                  />
                );
              })}
              <div
                id={tipId}
                role="tooltip"
                className="pointer-events-none absolute top-1.5 z-(--vs-layer-raised) flex min-w-44 animate-fade-in flex-col gap-1 rounded-md border border-line-strong bg-overlay px-3 py-2 text-caption font-normal shadow-overlay"
                style={{
                  left: `${x(hv)}%`,
                  transform: tipLeft ? "translateX(calc(-100% - 12px))" : "translateX(12px)",
                }}
              >
                <span className="whitespace-nowrap text-fg-2">
                  {formatBucket(times[hv] ?? "", range)}
                  {partial(hv) ? ", in progress" : ""}
                </span>
                {series.map((s) => (
                  <span key={s.key} className="flex items-center gap-2 whitespace-nowrap">
                    <span aria-hidden className={cn("size-2 rounded-bar", BG[s.tone])} />
                    <span className="flex-1 text-fg-2">{s.label}</span>
                    <span className="font-mono text-fg">{s.text(hv)}</span>
                  </span>
                ))}
                {previous ? (
                  <span className="flex items-center gap-2 whitespace-nowrap">
                    <span aria-hidden className="h-0.5 w-2 rounded-bar bg-series-prev" />
                    <span className="flex-1 text-fg-2">{previous.label}</span>
                    <span className="font-mono text-fg">{previous.text(hv)}</span>
                  </span>
                ) : null}
                {d ? (
                  <span className="flex justify-between gap-3 border-t border-line pt-1 whitespace-nowrap text-fg-2">
                    <span>vs previous</span>
                    <span
                      className={cn(
                        "font-mono",
                        d.tone === "good"
                          ? "text-success"
                          : d.tone === "bad"
                            ? "text-critical"
                            : "text-fg",
                      )}
                    >
                      {d.text}
                    </span>
                  </span>
                ) : null}
              </div>
            </>
          ) : null}

          {/* the plot reads by pointer and by arrow keys; the table view
              carries the same values for everyone else */}
          <div
            ref={plotRef}
            tabIndex={empty ? -1 : 0}
            role="img"
            aria-label={`${label}. Arrow keys read each bucket.`}
            aria-describedby={hv !== null ? tipId : undefined}
            onPointerMove={(e) => pick(e.clientX)}
            onPointerDown={(e) => pick(e.clientX)}
            onPointerLeave={() => setHover(null)}
            onFocus={() => setHover((h) => h ?? n - 1)}
            onBlur={() => setHover(null)}
            onKeyDown={key}
            className="absolute inset-0 cursor-crosshair rounded-sm focus-ring"
          />
        </div>
        <div
          aria-hidden
          className="flex justify-between gap-2 pt-1 font-mono text-axis whitespace-nowrap text-fg-3"
        >
          <span>{times[0] ? tick(times[0]) : ""}</span>
          {n > 2 ? <span>{tick(times[mid] ?? "")}</span> : null}
          <span>{n > 1 ? `${tick(times[n - 1] ?? "")} UTC` : ""}</span>
        </div>
      </div>
    </div>
  );
}

// the chart while its numbers load: bars in the plot's shape
const SKELETON = [38, 44, 41, 52, 58, 47, 36, 40, 49, 55, 60, 57, 45, 39, 47, 62, 66, 63, 53];

export function PlotSkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-busy="true" className="grid grid-cols-[2.75rem_minmax(0,1fr)] gap-2">
      <span className="sr-only">{label}</span>
      <span />
      <div aria-hidden className="flex h-52 items-end gap-1 border-b border-axis">
        {SKELETON.map((h, i) => (
          <Skeleton
            key={i}
            className="flex-1 rounded-t-bar rounded-b-none"
            style={{ height: `${h}%` }}
          />
        ))}
      </div>
    </div>
  );
}

// ---- table view

function SeriesTable({
  range,
  points,
  columns,
}: {
  range: ApiRange;
  points: ApiPoint[];
  columns: { label: string; value: (p: ApiPoint, i: number) => string }[];
}) {
  return (
    <table className="w-full text-left text-small">
      <thead className="sticky top-0 bg-overlay text-caption text-fg-2">
        <tr>
          <th scope="col" className="h-9 px-3 font-medium">
            Time
          </th>
          {columns.map((c) => (
            <th key={c.label} scope="col" className="h-9 px-3 text-right font-medium">
              {c.label}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="divide-y divide-line-subtle">
        {points.map((p, i) => (
          <tr key={p.at} className="hover:bg-hover">
            <th scope="row" className="h-8 px-3 font-normal whitespace-nowrap text-fg-2">
              {formatBucket(p.at, range)}
            </th>
            {columns.map((c) => (
              <td key={c.label} className="px-3 text-right font-mono whitespace-nowrap text-fg">
                {c.value(p, i)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const dash = (v: string | null) => v ?? "-";

// ---- the previous period, bucket by bucket: the same offset into the
// previous range as the current bucket has into the current one. ranges at
// another resolution (a retention step) are not compared bucket by bucket.

export function alignPrevious(
  points: ApiPoint[],
  range: ApiRange,
  previous: { range: ApiRange; points: ApiPoint[] } | null | undefined,
): (ApiPoint | null)[] | null {
  if (!previous || previous.range.resolution !== range.resolution) return null;
  const offset = Date.parse(range.from) - Date.parse(previous.range.from);
  const byAt = new Map(previous.points.map((p) => [Date.parse(p.at), p]));
  return points.map((p) => byAt.get(Date.parse(p.at) - offset) ?? null);
}

function delta(c: Change, upIsBad: boolean | null) {
  if (!c.text) return null;
  const tone: "good" | "bad" | "neutral" =
    c.direction === null || c.direction === "same" || upIsBad === null
      ? "neutral"
      : (c.direction === "up") === upIsBad
        ? "bad"
        : "good";
  return { text: c.text, tone };
}

export type ChartProps = {
  range: ApiRange;
  points: ApiPoint[];
  dim: boolean;
  // the previous period aligned to these points, or null when there is none
  previous?: (ApiPoint | null)[] | null;
  showPrevious?: boolean;
  onTogglePrevious?: () => void;
  className?: string;
};

function prevActions(p: ChartProps) {
  return p.previous && p.onTogglePrevious ? (
    <PreviousToggle on={p.showPrevious ?? false} onToggle={p.onTogglePrevious} />
  ) : null;
}

function prevColumn(
  previous: (ApiPoint | null)[] | null | undefined,
  label: string,
  value: (q: ApiPoint) => string,
) {
  return previous
    ? [
        {
          label,
          value: (_p: ApiPoint, i: number) => {
            const q = previous[i];
            return q ? value(q) : "-";
          },
        },
      ]
    : [];
}

// "in each hour", not "per hour": the range line above the page says
// ", per hour" once, and the specs find it by that text
const PER: Record<ApiRange["resolution"], string> = {
  minute: "in each minute",
  hour: "in each hour",
  day: "in each day",
};

export function VolumeChart(props: ChartProps) {
  const { range, points, dim, previous, showPrevious, className } = props;
  const prev = showPrevious && previous ? previous : null;
  return (
    <ChartCard
      title="Requests"
      description={`Every request your services handled ${PER[range.resolution]}, UTC.`}
      actions={prevActions(props)}
      dim={dim}
      className={className}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[
            { label: "Requests", value: (p) => formatCount(p.requests) },
            ...prevColumn(previous, "Previous period", (q) => formatCount(q.requests)),
          ]}
        />
      }
    >
      <TimePlot
        label="Requests per bucket"
        range={range}
        times={points.map((p) => p.at)}
        integer
        yFormat={compactCount}
        series={[
          {
            key: "requests",
            label: "Requests",
            tone: "series-1",
            kind: "bar",
            values: points.map((p) => p.requests),
            text: (i) => formatCount(points[i]?.requests ?? 0),
          },
        ]}
        previous={
          prev
            ? {
                label: "Previous period",
                values: prev.map((q) => (q ? q.requests : null)),
                text: (i) => {
                  const q = prev[i];
                  return q ? formatCount(q.requests) : "-";
                },
                delta: (i) => {
                  const q = prev[i];
                  const p = points[i];
                  return q && p ? delta(relativeChange(p.requests, q.requests), null) : null;
                },
              }
            : null
        }
      />
    </ChartCard>
  );
}

const rateOf = (p: ApiPoint, k: "4xx" | "5xx") => (k === "4xx" ? p.rate_4xx : p.rate_5xx);

export function ErrorChart(props: ChartProps & { focus?: "4xx" | "5xx" }) {
  const { range, points, dim, previous, showPrevious, className, focus = "5xx" } = props;
  const prev = showPrevious && previous ? previous : null;
  return (
    <ChartCard
      title="Error rates"
      description="Share of requests answered with a 4xx or a 5xx status. Gaps are buckets without requests."
      legend={
        <Legend
          series={[
            { key: "4xx", label: "4xx rate", tone: "warning" },
            { key: "5xx", label: "5xx rate", tone: "critical" },
            ...(prev
              ? [{ key: "p", label: `${focus} previous`, tone: "prev" as const, dashed: true }]
              : []),
          ]}
        />
      }
      actions={prevActions(props)}
      dim={dim}
      className={className}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[
            { label: "4xx", value: (p) => formatCount(p.errors_4xx) },
            { label: "4xx rate", value: (p) => dash(formatRate(p.rate_4xx)) },
            { label: "5xx", value: (p) => formatCount(p.errors_5xx) },
            { label: "5xx rate", value: (p) => dash(formatRate(p.rate_5xx)) },
            ...prevColumn(previous, `${focus} rate, previous`, (q) =>
              dash(formatRate(rateOf(q, focus))),
            ),
          ]}
        />
      }
    >
      <TimePlot
        label="Error rates per bucket"
        range={range}
        times={points.map((p) => p.at)}
        yFormat={(v) => formatRate(v) ?? ""}
        series={(["4xx", "5xx"] as const).map((k) => ({
          key: k,
          label: k === "4xx" ? "4xx rate" : "5xx rate",
          tone: k === "4xx" ? ("warning" as const) : ("critical" as const),
          kind: "line" as const,
          values: points.map((p) => rateOf(p, k)),
          text: (i: number) => {
            const p = points[i];
            if (!p) return "-";
            const n = k === "4xx" ? p.errors_4xx : p.errors_5xx;
            return `${dash(formatRate(rateOf(p, k)))} (${formatCount(n)})`;
          },
        }))}
        previous={
          prev
            ? {
                label: `${focus} rate, previous`,
                values: prev.map((q) => (q ? rateOf(q, focus) : null)),
                text: (i) => {
                  const q = prev[i];
                  return q ? dash(formatRate(rateOf(q, focus))) : "-";
                },
                delta: (i) => {
                  const q = prev[i];
                  const p = points[i];
                  return q && p
                    ? delta(rateChange(rateOf(p, focus), rateOf(q, focus)), true)
                    : null;
                },
              }
            : null
        }
      />
    </ChartCard>
  );
}

const PCT = ["p50", "p95", "p99"] as const;
const PCT_TONE: Record<(typeof PCT)[number], Tone> = {
  p50: "series-1",
  p95: "series-2",
  p99: "series-3",
};

export function LatencyChart(props: ChartProps) {
  const { range, points, dim, previous, showPrevious, className } = props;
  const prev = showPrevious && previous ? previous : null;
  const anyOpen = points.some((p) => PCT.some((k) => p[k] === OPEN_BUCKET));
  const anyLossy = points.some((p) => p.lossy);
  return (
    <ChartCard
      title="Latency"
      description={
        <>
          Percentiles of request duration {PER[range.resolution]}, from summed histograms.
          {anyOpen ? " Rings on the 10 s line are above 10 s, the last bucket bound." : ""}
          {anyLossy ? " Some buckets are approximate (marked in the table)." : ""}
        </>
      }
      legend={
        <Legend
          series={[
            ...PCT.map((k) => ({ key: k, label: k, tone: PCT_TONE[k] })),
            ...(prev
              ? [{ key: "prev", label: "p95 previous", tone: "prev" as const, dashed: true }]
              : []),
          ]}
        />
      }
      actions={prevActions(props)}
      dim={dim}
      className={className}
      table={
        <SeriesTable
          range={range}
          points={points}
          columns={[
            ...PCT.map((k) => ({
              label: k,
              value: (p: ApiPoint) => dash(formatPercentile(p[k])),
            })),
            { label: "Approximate", value: (p: ApiPoint) => (p.lossy ? "yes" : "no") },
            ...prevColumn(previous, "p95, previous", (q) => dash(formatPercentile(q.p95))),
          ]}
        />
      }
    >
      <TimePlot
        label="Latency percentiles per bucket"
        range={range}
        times={points.map((p) => p.at)}
        yFormat={formatSeconds}
        yMax={anyOpen ? OPEN_BUCKET_CEILING : undefined}
        ceiling={anyOpen ? { value: OPEN_BUCKET_CEILING, label: ">10 s above" } : null}
        series={PCT.map((k) => ({
          key: k,
          label: k,
          tone: PCT_TONE[k],
          kind: "line" as const,
          values: points.map((p) => plotPercentile(p[k])),
          open: (i: number) => points[i]?.[k] === OPEN_BUCKET,
          text: (i: number) => {
            const p = points[i];
            if (!p) return "-";
            const t = dash(formatPercentile(p[k]));
            return p.lossy && p[k] !== null ? `${t}, approx.` : t;
          },
        }))}
        previous={
          prev
            ? {
                label: "p95, previous",
                values: prev.map((q) => (q ? plotPercentile(q.p95) : null)),
                text: (i) => {
                  const q = prev[i];
                  return q ? dash(formatPercentile(q.p95)) : "-";
                },
                delta: (i) => {
                  const q = prev[i];
                  const p = points[i];
                  return q && p ? delta(percentileChange(p.p95, q.p95), true) : null;
                },
              }
            : null
        }
      />
    </ChartCard>
  );
}

// ---- status classes

export const CLASS_META: Record<StatusClass, { label: string; bg: string }> = {
  "1xx": { label: "1xx informational", bg: "bg-series-3" },
  "2xx": { label: "2xx success", bg: "bg-series-1" },
  "3xx": { label: "3xx redirect", bg: "bg-series-4" },
  "4xx": { label: "4xx client error", bg: "bg-warning" },
  "5xx": { label: "5xx server error", bg: "bg-critical" },
  unknown: { label: "No status code", bg: "bg-series-prev" },
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
    <div className="flex flex-col gap-3">
      <div
        role="img"
        aria-label={`Requests by status class: ${status.classes
          .map(
            (c) =>
              `${CLASS_META[c.class].label} ${formatCount(c.requests)}, ${share(c.requests, total)}`,
          )
          .join("; ")}`}
        className="flex h-3 w-full gap-1 overflow-hidden rounded-sm"
      >
        {parts.length === 0 ? <div className="h-full flex-1 rounded-bar bg-muted" /> : null}
        {parts.map((c) => (
          <div
            key={c.class}
            title={`${CLASS_META[c.class].label}: ${formatCount(c.requests)} (${share(c.requests, total)})`}
            className={cn("h-full min-w-0.5 rounded-bar", CLASS_META[c.class].bg)}
            // widths are data, so they cannot be classes; react writes them
            // through the cssom
            style={{ flexGrow: c.requests, flexBasis: 0 }}
          />
        ))}
      </div>
      <ul className="grid gap-x-6 gap-y-1 text-small sm:grid-cols-2">
        {status.classes.map((c) => (
          <li
            key={c.class}
            className={cn("flex items-center gap-2", c.requests === 0 ? "text-fg-3" : "text-fg")}
          >
            <span
              aria-hidden
              className={cn("size-2.5 shrink-0 rounded-bar", CLASS_META[c.class].bg)}
            />
            <span className="min-w-0 flex-1">{CLASS_META[c.class].label}</span>
            <span className="font-mono">{formatCount(c.requests)}</span>
            <span className="w-12 text-right font-mono text-fg-3">{share(c.requests, total)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
