// the time series card (system v2, R2) and the web traffic section built on
// it: outcome counts of the ingest route per utc hour or day, the previous
// period dashed behind them, a crosshair readout on hover and on the arrow
// keys, and a table view so no value depends on hovering or on colour.

import { type QueryKey, useQueries } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade, transition } from "@veristar/brand/motion";
import { CircleAlert, RotateCcw, Sheet, SquareChartGantt } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";
import { api } from "@/api/endpoints";
import { ingestDayQuery, ingestMonthQuery, keys } from "@/api/queries";
import type { Ingest, Site } from "@/api/schemas";
import { useRevealOnce } from "@/components/motion";
import { Sparkline, StatTile, type Trend } from "@/components/stat-tile";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errorText } from "@/lib/errors";
import { formatCount } from "@/lib/format";
import { sumRejected } from "@/lib/outcomes";

// ---------------------------------------------------------------- the chart

export type SeriesTone = "series-1" | "series-2" | "series-3" | "warning" | "critical";

const FILL: Record<SeriesTone, string> = {
  "series-1": "fill-series-1",
  "series-2": "fill-series-2",
  "series-3": "fill-series-3",
  warning: "fill-warning",
  critical: "fill-critical",
};
const SWATCH: Record<SeriesTone, string> = {
  "series-1": "bg-series-1",
  "series-2": "bg-series-2",
  "series-3": "bg-series-3",
  warning: "bg-warning",
  critical: "bg-critical",
};

export type ChartSeries = { key: string; label: string; tone: SeriesTone; values: number[] };
export type ChartBucket = {
  at: string;
  // the bucket as a reader names it in the readout, with UTC
  label: string;
  // the short axis label
  tick: string;
  // a day that has not ended (or began before the range): drawn hatched so
  // a half day never reads as a drop
  partial?: boolean;
};

export type ChartState = "loading" | "error" | "empty" | "data";

const PLOT_H = 200;
const AXIS_W = 48;

// 3 to 5 round steps from zero to at least the largest value
export function niceTicks(max: number): number[] {
  if (!(max > 0)) return [0, 1];
  const rough = max / 3;
  const mag = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= rough) ?? 10 * mag;
  const top = Math.ceil(max / step) * step;
  const out: number[] = [];
  for (let v = 0; v <= top + step / 2; v += step) out.push(Number(v.toPrecision(12)));
  return out;
}

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });

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

