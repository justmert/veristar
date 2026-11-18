// the one time series chart of the console: bars (stacked for several
// series) or lines over utc buckets, the previous period dashed behind them,
// partial periods hatched with a legend entry, a readout of exact values on
// hover and on the arrow keys, and a table view, so no value depends on
// hovering or on colour. one y axis only.

import { cn } from "@veristar/brand/cn";
import { fade, transition } from "@veristar/brand/motion";
import { Sheet } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { useRevealOnce } from "@/components/motion";
import { EmptyState, ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { formatChange, formatCompact, formatCount, MISSING, niceTicks } from "@/lib/format";

// series colours are the data roles; warning and critical only for series
// that are a status (4xx, 5xx)
export type SeriesTone =
  | "series-1"
  | "series-2"
  | "series-3"
  | "series-4"
  | "series-5"
  | "warning"
  | "critical";

export type TimeSeries = {
  key: string;
  label: string;
  tone: SeriesTone;
  // one value per bucket; null is "no value" (a gap in a line, nothing in a
  // bar), never zero
  values: (number | null)[];
};

export type TimeBucket = {
  // the bucket start, iso; also the key of a marker
  at: string;
  // the bucket as a reader names it in the readout and the table, with UTC
  label: string;
  // the short axis label
  tick: string;
  // a period that has not ended (or began before the range): hatched, so a
  // half day never reads as a drop
  partial?: boolean;
};

export type ChartState = "loading" | "error" | "empty" | "data";

const FILL: Record<SeriesTone, string> = {
  "series-1": "fill-series-1",
  "series-2": "fill-series-2",
  "series-3": "fill-series-3",
  "series-4": "fill-series-4",
  "series-5": "fill-series-5",
  warning: "fill-warning",
  critical: "fill-critical",
};
const STROKE: Record<SeriesTone, string> = {
  "series-1": "stroke-series-1",
  "series-2": "stroke-series-2",
  "series-3": "stroke-series-3",
  "series-4": "stroke-series-4",
  "series-5": "stroke-series-5",
  warning: "stroke-warning",
  critical: "stroke-critical",
};
const SWATCH: Record<SeriesTone, string> = {
  "series-1": "bg-series-1",
  "series-2": "bg-series-2",
  "series-3": "bg-series-3",
  "series-4": "bg-series-4",
  "series-5": "bg-series-5",
  warning: "bg-warning",
  critical: "bg-critical",
};

const AXIS_W = 48;

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w !== undefined) setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return { ref, width };
}