// a bar with its top corners rounded by 2 px (only the top segment of a stack)
function barPath(x: number, y: number, w: number, h: number, round: boolean): string {
  if (h <= 0) return "";
  const r = round ? Math.min(2, w / 2, h) : 0;
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

export function TimeChart({
  title,
  description,
  buckets,
  series,
  previous,
  previousLabel = "Previous period",
  format = formatCount,
  higherIsBetter = true,
  state,
  error,
  onRetry,
  emptyTitle = "No events in this range",
  emptyText = "Counts appear here a few minutes after events arrive.",
  className,
}: {
  title: string;
  description?: ReactNode;
  buckets: ChartBucket[];
  series: ChartSeries[];
  // the previous period of the same length, compared bucket by bucket with
  // the sum of the series
  previous?: number[] | null;
  previousLabel?: string;
  format?: (n: number) => string;
  higherIsBetter?: boolean;
  state: ChartState;
  error?: unknown;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyText?: string;
  className?: string;
}) {
  const [showPrev, setShowPrev] = useState(true);
  const [asTable, setAsTable] = useState(false);
  const [hover, setHover] = useState<number | null>(null);
  const { ref, width } = useWidth<HTMLDivElement>();
  const reveal = useRevealOnce<HTMLDivElement>();
  const uid = useId().replace(/:/g, "");
  const hasPrev = !!previous && previous.length > 0 && state === "data";

  const n = buckets.length;
  const totals = buckets.map((_, i) => series.reduce((a, s) => a + (s.values[i] ?? 0), 0));
  const prevVals = hasPrev && showPrev ? (previous ?? []) : [];
  const ticks = niceTicks(Math.max(0, ...totals, ...prevVals));
  const top = ticks[ticks.length - 1] ?? 1;
  const plotW = Math.max(0, width - AXIS_W);
  const slot = n > 0 ? plotW / n : 0;
  const gap = slot > 6 ? 2 : slot > 3 ? 1 : 0;
  const barW = Math.max(1, slot - gap);
  const y = (v: number) => PLOT_H - (v / top) * PLOT_H;
  const cx = (i: number) => AXIS_W + slot * i + slot / 2;

  function deltaText(i: number): { text: string; good: boolean | null } | null {
    if (!hasPrev) return null;
    const cur = totals[i] ?? 0;
    const prev = previous?.[i] ?? 0;
    if (prev === 0) return cur === 0 ? { text: "no change", good: null } : null;
    const r = (cur - prev) / prev;
    if (Math.abs(r) < 0.0005) return { text: "no change", good: null };
    const up = r > 0;
    return {
      text: `${up ? "+" : ""}${(r * 100).toFixed(1)}%`,
      good: up === higherIsBetter,
    };
  }

  function move(e: React.PointerEvent<SVGSVGElement>) {
    const box = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - box.left - AXIS_W;
    if (x < 0 || slot === 0) return setHover(null);
    setHover(Math.min(n - 1, Math.max(0, Math.floor(x / slot))));
  }

  function key(e: React.KeyboardEvent) {
    if (n === 0) return;
    const step =
      e.key === "ArrowRight"
        ? 1
        : e.key === "ArrowLeft"
          ? -1
          : e.key === "End"
            ? n
            : e.key === "Home"
              ? -n
              : 0;
    if (!step) {
      if (e.key === "Escape") setHover(null);
      return;
    }
    e.preventDefault();
    setHover((h) => Math.min(n - 1, Math.max(0, (h ?? n - 1) + step)));
  }

  const hb = hover !== null ? buckets[hover] : undefined;
  const readout =
    hover !== null && hb
      ? `${hb.label}${hb.partial ? ", partial" : ""}: ${series
          .map((s) => `${s.label} ${format(s.values[hover] ?? 0)}`)
          .join(
            ", ",
          )}${hasPrev ? `, ${previousLabel.toLowerCase()} ${format(previous?.[hover] ?? 0)}` : ""}`
      : "";
  const mid = Math.floor((n - 1) / 2);

  return (
    <section
      aria-label={title}
      className={cn("flex min-w-0 flex-col rounded-lg border border-line bg-raised", className)}
    >
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 px-4 pt-3.5 pb-2 sm:px-5">
        <div className="flex min-w-0 flex-col gap-0.5">
          <h2 className="text-title text-fg">{title}</h2>
          {description ? <p className="text-small text-fg-2">{description}</p> : null}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {series.length > 1 ? (
            <ul aria-label="Legend" className="flex flex-wrap items-center gap-x-3 gap-y-1">
              {series.map((s) => (
                <li key={s.key} className="flex items-center gap-1.5 text-caption text-fg-2">
                  <span aria-hidden className={cn("size-2.5 rounded-bar", SWATCH[s.tone])} />
                  {s.label}
                </li>
              ))}
            </ul>
          ) : null}
          {hasPrev ? (
            <button
              type="button"
              aria-pressed={showPrev}
              onClick={() => setShowPrev((v) => !v)}
              className={cn(
                "inline-flex h-7 items-center gap-1.5 rounded-md border px-2 text-caption transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-(--vs-control-touch)",
                showPrev
                  ? "border-fg-disabled bg-selected text-fg"
                  : "border-line-strong text-fg-2 hover:bg-hover hover:text-fg",
              )}
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
            </button>
          ) : null}
          {state === "data" ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="max-lg:size-(--vs-control-touch)"
              aria-label={asTable ? "Show as chart" : "Show as table"}
              aria-pressed={asTable}
              onClick={() => setAsTable((v) => !v)}
            >
              {asTable ? <SquareChartGantt aria-hidden /> : <Sheet aria-hidden />}
            </Button>
          ) : null}
        </div>
      </header>

      <div ref={ref} className="px-4 pb-4 sm:px-5">
        <AnimatePresence mode="wait" initial={false}>
          {state === "data" && asTable ? (
            <motion.div key="table" {...fade} className="max-h-80 overflow-y-auto">
              <Table>
                <TableHeader className="sticky top-0 bg-raised">
                  <TableRow>
                    <TableHead>Time (UTC)</TableHead>
                    {series.map((s) => (
                      <TableHead key={s.key} className="text-right">
                        {s.label}
                      </TableHead>
                    ))}
                    {hasPrev ? <TableHead className="text-right">{previousLabel}</TableHead> : null}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {buckets.map((b, i) => (
                    <TableRow key={b.at}>
                      <TableCell className="text-fg-2">
                        {b.label}
                        {b.partial ? " (partial)" : ""}
                      </TableCell>
                      {series.map((s) => (
                        <TableCell key={s.key} className="text-right font-mono">
                          {format(s.values[i] ?? 0)}
                        </TableCell>
                      ))}
                      {hasPrev ? (
                        <TableCell className="text-right font-mono text-fg-2">
                          {format(previous?.[i] ?? 0)}
                        </TableCell>
                      ) : null}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </motion.div>
          ) : (
            <motion.div key={`chart-${state}`} {...fade}>
              <div>
                {state === "loading" ? (
                  <div
                    aria-busy="true"
                    className="flex items-end gap-0.5 pl-12"
                    style={{ height: PLOT_H }}
                  >
                    <span className="sr-only">Loading the chart</span>
                    {Array.from({ length: 30 }, (_, i) => (
                      <Skeleton
                        key={i}
                        className="flex-1 rounded-b-none"
                        style={{ height: `${30 + ((i * 37) % 55)}%` }}
                      />
                    ))}
                  </div>
                ) : state === "error" ? (
                  <div
                    role="alert"
                    className="flex flex-col items-center justify-center gap-3 text-center"
                    style={{ height: PLOT_H }}
                  >
                    <span className="flex items-center gap-1.5 text-body text-fg">
                      <CircleAlert aria-hidden className="size-4 text-critical" />
                      The chart could not be loaded
                    </span>
                    {error ? (
                      <span className="max-w-sm text-small text-fg-2">{errorText(error)}</span>
                    ) : null}
                    {onRetry ? (
                      <Button variant="outline" size="sm" onClick={onRetry}>
                        <RotateCcw aria-hidden />
                        Try again
                      </Button>
                    ) : null}
                  </div>
                ) : state === "empty" ? (
                  <div className="relative" style={{ height: PLOT_H }}>
                    <svg aria-hidden width="100%" height={PLOT_H} className="absolute inset-0">
                      <line
                        x1={AXIS_W}
                        x2="100%"
                        y1={PLOT_H - 0.5}
                        y2={PLOT_H - 0.5}
                        className="stroke-line-strong"
                        strokeDasharray="4 4"
                      />
                    </svg>
                    <div
                      role="status"
                      className="absolute inset-0 flex flex-col items-center justify-center gap-1 pl-12 text-center"
                    >
                      <span className="text-body text-fg">{emptyTitle}</span>
                      <span className="max-w-sm text-small text-fg-2">{emptyText}</span>
                    </div>
                  </div>
                ) : (
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
                        height={PLOT_H + 1}
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
                              <rect width="1.5" height="4" className={FILL[s.tone]} />
                            </pattern>
                          ))}
                        </defs>
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
                              {compact.format(t)}
                            </text>
                          </g>
                        ))}
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
                        {prevVals.length ? (
                          <polyline
                            points={prevVals.map((v, i) => `${cx(i)},${y(v)}`).join(" ")}
                            fill="none"
                            className="stroke-series-prev"
                            strokeWidth="1.5"
                            strokeDasharray="4 3"
                            strokeLinejoin="round"
                          />
                        ) : null}
                        {hover !== null ? (
                          <line
                            x1={cx(hover)}
                            x2={cx(hover)}
                            y1="0"
                            y2={PLOT_H}
                            className="stroke-line-strong"
                          />
                        ) : null}
                        {hover !== null && prevVals.length ? (
                          <circle
                            cx={cx(hover)}
                            cy={y(prevVals[hover] ?? 0)}
                            r="4"
                            className="fill-raised stroke-series-prev"
                            strokeWidth="1.5"
                          />
                        ) : null}
                      </svg>
                    </div>
                    <span className="sr-only" aria-live="polite">
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
                          value: format(s.values[hover] ?? 0),
                        }))}
                        previous={hasPrev ? format(previous?.[hover] ?? 0) : null}
                        previousLabel={previousLabel}
                        delta={deltaText(hover)}
                      />
                    ) : null}
                  </div>
                )}
              </div>
              {n > 0 ? (
                <div className="mt-1.5 flex justify-between pl-12 font-mono text-axis text-fg-3">
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
  bucket: ChartBucket;
  rows: { key: string; label: string; tone: SeriesTone; value: string }[];
  previous: string | null;
  previousLabel: string;
  delta: { text: string; good: boolean | null } | null;
}) {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute top-2 z-10 flex min-w-44 animate-fade-in flex-col gap-1.5 rounded-md border border-line-strong bg-overlay px-3 py-2 text-small shadow-overlay"
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
        <span className="flex items-center gap-2 border-t border-line pt-1.5">
          <span className="flex-1 text-fg-3">{previousLabel}</span>
          <span className="font-mono text-fg-2 tabular">{previous}</span>
        </span>
      ) : null}
      {delta ? (
        <span className="flex items-center gap-2">
          <span className="flex-1 text-fg-3">vs previous</span>
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

// ---------------------------------------------------------- the web traffic

export type WebRange = "24h" | "30d";

export const WEB_RANGES: readonly { value: WebRange; label: string }[] = [
  { value: "24h", label: "24h" },
  { value: "30d", label: "30d" },
];

export function readWebRange(v: string | null): WebRange {
  return v === "24h" ? "24h" : "30d";
}

const PERIOD: Record<WebRange, string> = { "24h": "previous 24 hours", "30d": "previous 30 days" };

const HOUR = 3600_000;
const DAY = 24 * HOUR;

const tickHour = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});
const tickDay = new Intl.DateTimeFormat("en", { month: "short", day: "numeric", timeZone: "UTC" });
const labelDay = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  year: "numeric",
  timeZone: "UTC",
});

const iso = (t: number) => new Date(t).toISOString().replace(/\.\d{3}Z$/, "Z");

// the buckets of an ingest answer: every hour or utc day the window touches.
// the route omits empty buckets, which are zero, not unknown. a 30 day
// window starts and ends inside a day, so those two days are partial.
export function ingestBuckets(i: Pick<Ingest, "from" | "to" | "bucket">): ChartBucket[] {
  const from = Date.parse(i.from);
  const to = Date.parse(i.to);
  const out: ChartBucket[] = [];
  if (i.bucket === "hour") {
    for (let t = from; t < to; t += HOUR) {
      const d = new Date(t);
      out.push({
        at: iso(t),
        tick: tickHour.format(d),
        label: `${tickDay.format(d)}, ${tickHour.format(d)} UTC`,
      });
    }
    return out;
  }
  for (let t = Math.floor(from / DAY) * DAY; t < to; t += DAY) {
    const d = new Date(t);
    out.push({
      at: iso(t),
      tick: tickDay.format(d),
      label: `${labelDay.format(d)}, UTC`,
      partial: t < from || t + DAY > to,
    });
  }
  return out;
}

type Outcomes = {
  accepted: number;
  quarantined: number;
  bot: number;
  rejected: number;
  all: number;
};

const zero = (): Outcomes => ({ accepted: 0, quarantined: 0, bot: 0, rejected: 0, all: 0 });