// a bar with its top corners rounded (only the top segment of a stack)
function barPath(x: number, y: number, w: number, h: number, round: boolean): string {
  if (h <= 0) return "";
  const r = round ? Math.min(2, w / 2, h) : 0;
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

// the runs of a line between its gaps
function segments(values: (number | null)[]): { i: number; v: number }[][] {
  const out: { i: number; v: number }[][] = [];
  let run: { i: number; v: number }[] = [];
  values.forEach((v, i) => {
    if (v === null || v === undefined) {
      if (run.length) out.push(run);
      run = [];
    } else run.push({ i, v });
  });
  if (run.length) out.push(run);
  return out;
}

export type TimeChartProps = {
  title: string;
  description?: ReactNode;
  // h2 on a page, h3 inside a section that has its own heading
  headingLevel?: 2 | 3;
  // bars count things per bucket; lines show rates and latencies
  kind?: "bar" | "line";
  buckets: TimeBucket[];
  series: TimeSeries[];
  // the previous period of the same length, bucket by bucket, compared with
  // the sum of the series (bars) or with the one series (lines)
  previous?: (number | null)[] | null;
  previousLabel?: string;
  // the exact value in the readout and the table
  format?: (n: number) => string;
  // the short value on the axis
  axisFormat?: (n: number) => string;
  // decides the colour of the change in the readout
  higherIsBetter?: boolean;
  state: ChartState;
  error?: unknown;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyText?: string;
  // shown when every bucket is partial: a chart of one running period
  // would read as a drop
  noCompleteTitle?: string;
  noCompleteText?: string;
  // a dashed line at one bucket, such as an award date
  marker?: { at: string; label: string } | null;
  // several such lines, one per compared project; each label sits one line
  // below the previous one so they never overlap
  markers?: { at: string; label: string }[];
  // a value line the data is capped at, such as the 10 s latency bucket
  ceiling?: { value: number; label: string } | null;
  // the plot height in px
  height?: number;
  // controls next to the legend, such as a metric switch
  actions?: ReactNode;
  className?: string;
};

export function TimeChart({
  title,
  description,
  headingLevel = 2,
  kind = "bar",
  buckets,
  series,
  previous,
  previousLabel = "Previous period",
  format = formatCount,
  axisFormat = formatCompact,
  higherIsBetter = true,
  state,
  error,
  onRetry,
  emptyTitle = "No data in this range",
  emptyText = "Values show here a few minutes after data arrives.",
  noCompleteTitle = "No complete period yet",
  noCompleteText = "The first period is still running. Its value shows here when it ends.",
  marker,
  markers,
  ceiling,
  height = 200,
  actions,
  className,
}: TimeChartProps) {
  const [showPrev, setShowPrev] = useState(true);
  const [asTable, setAsTable] = useState(false);
  const [hover, setHover] = useState<number | null>(null);
  const { ref, width } = useWidth<HTMLDivElement>();
  const reveal = useRevealOnce<HTMLDivElement>();
  const uid = useId().replace(/:/g, "");
  const H = height;
  const n = buckets.length;
  const noComplete = state === "data" && n > 0 && buckets.every((b) => b.partial);
  const view: ChartState | "no-complete" = noComplete ? "no-complete" : state;
  const hasPrev = view === "data" && !!previous && previous.length > 0;
  const hasPartial = view === "data" && buckets.some((b) => b.partial);
  const Heading = headingLevel === 2 ? "h2" : "h3";

  // the value a bucket compares with the previous period
  const totals = buckets.map((_, i) => {
    if (kind === "line") return series[0]?.values[i] ?? null;
    let sum: number | null = null;
    for (const s of series) {
      const v = s.values[i];
      if (v !== null && v !== undefined) sum = (sum ?? 0) + v;
    }
    return sum;
  });
  const prevVals = hasPrev && showPrev ? (previous ?? []) : [];
  const dataMax =
    kind === "bar"
      ? Math.max(0, ...totals.map((v) => v ?? 0))
      : Math.max(0, ...series.flatMap((s) => s.values.map((v) => v ?? 0)));
  const ticks = niceTicks(
    Math.max(dataMax, ...prevVals.map((v) => v ?? 0), ceiling ? ceiling.value : 0),
  );
  const top = ticks[ticks.length - 1] ?? 1;
  const plotW = Math.max(0, width - AXIS_W);
  const slot = n > 0 ? plotW / n : 0;
  const gap = slot > 6 ? 2 : slot > 3 ? 1 : 0;
  const barW = Math.max(1, slot - gap);
  const y = (v: number) => H - (Math.min(v, top) / top) * H;
  const cx = (i: number) => AXIS_W + slot * i + slot / 2;
  const marks = [...(marker ? [marker] : []), ...(markers ?? [])]
    .map((m) => ({ ...m, i: buckets.findIndex((b) => b.at.startsWith(m.at)) }))
    .filter((m) => m.i >= 0);

  function change(i: number): { text: string; good: boolean | null } | null {
    if (!hasPrev) return null;
    const cur = totals[i];
    const prev = previous?.[i];
    if (cur === null || cur === undefined || prev === null || prev === undefined) return null;
    if (prev === 0) return cur === 0 ? { text: "no change", good: null } : null;
    const r = (cur - prev) / prev;
    if (Math.abs(r) < 0.0005) return { text: "no change", good: null };
    return { text: formatChange(r), good: r > 0 === higherIsBetter };
  }

  const show = (v: number | null | undefined) =>
    v === null || v === undefined ? MISSING : format(v);

  function move(e: React.PointerEvent<SVGSVGElement>) {
    const box = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - box.left - AXIS_W;
    if (x < 0 || slot === 0) return setHover(null);
    setHover(Math.min(n - 1, Math.max(0, Math.floor(x / slot))));
  }

  function key(e: React.KeyboardEvent) {
    if (n === 0) return;
    const step = { ArrowRight: 1, ArrowLeft: -1, End: n, Home: -n }[e.key] ?? 0;
    if (!step) {
      if (e.key === "Escape") setHover(null);
      return;
    }
    e.preventDefault();
    setHover((h) => Math.min(n - 1, Math.max(0, (h ?? n - 1) + step)));
  }

  const hb = hover !== null ? buckets[hover] : undefined;
  const delta = hover !== null ? change(hover) : null;
  const readout =
    hover !== null && hb
      ? [
          `${hb.label}${hb.partial ? ", partial" : ""}`,
          ...series.map((s) => `${s.label} ${show(s.values[hover])}`),
          ...(hasPrev ? [`${previousLabel} ${show(previous?.[hover])}`] : []),
          ...(delta ? [`change ${delta.text}`] : []),
        ].join(", ")
      : "";
  const mid = Math.floor((n - 1) / 2);

  return (
    <section
      aria-label={title}
      className={cn("flex min-w-0 flex-col rounded-lg border border-line bg-raised", className)}
    >
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 px-4 pt-4 pb-2 sm:px-5">
        <div className="flex min-w-0 flex-col gap-1">
          <Heading className="text-title text-fg">{title}</Heading>
          {description ? <p className="text-small text-fg-2">{description}</p> : null}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {actions}
          {view === "data" && (series.length > 1 || hasPartial) ? (
            <ul aria-label="Legend" className="flex flex-wrap items-center gap-x-3 gap-y-1">
              {series.length > 1
                ? series.map((s) => (
                    <li key={s.key} className="flex items-center gap-1 text-caption text-fg-2">
                      <span aria-hidden className={cn("size-2 rounded-bar", SWATCH[s.tone])} />
                      {s.label}
                    </li>
                  ))
                : null}
              {hasPartial ? (
                <li
                  data-legend="partial"
                  className="flex items-center gap-1 text-caption text-fg-2"
                >
                  <svg aria-hidden width="10" height="10" className="rounded-bar">
                    <rect width="10" height="10" fill={`url(#${uid}-legend)`} />
                  </svg>
                  Partial
                </li>
              ) : null}
            </ul>
          ) : null}
          {hasPrev ? (
            <Button
              type="button"
              variant="outline"
              size="xs"
              aria-pressed={showPrev}
              onClick={() => setShowPrev((v) => !v)}
              className="max-lg:h-(--vs-control-touch)"
            >
              <svg aria-hidden width="14" height="6" className="overflow-visible">
                <line
                  x1="0"
                  x2="14"
                  y1="3"
                  y2="3"
                  className="stroke-series-prev"
                  strokeWidth="1.5"
                  strokeDasharray="4 3"
                />
              </svg>
              {previousLabel}
            </Button>
          ) : null}
          {view === "data" ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="max-lg:size-(--vs-control-touch)"
              aria-label="Show as table"
              aria-pressed={asTable}
              onClick={() => setAsTable((v) => !v)}
            >
              <Sheet aria-hidden />
            </Button>
          ) : null}
        </div>
      </header>

      <div ref={ref} className="px-4 pb-4 sm:px-5">
        {/* one pattern for the legend swatch, so the swatch matches the bars */}
        <svg aria-hidden width="0" height="0" className="absolute">
          <defs>
            <pattern
              id={`${uid}-legend`}
              width="4"
              height="4"
              patternUnits="userSpaceOnUse"
              patternTransform="rotate(45)"
            >
              <rect width="4" height="4" className="fill-series-prev" />
              <rect width="1.5" height="4" fill="var(--vs-series-partial)" />
            </pattern>
          </defs>
        </svg>
        <AnimatePresence mode="wait" initial={false}>
          {view === "loading" ? (
            <motion.div key="loading" {...fade} role="status" aria-busy="true">
              <span className="sr-only">Loading the chart</span>
              <div aria-hidden className="flex items-end gap-1 pl-10" style={{ height: H }}>
                {Array.from({ length: 24 }, (_, i) => (
                  <Bone
                    key={i}
                    className="flex-1 rounded-b-none"
                    style={{ height: `${30 + ((i * 37) % 55)}%` }}
                  />
                ))}
              </div>
            </motion.div>
          ) : view === "error" ? (
            <motion.div key="error" {...fade}>
              <ErrorState
                compact
                title="The chart did not load"
                error={error}
                onRetry={onRetry}
                className="border-0"
              />
            </motion.div>
          ) : view === "empty" ? (
            <motion.div key="empty" {...fade}>
              <EmptyState compact title={emptyTitle}>
                {emptyText}
              </EmptyState>
            </motion.div>
          ) : view === "no-complete" ? (
            <motion.div key="no-complete" {...fade}>
              <EmptyState compact title={noCompleteTitle}>
                {noCompleteText}
              </EmptyState>
            </motion.div>
          ) : asTable ? (
            <motion.div key="table" {...fade} className="max-h-80 overflow-y-auto">
              <table className="w-full border-separate border-spacing-0 text-small tabular">
                <caption className="sr-only">{title}</caption>
                <thead className="sticky top-0 bg-raised">
                  <tr>
                    <th
                      scope="col"
                      className="h-9 border-b border-line px-2 text-left text-caption font-medium text-fg-2"
                    >
                      Time (UTC)
                    </th>
                    {series.map((s) => (
                      <th
                        key={s.key}
                        scope="col"
                        className="h-9 border-b border-line px-2 text-right text-caption font-medium text-fg-2"
                      >
                        {s.label}
                      </th>
                    ))}
                    {hasPrev ? (
                      <th
                        scope="col"
                        className="h-9 border-b border-line px-2 text-right text-caption font-medium text-fg-2"
                      >
                        {previousLabel}
                      </th>
                    ) : null}
                  </tr>
                </thead>
                <tbody>
                  {buckets.map((b, i) => (
                    <tr key={b.at} data-bucket={b.at} className="hover:bg-hover">
                      <th
                        scope="row"
                        className="h-(--vs-row) border-b border-line-subtle px-2 text-left font-normal text-fg-2"
                      >
                        {b.label}
                        {b.partial ? " (partial)" : ""}
                      </th>
                      {series.map((s) => (
                        <td
                          key={s.key}
                          className="border-b border-line-subtle px-2 text-right font-mono text-fg"
                        >
                          {show(s.values[i])}
                        </td>
                      ))}
                      {hasPrev ? (
                        <td className="border-b border-line-subtle px-2 text-right font-mono text-fg-2">
                          {show(previous?.[i])}
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </motion.div>
          ) : (
            <motion.div key="chart" {...fade}>
              <div ref={reveal.ref} className="relative pt-3">
                {/* biome-ignore lint/a11y/useSemanticElements: a chart is not a fieldset of controls */}
                <div
                  // biome-ignore lint/a11y/noNoninteractiveTabindex: the plot is read with the arrow keys
                  tabIndex={0}
                  role="group"
                  aria-label={`${title}. Use the arrow keys to read each bucket.`}
                  onKeyDown={key}
                  onBlur={() => setHover(null)}
                  className="rounded-sm focus-ring"
                >
                  <svg
                    width={width}
                    height={H + 1}
                    aria-hidden
                    className="block overflow-visible"
                    onPointerMove={move}
                    onPointerLeave={() => setHover(null)}
                  >
                    <defs>
                      {series.map((s) => (
                        <pattern
                          key={s.key}
                          id={`${uid}-${s.key}`}
                          width="4"
                          height="4"
                          patternUnits="userSpaceOnUse"
                          patternTransform="rotate(45)"
                        >
                          <rect width="4" height="4" className={FILL[s.tone]} />
                          <rect width="1.5" height="4" fill="var(--vs-series-partial)" />
                        </pattern>
                      ))}
                      <pattern
                        id={`${uid}-band`}
                        width="4"
                        height="4"
                        patternUnits="userSpaceOnUse"
                        patternTransform="rotate(45)"
                      >
                        <rect width="1.5" height="4" fill="var(--vs-series-partial)" />
                      </pattern>
                    </defs>
                    {kind === "line"
                      ? buckets.map((b, i) =>
                          b.partial ? (
                            // a line cannot be hatched, so the running period
                            // is a hatched band behind it, as in the legend
                            <rect
                              key={`band-${b.at}`}
                              data-partial=""
                              x={AXIS_W + slot * i}
                              y="0"
                              width={slot}
                              height={H}
                              fill={`url(#${uid}-band)`}
                              opacity="0.5"
                            />
                          ) : null,
                        )
                      : null}
                    {ticks.map((t) => (
                      <g key={t}>
                        <line
                          x1={AXIS_W}
                          x2={width}
                          y1={Math.round(y(t)) + 0.5}
                          y2={Math.round(y(t)) + 0.5}
                          className="stroke-grid"
                        />
                        <text
                          x={AXIS_W - 8}
                          y={y(t)}
                          dy="0.32em"
                          textAnchor="end"
                          className="fill-fg-3 font-mono text-axis"
                        >
                          {axisFormat(t)}
                        </text>
                      </g>
                    ))}
                    {ceiling ? (
                      <g data-ceiling="">
                        <line
                          x1={AXIS_W}
                          x2={width}
                          y1={y(ceiling.value)}
                          y2={y(ceiling.value)}
                          className="stroke-line-strong"
                          strokeDasharray="4 4"
                        />
                        <text
                          x={width}
                          y={y(ceiling.value) - 4}
                          textAnchor="end"
                          className="fill-fg-3 text-axis"
                        >
                          {ceiling.label}
                        </text>
                      </g>
                    ) : null}
                    {kind === "bar" ? (
                      <motion.g
                        initial={{ scaleY: 0, opacity: 0 }}
                        animate={reveal.seen ? { scaleY: 1, opacity: 1 } : undefined}
                        transition={transition.arrival}
                        style={{ originY: 1 }}
                      >
                        {buckets.map((b, i) => {
                          let base = 0;
                          const last = series.reduce(
                            (k, s, j) => ((s.values[i] ?? 0) > 0 ? j : k),
                            -1,
                          );
                          return (
                            <g
                              key={b.at}
                              data-partial={b.partial ? "" : undefined}
                              className={cn(
                                "transition-opacity duration-(--vs-duration-press)",
                                hover !== null && hover !== i && "opacity-55",
                              )}
                            >
                              {series.map((s, j) => {
                                const v = s.values[i] ?? 0;
                                const y0 = y(base);
                                base += v;
                                const y1 = y(base);
                                const d = barPath(
                                  AXIS_W + slot * i + gap / 2,
                                  y1,
                                  barW,
                                  y0 - y1,
                                  j === last,
                                );
                                if (!d) return null;
                                return b.partial ? (
                                  <path key={s.key} d={d} fill={`url(#${uid}-${s.key})`} />
                                ) : (
                                  <path key={s.key} d={d} className={FILL[s.tone]} />
                                );
                              })}
                            </g>
                          );
                        })}
                      </motion.g>
                    ) : (
                      <motion.g
                        initial={{ opacity: 0 }}
                        animate={reveal.seen ? { opacity: 1 } : undefined}
                        transition={transition.arrival}
                      >
                        {series.map((s) =>
                          segments(s.values).map((run) =>
                            run.length === 1 ? (
                              <circle
                                key={`${s.key}-${run[0]?.i}`}
                                data-series={s.key}
                                cx={cx(run[0]?.i ?? 0)}
                                cy={y(run[0]?.v ?? 0)}
                                r="2"
                                className={FILL[s.tone]}
                              />
                            ) : (
                              <polyline
                                key={`${s.key}-${run[0]?.i}`}
                                data-series={s.key}
                                points={run.map((p) => `${cx(p.i)},${y(p.v)}`).join(" ")}
                                fill="none"
                                className={STROKE[s.tone]}
                                strokeWidth="2"
                                strokeLinejoin="round"
                              />
                            ),
                          ),
                        )}
                      </motion.g>
                    )}
                    {prevVals.length
                      ? segments(prevVals).map((run) => (
                          <polyline
                            key={`prev-${run[0]?.i}`}
                            data-previous=""
                            points={run.map((p) => `${cx(p.i)},${y(p.v)}`).join(" ")}
                            fill="none"
                            className="stroke-series-prev"
                            strokeWidth="1.5"
                            strokeDasharray="4 3"
                            strokeLinejoin="round"
                          />
                        ))
                      : null}
                    {marks.map((m, k) => (
                      <g key={`${m.at}-${m.label}`} data-marker={m.at}>
                        <line
                          x1={cx(m.i)}
                          x2={cx(m.i)}
                          y1="0"
                          y2={H}
                          className="stroke-accent-text"
                          strokeDasharray="3 3"
                        />
                        <text
                          x={cx(m.i) + (cx(m.i) > width / 2 ? -4 : 4)}
                          y={10 + k * 12}
                          textAnchor={cx(m.i) > width / 2 ? "end" : "start"}
                          className="fill-fg-2 text-axis"
                        >
                          {m.label}
                        </text>
                      </g>
                    ))}
                    {hover !== null ? (
                      <line
                        x1={cx(hover)}
                        x2={cx(hover)}
                        y1="0"
                        y2={H}
                        className="stroke-line-strong"
                      />
                    ) : null}
                  </svg>
                </div>
                <span className="sr-only" aria-live="polite" data-readout="">
                  {readout}
                </span>
                {hover !== null && hb ? (
                  <Readout
                    x={cx(hover)}
                    flip={cx(hover) > width / 2}
                    bucket={hb}
                    rows={series.map((s) => ({
                      key: s.key,
                      label: s.label,
                      tone: s.tone,
                      value: show(s.values[hover]),
                    }))}
                    previous={hasPrev ? show(previous?.[hover]) : null}
                    previousLabel={previousLabel}
                    delta={delta}
                  />
                ) : null}
              </div>
              {n > 0 ? (
                <div className="mt-2 flex justify-between pl-12 font-mono text-axis text-fg-3">
                  <span>{buckets[0]?.tick}</span>
                  {n > 2 ? <span className="max-sm:hidden">{buckets[mid]?.tick}</span> : null}
                  <span>{buckets[n - 1]?.tick} UTC</span>
                </div>
              ) : null}
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </section>
  );
}

function Readout({
  x,
  flip,
  bucket,
  rows,
  previous,
  previousLabel,
  delta,
}: {
  x: number;
  flip: boolean;
  bucket: TimeBucket;
  rows: { key: string; label: string; tone: SeriesTone; value: string }[];
  previous: string | null;
  previousLabel: string;
  delta: { text: string; good: boolean | null } | null;
}) {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute top-2 z-(--vs-layer-raised) flex min-w-44 animate-fade-in flex-col gap-1 rounded-md border border-line-strong bg-overlay px-3 py-2 text-small shadow-overlay"
      style={{ left: x, transform: flip ? "translateX(calc(-100% - 12px))" : "translateX(12px)" }}
    >
      <span className="text-caption text-fg-2">
        {bucket.label}
        {bucket.partial ? ", so far" : ""}
      </span>
      {rows.map((r) => (
        <span key={r.key} className="flex items-center gap-2">
          <span aria-hidden className={cn("size-2 rounded-bar", SWATCH[r.tone])} />
          <span className="flex-1 text-fg-2">{r.label}</span>
          <span className="font-mono text-fg tabular">{r.value}</span>
        </span>
      ))}
      {previous !== null ? (
        <span className="flex items-center gap-2 border-t border-line pt-1">
          <span className="flex-1 text-fg-3">{previousLabel}</span>
          <span className="font-mono text-fg-2 tabular">{previous}</span>
        </span>
      ) : null}
      {delta ? (
        <span className="flex items-center gap-2">
          <span className="flex-1 text-fg-3">Change</span>
          <span
            className={cn(
              "font-mono tabular",
              delta.good === null ? "text-fg-2" : delta.good ? "text-success" : "text-critical",
            )}
          >
            {delta.text}
          </span>
        </span>
      ) : null}
    </div>
  );
}