function addCounts(o: Outcomes, c: Record<string, number>) {
  o.accepted += c.accepted ?? 0;
  o.quarantined += c.quarantined ?? 0;
  o.bot += c.bot ?? 0;
  o.rejected += sumRejected(c);
  for (const v of Object.values(c)) o.all += v;
}

export const heldBack = (o: Outcomes) => o.quarantined + o.bot + o.rejected;

// per bucket outcomes of several sites, by bucket start
function fold(answers: Ingest[], buckets: ChartBucket[]): Outcomes[] {
  const index = new Map(buckets.map((b, i) => [Date.parse(b.at), i]));
  const out = buckets.map(zero);
  for (const a of answers) {
    for (const p of a.series) {
      const i = index.get(Date.parse(p.at));
      const o = i === undefined ? undefined : out[i];
      if (o) addCounts(o, p.counts);
    }
  }
  return out;
}

function previousRange(cur: Ingest) {
  const from = Date.parse(cur.from);
  const span = Date.parse(cur.to) - from;
  return { from: iso(from - span), to: cur.from, bucket: cur.bucket };
}

export type WebTrafficData = {
  state: "loading" | "error" | "data";
  error: unknown;
  refetch: () => void;
  buckets: ChartBucket[];
  current: Outcomes[];
  // null until the previous window has loaded, or when it failed
  previous: Outcomes[] | null;
  totals: Outcomes;
  prevTotals: Outcomes | null;
  // the answers per site, in the order of the sites passed in
  bySite: (Ingest | undefined)[];
};

// the ingest counts of the given sites over a range, summed, with the
// previous window of the same length from the same route
export function useWebTraffic(
  sites: Site[],
  range: WebRange,
  opts: { refetchInterval?: number | false } = {},
): WebTrafficData {
  const current = useQueries({
    queries: sites.map((s) => ({
      ...(range === "24h" ? ingestDayQuery(s.id) : ingestMonthQuery(s.id)),
      refetchInterval: opts.refetchInterval ?? false,
    })),
  });
  const prev = useQueries({
    queries: sites.map((s, i) => {
      const cur = current[i]?.data;
      const key: QueryKey = [...keys.ingest(s.id, `${range}-previous`), cur?.from ?? null];
      return {
        queryKey: key,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          cur
            ? api.ingest(s.id, previousRange(cur), signal)
            : Promise.reject(new Error("no range")),
        enabled: !!cur,
        staleTime: 5 * 60_000,
      };
    }),
  });

  const answers = current.map((q) => q.data);
  const prevAnswers = prev.map((q) => q.data);
  const failed = current.find((q) => q.error);
  const loading = current.some((q) => q.isPending);
  const firstCur = answers.find((a): a is Ingest => !!a);
  const firstPrev = prevAnswers.find((a): a is Ingest => !!a);
  const prevReady = prev.length > 0 && prev.every((q) => q.data !== undefined);

  // the fold only reruns when an answer changes, not on every render
  const stamp = current.map((q) => q.dataUpdatedAt).join(",");
  const prevStamp = prev.map((q) => q.dataUpdatedAt).join(",");
  const folded = useMemo(() => {
    const buckets = firstCur ? ingestBuckets(firstCur) : [];
    const cur = fold(
      answers.filter((a): a is Ingest => !!a),
      buckets,
    );
    const totals = zero();
    for (const a of answers) if (a) addCounts(totals, a.totals);
    let p: Outcomes[] | null = null;
    let pt: Outcomes | null = null;
    if (prevReady && firstPrev) {
      const pb = ingestBuckets(firstPrev);
      const pf = fold(
        prevAnswers.filter((a): a is Ingest => !!a),
        pb,
      );
      // compared position by position: both windows have the same length
      p = buckets.map((_, i) => pf[i] ?? zero());
      pt = zero();
      for (const a of prevAnswers) if (a) addCounts(pt, a.totals);
    }
    return { buckets, cur, totals, p, pt };
  }, [stamp, prevStamp, prevReady]);

  return {
    state: failed ? "error" : loading ? "loading" : "data",
    error: failed?.error ?? null,
    refetch: () => {
      for (const q of current) if (q.error) q.refetch();
    },
    buckets: folded.buckets,
    current: folded.cur,
    previous: folded.p,
    totals: folded.totals,
    prevTotals: folded.pt,
    bySite: answers,
  };
}

const pctFmt = (r: number) => (r === 0 ? "0%" : r < 0.001 ? "<0.1%" : `${(r * 100).toFixed(1)}%`);

export function formatShare(part: number, all: number): string {
  return all === 0 ? "n/a" : pctFmt(part / all);
}

function countTrend(
  cur: number,
  prev: number,
  range: WebRange,
  higherIsBetter: boolean,
): Trend | null {
  if (prev === 0) return null;
  const r = (cur - prev) / prev;
  if (Math.abs(r) < 0.0005)
    return { text: "no change", direction: "flat", good: null, period: PERIOD[range] };
  const up = r > 0;
  return {
    text: `${Math.abs(r * 100).toFixed(1)}%`,
    direction: up ? "up" : "down",
    good: up === higherIsBetter,
    period: PERIOD[range],
  };
}

function shareTrend(cur: Outcomes, prev: Outcomes, range: WebRange): Trend | null {
  if (cur.all === 0 || prev.all === 0) return null;
  const d = (heldBack(cur) / cur.all - heldBack(prev) / prev.all) * 100;
  if (Math.abs(d) < 0.05)
    return { text: "no change", direction: "flat", good: null, period: PERIOD[range] };
  return {
    text: `${Math.abs(d).toFixed(1)} pt`,
    direction: d > 0 ? "up" : "down",
    good: d < 0,
    period: PERIOD[range],
  };
}

type Metric = "accepted" | "held";

// the stat tiles of the web traffic and the chart they drive: pressing a
// tile puts its series in the chart. `extra` tiles (api numbers) sit in the
// same row but do not drive the chart.
export function WebTraffic({
  data,
  range,
  extra,
  label,
}: {
  data: WebTrafficData;
  range: WebRange;
  extra?: ReactNode;
  label: string;
}) {
  const [metric, setMetric] = useState<Metric>("accepted");
  const { totals, prevTotals, current, previous, buckets } = data;
  const tileState = data.state === "data" ? (totals.all === 0 ? "empty" : "data") : data.state;
  const per = range === "24h" ? "hour" : "day";
  const accepted = current.map((o) => o.accepted);
  const held = current.map(heldBack);

  const chart =
    metric === "accepted"
      ? {
          title: `Accepted events per ${per}`,
          series: [
            { key: "accepted", label: "Accepted", tone: "series-1" as const, values: accepted },
            {
              key: "quarantined",
              label: "Quarantined",
              tone: "warning" as const,
              values: current.map((o) => o.quarantined),
            },
          ],
          previous: previous?.map((o) => o.accepted + o.quarantined) ?? null,
          higherIsBetter: true,
        }
      : {
          title: `Held back events per ${per}`,
          series: [
            {
              key: "quarantined",
              label: "Quarantined",
              tone: "warning" as const,
              values: current.map((o) => o.quarantined),
            },
            {
              key: "bot",
              label: "Bots",
              tone: "series-3" as const,
              values: current.map((o) => o.bot),
            },
            {
              key: "rejected",
              label: "Rejected",
              tone: "critical" as const,
              values: current.map((o) => o.rejected),
            },
          ],
          previous: previous?.map(heldBack) ?? null,
          higherIsBetter: false,
        };
  const chartState =
    data.state === "data"
      ? (metric === "accepted" ? totals.accepted + totals.quarantined : heldBack(totals)) === 0
        ? "empty"
        : "data"
      : data.state;

  return (
    <div className="flex flex-col gap-4">
      <section
        aria-label={label}
        className={cn(
          "grid grid-cols-2 gap-3",
          extra ? "lg:grid-cols-4" : "lg:grid-cols-[repeat(2,minmax(0,18rem))]",
        )}
      >
        <StatTile
          label="Accepted events"
          hint="Events stored in the primary web dashboard"
          value={formatCount(totals.accepted)}
          numeric={totals.accepted}
          format={formatCount}
          state={tileState}
          emptyText="No events in this range"
          trend={prevTotals ? countTrend(totals.accepted, prevTotals.accepted, range, true) : null}
          spark={
            <Sparkline
              values={accepted}
              previous={previous?.map((o) => o.accepted)}
              label={`Accepted events per ${per}`}
            />
          }
          pressed={metric === "accepted"}
          onPress={() => setMetric("accepted")}
        />
        <StatTile
          label="Held back"
          hint="Quarantined, bots and rejected events, as a share of all events"
          value={formatShare(heldBack(totals), totals.all)}
          state={tileState}
          emptyText="No events in this range"
          trend={prevTotals ? shareTrend(totals, prevTotals, range) : null}
          spark={
            <Sparkline
              values={held}
              previous={previous?.map(heldBack)}
              label={`Held back events per ${per}`}
            />
          }
          pressed={metric === "held"}
          onPress={() => setMetric("held")}
        />
        {extra}
      </section>
      <TimeChart
        title={chart.title}
        description={`Counts per UTC ${per}${range === "30d" ? ", the first and last day partial" : ""}`}
        buckets={buckets}
        series={chart.series}
        previous={chart.previous}
        previousLabel={range === "24h" ? "Previous 24 hours" : "Previous 30 days"}
        higherIsBetter={chart.higherIsBetter}
        state={chartState}
        error={data.error}
        onRetry={data.refetch}
        emptyTitle={
          metric === "accepted" ? "No events in this range" : "Nothing held back in this range"
        }
        emptyText={
          metric === "accepted"
            ? "Counts appear here a few minutes after the first events arrive."
            : "Quarantined, bot and rejected events show here when there are any."
        }
      />
    </div>
  );
}
